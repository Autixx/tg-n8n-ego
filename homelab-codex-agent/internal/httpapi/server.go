package httpapi

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"homelab-codex-agent/internal/attachments"
	"homelab-codex-agent/internal/auth"
	"homelab-codex-agent/internal/codex"
	"homelab-codex-agent/internal/config"
	"homelab-codex-agent/internal/decompose"
	"homelab-codex-agent/internal/jobs"
	"homelab-codex-agent/internal/sessions"
)

const maxInputBytes = 256 * 1024

type Runner interface {
	Run(jobID, jobDir string, imagePaths []string) error
	RunPrompt(jobID, jobDir, prompt string, imagePaths []string) error
}

type AttachmentStager interface {
	Validate(requests []jobs.AttachmentRequest) error
	Stage(ctx context.Context, jobDir string, requests []jobs.AttachmentRequest) ([]jobs.StagedAttachment, error)
}

type Server struct {
	cfg      config.Config
	store    *jobs.Store
	runner   Runner
	stager   AttachmentStager
	sessions *sessions.Store
	logger   *log.Logger
}

type processResponse struct {
	JobID      string           `json:"job_id"`
	Status     string           `json:"status"`
	Result     json.RawMessage  `json:"result,omitempty"`
	Eventlog   []map[string]any `json:"eventlog,omitempty"`
	Warnings   []string         `json:"warnings,omitempty"`
	Error      string           `json:"error,omitempty"`
	StdoutTail string           `json:"stdout_tail,omitempty"`
	StderrTail string           `json:"stderr_tail,omitempty"`
}

type decomposeRequest struct {
	ThreadID    string                   `json:"thread_id"`
	Mode        string                   `json:"mode,omitempty"`
	Source      string                   `json:"source,omitempty"`
	Text        string                   `json:"text,omitempty"`
	Attachments []jobs.AttachmentRequest `json:"attachments,omitempty"`
}

type decomposeResponse struct {
	JobID    string           `json:"job_id"`
	Status   string           `json:"status"`
	ThreadID string           `json:"thread_id"`
	Session  sessionResponse  `json:"session"`
	Result   decompose.Result `json:"result,omitempty"`
	Warnings []string         `json:"warnings"`
	Error    string           `json:"error,omitempty"`
}

type sessionResponse struct {
	ID             string `json:"id"`
	CodexSessionID string `json:"codex_session_id"`
	TurnCount      int    `json:"turn_count"`
	MaxTurns       int    `json:"max_turns"`
	Rotated        bool   `json:"rotated"`
}

func NewServer(cfg config.Config, store *jobs.Store, runner Runner, logger *log.Logger) *Server {
	return NewServerWithRegistry(cfg, store, runner, nil, logger)
}

func NewServerWithRegistry(cfg config.Config, store *jobs.Store, runner Runner, registry attachments.FileRegistry, logger *log.Logger) *Server {
	stager := attachments.NewStager(attachments.Config{
		Token:      cfg.DashboardAttachmentToken,
		MaxCount:   cfg.MaxAttachments,
		MaxBytes:   cfg.MaxAttachmentBytes,
		AllowImage: cfg.AllowImageAttachments,
		Registry:   registry,
	})
	return NewServerWithStager(cfg, store, runner, stager, logger)
}

func NewServerWithStager(cfg config.Config, store *jobs.Store, runner Runner, stager AttachmentStager, logger *log.Logger) *Server {
	return NewServerWithStagerAndSessions(cfg, store, runner, stager, sessions.NewStore(cfg.SessionStorePath), logger)
}

func NewServerWithStagerAndSessions(cfg config.Config, store *jobs.Store, runner Runner, stager AttachmentStager, sessionStore *sessions.Store, logger *log.Logger) *Server {
	return &Server{cfg: cfg, store: store, runner: runner, stager: stager, sessions: sessionStore, logger: logger}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /v1/projectego/process", s.handleProcess)
	mux.HandleFunc("POST /v2/projectego/decompose", s.handleDecompose)
	mux.HandleFunc("GET /v1/jobs/{job_id}", s.handleJobStatus)
	mux.HandleFunc("GET /v1/jobs/{job_id}/result", s.handleJobResult)
	mux.HandleFunc("GET /v1/jobs/{job_id}/eventlog", s.handleJobEventlog)
	return mux
}

func (s *Server) handleDecompose(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}

	var req decomposeRequest
	reader := http.MaxBytesReader(w, r.Body, maxInputBytes+64*1024)
	if err := json.NewDecoder(reader).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "", fmt.Errorf("invalid request json: %w", err))
		return
	}
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.Mode = strings.TrimSpace(req.Mode)
	if req.Mode == "" {
		req.Mode = s.cfg.DefaultMode
	}
	if req.ThreadID == "" {
		writeError(w, http.StatusBadRequest, "", errors.New("thread_id is required"))
		return
	}
	if !config.IsAllowedMode(req.Mode) {
		writeError(w, http.StatusBadRequest, "", fmt.Errorf("mode is not allowed: %s", req.Mode))
		return
	}
	if strings.TrimSpace(req.Text) == "" && len(req.Attachments) == 0 {
		writeError(w, http.StatusBadRequest, "", errors.New("text is required unless attachments are present"))
		return
	}
	if len([]byte(req.Text)) > maxInputBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "", errors.New("text exceeds 256 KiB"))
		return
	}

	warnings := []string{"codex_session_resume_unavailable"}
	if !s.cfg.SessionEnabled {
		warnings = append(warnings, "session_manager_disabled")
	}
	bootstrap, err := os.ReadFile(s.cfg.PromptPathV2)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "", fmt.Errorf("read v2 prompt: %w", err))
		return
	}
	bootstrapHash := fmt.Sprintf("%x", sha256.Sum256(bootstrap))
	now := time.Now().UTC()
	session := disabledSession(req.ThreadID, s.cfg.SessionPurpose, bootstrapHash, s.cfg.SessionMaxTurns, now)
	rotated := false
	if s.cfg.SessionEnabled {
		var reused bool
		var rotatedSummary string
		session, reused, rotatedSummary, err = s.sessions.GetActive(req.ThreadID, s.cfg.SessionPurpose, bootstrapHash, s.cfg.SessionMaxTurns, s.cfg.SessionMaxAge, now)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "", err)
			return
		}
		rotated = !reused && rotatedSummary != ""
		if !reused {
			session, err = s.sessions.Create(req.ThreadID, s.cfg.SessionPurpose, bootstrapHash, rotatedSummary, s.cfg.SessionMaxTurns, now)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "", err)
				return
			}
		}
	}

	jobReq := jobs.Request{
		Mode:        req.Mode,
		Text:        req.Text,
		Source:      req.Source,
		Attachments: req.Attachments,
	}
	job, err := s.store.Create(jobReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "", err)
		return
	}

	staged, stagingWarnings, ok := s.stageV2(r.Context(), job.Dir, req.Text, req.Attachments)
	warnings = append(warnings, stagingWarnings...)
	if !ok {
		status := jobs.Status{JobID: job.ID, Status: "error", Mode: req.Mode, CreatedAt: now, Error: strings.Join(stagingWarnings, "; ")}
		_ = s.store.WriteStatus(job, status)
		writeJSON(w, http.StatusBadRequest, decomposeResponse{
			JobID: job.ID, Status: "error", ThreadID: req.ThreadID, Session: makeSessionResponse(session, rotated), Warnings: warnings, Error: "attachments could not be processed and no text fallback is available",
		})
		return
	}

	stagingEvents, _ := os.ReadFile(filepath.Join(job.Dir, "eventlog.jsonl"))
	status := jobs.Status{JobID: job.ID, Status: "running", Mode: req.Mode, CreatedAt: now}
	_ = s.store.WriteStatus(job, status)

	imagePaths := make([]string, 0, len(staged))
	attachmentMetadata := make([]sessions.AttachmentMetadata, 0, len(staged))
	for _, attachment := range staged {
		localPath := filepath.Join(job.Dir, filepath.FromSlash(attachment.RelativePath))
		imagePaths = append(imagePaths, localPath)
		attachmentMetadata = append(attachmentMetadata, sessions.AttachmentMetadata{
			AttachmentID: attachment.ID,
			Kind:         attachment.Kind,
			FileName:     attachment.FileName,
			MIMEType:     attachment.MIMEType,
			SizeBytes:    attachment.SizeBytes,
			LocalPath:    attachment.RelativePath,
		})
	}
	prompt := buildV2Prompt(string(bootstrap), session.Summary, req, warnings)
	if err := s.runner.RunPrompt(job.ID, job.Dir, prompt, imagePaths); err != nil {
		if s.cfg.SessionEnabled {
			_ = s.sessions.MarkFailed(session.ID, err.Error(), time.Now().UTC())
		}
		preserveStagingEvents(filepath.Join(job.Dir, "eventlog.jsonl"), stagingEvents)
		status.Status = "error"
		status.Error = err.Error()
		_ = s.store.WriteStatus(job, status)
		writeJSON(w, http.StatusInternalServerError, decomposeResponse{
			JobID: job.ID, Status: "error", ThreadID: req.ThreadID, Session: makeSessionResponse(session, rotated), Warnings: warnings, Error: err.Error(),
		})
		return
	}
	preserveStagingEvents(filepath.Join(job.Dir, "eventlog.jsonl"), stagingEvents)

	resultPath := filepath.Join(job.Dir, "result.json")
	resultBytes, err := os.ReadFile(resultPath)
	if err != nil {
		runErr := fmt.Errorf("result.json not found for job_id=%s", job.ID)
		if s.cfg.SessionEnabled {
			_ = s.sessions.MarkFailed(session.ID, runErr.Error(), time.Now().UTC())
		}
		status.Status = "error"
		status.Error = runErr.Error()
		_ = s.store.WriteStatus(job, status)
		writeJSON(w, http.StatusInternalServerError, decomposeResponse{
			JobID: job.ID, Status: "error", ThreadID: req.ThreadID, Session: makeSessionResponse(session, rotated), Warnings: warnings, Error: runErr.Error(),
		})
		return
	}
	result, err := decompose.ParseAndValidate(resultBytes)
	if err != nil {
		s.logger.Printf("v2 result validation failed job_id=%s error=%v raw_result=%s", job.ID, err, resultBytes)
		if s.cfg.SessionEnabled {
			_ = s.sessions.MarkFailed(session.ID, err.Error(), time.Now().UTC())
		}
		status.Status = "error"
		status.Error = err.Error()
		_ = s.store.WriteStatus(job, status)
		writeJSON(w, http.StatusInternalServerError, decomposeResponse{
			JobID: job.ID, Status: "error", ThreadID: req.ThreadID, Session: makeSessionResponse(session, rotated), Warnings: warnings, Error: err.Error(),
		})
		return
	}

	if s.cfg.SessionEnabled {
		session, _, err = s.sessions.RecordTurn(session.ID, req.Mode, req.Source, req.Text, resultBytes, attachmentMetadata, time.Now().UTC())
		if err != nil {
			writeError(w, http.StatusInternalServerError, job.ID, err)
			return
		}
	}
	status.Status = "done"
	status.ResultPath = resultPath
	_ = s.store.WriteStatus(job, status)
	writeJSON(w, http.StatusOK, decomposeResponse{
		JobID: job.ID, Status: "done", ThreadID: req.ThreadID, Session: makeSessionResponse(session, rotated), Result: result, Warnings: warnings,
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(s.cfg.Listen)
	if err == nil && host != "127.0.0.1" && host != "localhost" {
		if !s.authorized(w, r) {
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleProcess(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}

	var req jobs.Request
	reader := http.MaxBytesReader(w, r.Body, maxInputBytes+64*1024)
	if err := json.NewDecoder(reader).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "", fmt.Errorf("invalid request json: %w", err))
		return
	}

	req.Mode = strings.TrimSpace(req.Mode)
	if req.Mode == "" {
		req.Mode = s.cfg.DefaultMode
	}
	if !config.IsAllowedMode(req.Mode) {
		writeError(w, http.StatusBadRequest, "", fmt.Errorf("mode is not allowed: %s", req.Mode))
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "", errors.New("text is required"))
		return
	}
	if len([]byte(req.Text)) > maxInputBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "", errors.New("text exceeds 256 KiB"))
		return
	}
	if err := s.stager.Validate(req.Attachments); err != nil {
		writeError(w, http.StatusBadRequest, "", err)
		return
	}

	job, err := s.store.Create(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "", err)
		return
	}
	staged, err := s.stager.Stage(r.Context(), job.Dir, req.Attachments)
	if err != nil {
		s.logger.Printf("job attachment error job_id=%s error=%v", job.ID, err)
		status := jobs.Status{
			JobID:     job.ID,
			Status:    "error",
			Mode:      req.Mode,
			CreatedAt: time.Now().UTC(),
			Error:     err.Error(),
		}
		_ = s.store.WriteStatus(job, status)
		writeRunError(w, job, err)
		return
	}
	stagingEvents, _ := os.ReadFile(filepath.Join(job.Dir, "eventlog.jsonl"))

	status := jobs.Status{
		JobID:     job.ID,
		Status:    "running",
		Mode:      req.Mode,
		CreatedAt: time.Now().UTC(),
	}
	_ = s.store.WriteStatus(job, status)

	imagePaths := make([]string, 0, len(staged))
	for _, attachment := range staged {
		imagePaths = append(imagePaths, filepath.Join(job.Dir, filepath.FromSlash(attachment.RelativePath)))
	}
	if err := s.runner.Run(job.ID, job.Dir, imagePaths); err != nil {
		s.logger.Printf("job error job_id=%s error=%v", job.ID, err)
		preserveStagingEvents(filepath.Join(job.Dir, "eventlog.jsonl"), stagingEvents)
		if errors.Is(err, codex.ErrImageAttachmentsUnsupported) {
			_ = attachments.AppendEvent(job.Dir, "vision_unsupported", "", "", err.Error())
		}
		status.Status = "error"
		status.Error = err.Error()
		_ = s.store.WriteStatus(job, status)
		writeRunError(w, job, err)
		return
	}
	preserveStagingEvents(filepath.Join(job.Dir, "eventlog.jsonl"), stagingEvents)

	resultPath := filepath.Join(job.Dir, "result.json")
	result, err := os.ReadFile(resultPath)
	if err != nil {
		runErr := fmt.Errorf("result.json not found for job_id=%s", job.ID)
		status.Status = "error"
		status.Error = runErr.Error()
		_ = s.store.WriteStatus(job, status)
		writeRunError(w, job, runErr)
		return
	}
	if !json.Valid(result) {
		runErr := fmt.Errorf("result.json is not valid JSON for job_id=%s", job.ID)
		status.Status = "error"
		status.Error = runErr.Error()
		_ = s.store.WriteStatus(job, status)
		writeRunError(w, job, runErr)
		return
	}

	eventlog, warnings := readEventlog(filepath.Join(job.Dir, "eventlog.jsonl"))
	status.Status = "done"
	status.ResultPath = resultPath
	_ = s.store.WriteStatus(job, status)

	s.logger.Printf("job done job_id=%s warnings=%d", job.ID, len(warnings))
	writeJSON(w, http.StatusOK, processResponse{
		JobID:    job.ID,
		Status:   "done",
		Result:   json.RawMessage(result),
		Eventlog: eventlog,
		Warnings: warnings,
	})
}

func (s *Server) stageV2(ctx context.Context, jobDir, text string, requests []jobs.AttachmentRequest) ([]jobs.StagedAttachment, []string, bool) {
	if len(requests) == 0 {
		return nil, nil, true
	}
	imageRequests := make([]jobs.AttachmentRequest, 0, len(requests))
	warnings := make([]string, 0)
	for _, request := range requests {
		if request.Kind == "image" {
			imageRequests = append(imageRequests, request)
			continue
		}
		warnings = append(warnings, "file_text_extraction_unavailable")
	}
	if len(imageRequests) == 0 {
		if strings.TrimSpace(text) == "" {
			warnings = append(warnings, "text_fallback_unavailable")
			return nil, warnings, false
		}
		warnings = append(warnings, "continued_text_only")
		return nil, warnings, true
	}
	staged, err := s.stager.Stage(ctx, jobDir, imageRequests)
	if err == nil {
		return staged, warnings, true
	}
	warnings = append(warnings, "attachment_processing_failed")
	if strings.TrimSpace(text) == "" {
		warnings = append(warnings, "text_fallback_unavailable")
		return nil, warnings, false
	}
	warnings = append(warnings, "continued_text_only")
	return nil, warnings, true
}

func disabledSession(threadID, purpose, bootstrapHash string, maxTurns int, now time.Time) sessions.Session {
	return sessions.Session{
		ID:             "stateless-" + threadID,
		ThreadID:       threadID,
		Purpose:        purpose,
		CodexSessionID: "fallback-stateless",
		Status:         sessions.StatusClosed,
		MaxTurns:       maxTurns,
		BootstrapHash:  bootstrapHash,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastUsedAt:     now,
	}
}

func makeSessionResponse(session sessions.Session, rotated bool) sessionResponse {
	return sessionResponse{
		ID:             session.ID,
		CodexSessionID: session.CodexSessionID,
		TurnCount:      session.TurnCount,
		MaxTurns:       session.MaxTurns,
		Rotated:        rotated,
	}
}

func buildV2Prompt(bootstrap, summary string, req decomposeRequest, warnings []string) string {
	type safeAttachment struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		FileName  string `json:"fileName"`
		MIMEType  string `json:"mimeType"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	attachments := make([]safeAttachment, 0, len(req.Attachments))
	for _, attachment := range req.Attachments {
		attachments = append(attachments, safeAttachment{
			ID:        attachment.ID,
			Kind:      attachment.Kind,
			FileName:  attachment.FileName,
			MIMEType:  attachment.MIMEType,
			SizeBytes: attachment.SizeBytes,
		})
	}
	message := struct {
		Mode        string           `json:"mode"`
		Source      string           `json:"source,omitempty"`
		Text        string           `json:"text,omitempty"`
		Attachments []safeAttachment `json:"attachments,omitempty"`
		Warnings    []string         `json:"warnings,omitempty"`
	}{
		Mode:        req.Mode,
		Source:      req.Source,
		Text:        req.Text,
		Attachments: attachments,
		Warnings:    warnings,
	}
	messageJSON, _ := json.MarshalIndent(message, "", "  ")
	return fmt.Sprintf(`%s

Session resume is unavailable in this runner. Use this compact session summary as context, but do not invent details:
%s

Current request:
%s

Create result.json in the current directory using the v2 output schema. Preserve existing eventlog.jsonl entries and append a short JSONL log of your actions.
`, bootstrap, summary, string(messageJSON))
}

func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	jobDir, ok := s.validJobDir(w, r.PathValue("job_id"))
	if !ok {
		return
	}
	serveJSONFile(w, filepath.Join(jobDir, "status.json"))
}

func (s *Server) handleJobResult(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	jobDir, ok := s.validJobDir(w, r.PathValue("job_id"))
	if !ok {
		return
	}
	serveJSONFile(w, filepath.Join(jobDir, "result.json"))
}

func (s *Server) handleJobEventlog(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	jobDir, ok := s.validJobDir(w, r.PathValue("job_id"))
	if !ok {
		return
	}
	eventlog, _ := readEventlog(filepath.Join(jobDir, "eventlog.jsonl"))
	writeJSON(w, http.StatusOK, eventlog)
}

func (s *Server) authorized(w http.ResponseWriter, r *http.Request) bool {
	if auth.CheckRequest(r, s.cfg.Token) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	return false
}

func (s *Server) validJobDir(w http.ResponseWriter, jobID string) (string, bool) {
	jobDir, err := s.store.JobDir(jobID)
	if err != nil {
		writeError(w, http.StatusBadRequest, jobID, err)
		return "", false
	}
	return jobDir, true
}

func readEventlog(path string) ([]map[string]any, []string) {
	file, err := os.Open(path)
	if err != nil {
		return []map[string]any{}, []string{"eventlog.jsonl is missing"}
	}
	defer file.Close()

	var events []map[string]any
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(text), &event); err != nil {
			return []map[string]any{}, []string{fmt.Sprintf("eventlog.jsonl has invalid JSON on line %d", line)}
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return []map[string]any{}, []string{"eventlog.jsonl could not be read"}
	}
	if events == nil {
		events = []map[string]any{}
	}
	return events, nil
}

func writeRunError(w http.ResponseWriter, job jobs.Job, err error) {
	writeJSON(w, http.StatusInternalServerError, processResponse{
		JobID:      job.ID,
		Status:     "error",
		Error:      err.Error(),
		StdoutTail: codex.TailFile(filepath.Join(job.Dir, "stdout.log"), 8192),
		StderrTail: codex.TailFile(filepath.Join(job.Dir, "stderr.log"), 8192),
	})
}

func serveJSONFile(w http.ResponseWriter, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "", err)
		return
	}
	if !json.Valid(data) {
		writeError(w, http.StatusInternalServerError, "", fmt.Errorf("%s is not valid JSON", filepath.Base(path)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, status int, jobID string, err error) {
	writeJSON(w, status, processResponse{
		JobID:  jobID,
		Status: "error",
		Error:  err.Error(),
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func preserveStagingEvents(path string, stagingEvents []byte) {
	if len(stagingEvents) == 0 {
		return
	}
	current, err := os.ReadFile(path)
	if err != nil {
		_ = os.WriteFile(path, stagingEvents, 0o600)
		return
	}
	for _, action := range []string{"read_attachments", "download_attachment", "stage_attachment", "vision_attachment_ready"} {
		if !strings.Contains(string(current), `"action":"`+action+`"`) {
			combined := append(append([]byte(nil), stagingEvents...), current...)
			_ = os.WriteFile(path, combined, 0o600)
			return
		}
	}
}
