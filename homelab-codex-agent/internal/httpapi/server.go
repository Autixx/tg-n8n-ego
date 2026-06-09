package httpapi

import (
	"bufio"
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

	"homelab-codex-agent/internal/auth"
	"homelab-codex-agent/internal/codex"
	"homelab-codex-agent/internal/config"
	"homelab-codex-agent/internal/jobs"
)

const maxInputBytes = 256 * 1024

type Runner interface {
	Run(jobID, jobDir string) error
}

type Server struct {
	cfg    config.Config
	store  *jobs.Store
	runner Runner
	logger *log.Logger
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

func NewServer(cfg config.Config, store *jobs.Store, runner Runner, logger *log.Logger) *Server {
	return &Server{cfg: cfg, store: store, runner: runner, logger: logger}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /v1/projectego/process", s.handleProcess)
	mux.HandleFunc("GET /v1/jobs/{job_id}", s.handleJobStatus)
	mux.HandleFunc("GET /v1/jobs/{job_id}/result", s.handleJobResult)
	mux.HandleFunc("GET /v1/jobs/{job_id}/eventlog", s.handleJobEventlog)
	return mux
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
	reader := http.MaxBytesReader(w, r.Body, maxInputBytes+4096)
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

	job, err := s.store.Create(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "", err)
		return
	}

	status := jobs.Status{
		JobID:     job.ID,
		Status:    "running",
		Mode:      req.Mode,
		CreatedAt: time.Now().UTC(),
	}
	_ = s.store.WriteStatus(job, status)

	if err := s.runner.Run(job.ID, job.Dir); err != nil {
		s.logger.Printf("job error job_id=%s error=%v", job.ID, err)
		status.Status = "error"
		status.Error = err.Error()
		_ = s.store.WriteStatus(job, status)
		writeRunError(w, job, err)
		return
	}

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
