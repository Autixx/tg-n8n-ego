package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab-codex-agent/internal/codex"
	"homelab-codex-agent/internal/config"
	"homelab-codex-agent/internal/jobs"
)

type fakeRunner struct {
	unsupported  bool
	appServerErr error
	imageCount   int
	prompt       string
	appMessage   string
	appThreadID  string
	appCalls     int
	result       string
}

func (r *fakeRunner) Run(_ string, jobDir string, imagePaths []string) error {
	return r.RunPrompt("", jobDir, "", imagePaths)
}

func (r *fakeRunner) RunPrompt(_ string, jobDir, prompt string, imagePaths []string) error {
	r.prompt = prompt
	r.imageCount = len(imagePaths)
	if r.unsupported && len(imagePaths) > 0 {
		return fmt.Errorf("%w: test CLI has no image flag", codex.ErrImageAttachmentsUnsupported)
	}
	result := r.result
	if result == "" {
		result = `{"mode":"structured_breakdown","source_summary":"ok","items":[],"needs_clarification":[],"eventlog_summary":"ok"}`
	}
	return os.WriteFile(filepath.Join(jobDir, "result.json"), []byte(result), 0o600)
}

func (r *fakeRunner) RunAppServer(_ string, jobDir, threadID, _ string, message string, imagePaths []string) (codex.AppServerResult, error) {
	r.appCalls++
	r.appMessage = message
	r.imageCount = len(imagePaths)
	if r.appServerErr != nil {
		return codex.AppServerResult{}, r.appServerErr
	}
	if threadID == "" {
		threadID = "thr_test"
	}
	r.appThreadID = threadID
	return codex.AppServerResult{ThreadID: threadID, UsedResume: true}, r.RunPrompt("", jobDir, "", imagePaths)
}

func TestTextOnlyRequestStillWorks(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t.TempDir())
	runner := &fakeRunner{}
	server := NewServer(cfg, jobs.NewStore(cfg.Workdir, testLogger()), runner, testLogger())

	response := performProcessRequest(t, server.Routes(), cfg.Token, `{
		"mode":"structured_breakdown","source":"test","text":"text-only input"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if runner.imageCount != 0 {
		t.Fatalf("image count = %d", runner.imageCount)
	}
}

func TestUnsupportedCLIImageResponseIsExplicit(t *testing.T) {
	t.Parallel()
	dashboard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	defer dashboard.Close()

	cfg := testConfig(t.TempDir())
	runner := &fakeRunner{unsupported: true}
	server := NewServer(cfg, jobs.NewStore(cfg.Workdir, testLogger()), runner, testLogger())
	body := fmt.Sprintf(`{
		"mode":"structured_breakdown","text":"analyze image",
		"attachments":[{
			"id":"ATT_1","kind":"image","fileName":"ui.png","mimeType":"image/png",
			"sizeBytes":3,"downloadUrl":%q
		}]
	}`, dashboard.URL+"/attachment")
	response := performProcessRequest(t, server.Routes(), cfg.Token, body)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload processResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.Error, "image_attachments_not_supported_by_current_codex_cli") {
		t.Fatalf("error = %q", payload.Error)
	}
	eventlog, err := os.ReadFile(filepath.Join(cfg.Workdir, "jobs", payload.JobID, "eventlog.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(eventlog, []byte(`"action":"vision_unsupported"`)) {
		t.Fatalf("eventlog = %s", eventlog)
	}
}

func TestV2MissingThreadIDReturns400(t *testing.T) {
	t.Parallel()
	cfg, server := newV2TestServer(t, &fakeRunner{})
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"mode":"structured_breakdown","source":"test","text":"input"
	}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestV2MissingTextAndAttachmentsReturns400(t *testing.T) {
	t.Parallel()
	cfg, server := newV2TestServer(t, &fakeRunner{})
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test"
	}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestV2TextAndImageAttachmentAccepted(t *testing.T) {
	t.Parallel()
	dashboard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	defer dashboard.Close()

	runner := &fakeRunner{}
	cfg, server := newV2TestServer(t, runner)
	body := fmt.Sprintf(`{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"analyze image",
		"attachments":[{
			"id":"ATT_1","kind":"image","fileName":"ui.png","mimeType":"image/png",
			"sizeBytes":3,"downloadUrl":%q
		}]
	}`, dashboard.URL+"/attachment")
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if runner.imageCount != 1 {
		t.Fatalf("image count = %d", runner.imageCount)
	}
	var payload decomposeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Session.ID == "" || payload.ThreadID != "projectego-intake" {
		t.Fatalf("payload = %#v", payload)
	}
	if !contains(payload.Warnings, "codex_session_resume_unavailable") {
		t.Fatalf("warnings = %#v", payload.Warnings)
	}
}

func TestV2AttachmentsOnlyImageAccepted(t *testing.T) {
	t.Parallel()
	dashboard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	defer dashboard.Close()

	runner := &fakeRunner{}
	cfg, server := newV2TestServer(t, runner)
	body := fmt.Sprintf(`{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test",
		"attachments":[{
			"id":"ATT_1","kind":"image","fileName":"ui.png","mimeType":"image/png",
			"sizeBytes":3,"downloadUrl":%q
		}]
	}`, dashboard.URL+"/attachment")
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if runner.imageCount != 1 {
		t.Fatalf("image count = %d", runner.imageCount)
	}
}

func TestV2AttachmentFailureWarnsAndContinuesWithText(t *testing.T) {
	t.Parallel()
	dashboard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer dashboard.Close()

	cfg, server := newV2TestServer(t, &fakeRunner{})
	body := fmt.Sprintf(`{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"continue from text",
		"attachments":[{
			"id":"ATT_1","kind":"image","fileName":"ui.png","mimeType":"image/png",
			"sizeBytes":3,"downloadUrl":%q
		}]
	}`, dashboard.URL+"/attachment")
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload decomposeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !contains(payload.Warnings, "attachment_processing_failed") || !contains(payload.Warnings, "continued_text_only") {
		t.Fatalf("warnings = %#v", payload.Warnings)
	}
}

func TestV2RejectsPlaneStyleRunnerOutput(t *testing.T) {
	t.Parallel()
	result := `{"mode":"structured_breakdown","source_summary":"old","items":[{"title":"x","type":"task","project":"Combat Framework","module":"Stagger","summary":"x","details":"x","source_text":"x","priority":"medium","labels":[],"dependencies":[],"acceptance_criteria":[],"needs_clarification":[]}],"needs_clarification":[],"eventlog_summary":"x"}`
	cfg, server := newV2TestServer(t, &fakeRunner{result: result})
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"input"
	}`)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestV2SessionDisabledDoesNotWriteSessionStore(t *testing.T) {
	t.Parallel()
	cfg, server := newV2TestServer(t, &fakeRunner{})
	cfg.SessionEnabled = false
	server.cfg = cfg
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"input"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(cfg.SessionStorePath); !os.IsNotExist(err) {
		t.Fatalf("session store exists or unexpected stat error: %v", err)
	}
	var payload decomposeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !contains(payload.Warnings, "session_manager_disabled") {
		t.Fatalf("warnings = %#v", payload.Warnings)
	}
}

func TestV2FileAttachmentWarnsAndContinuesWithText(t *testing.T) {
	t.Parallel()
	cfg, server := newV2TestServer(t, &fakeRunner{})
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"summarize from text",
		"attachments":[{
			"id":"FILE_1","kind":"file","fileName":"notes.pdf","mimeType":"application/pdf",
			"sizeBytes":42,"downloadUrl":"http://127.0.0.1:19100/internal/FILE_1?token=secret"
		}]
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload decomposeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !contains(payload.Warnings, "file_text_extraction_unavailable") || !contains(payload.Warnings, "continued_text_only") {
		t.Fatalf("warnings = %#v", payload.Warnings)
	}
}

func TestV2AttachmentsOnlyFileReturnsWarningError(t *testing.T) {
	t.Parallel()
	cfg, server := newV2TestServer(t, &fakeRunner{})
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test",
		"attachments":[{
			"id":"FILE_1","kind":"file","fileName":"notes.pdf","mimeType":"application/pdf",
			"sizeBytes":42,"downloadUrl":"http://127.0.0.1:19100/internal/FILE_1"
		}]
	}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload decomposeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !contains(payload.Warnings, "file_text_extraction_unavailable") || !contains(payload.Warnings, "text_fallback_unavailable") {
		t.Fatalf("warnings = %#v", payload.Warnings)
	}
}

func TestV2DoesNotStoreAttachmentSecretsOrRawBytes(t *testing.T) {
	t.Parallel()
	const rawBytes = "png-secret-bytes"
	dashboard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(rawBytes))
	}))
	defer dashboard.Close()

	runner := &fakeRunner{}
	cfg, server := newV2TestServer(t, runner)
	body := fmt.Sprintf(`{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"analyze image",
		"attachments":[{
			"id":"ATT_1","kind":"image","fileName":"ui.png","mimeType":"image/png",
			"sizeBytes":%d,"downloadUrl":%q
		}]
	}`, len(rawBytes), dashboard.URL+"/attachment?token=download-secret")
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	storeData, err := os.ReadFile(cfg.SessionStorePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"download-secret", dashboard.URL, rawBytes} {
		if strings.Contains(runner.prompt, forbidden) {
			t.Fatalf("prompt leaked %q: %s", forbidden, runner.prompt)
		}
		if bytes.Contains(storeData, []byte(forbidden)) {
			t.Fatalf("session store leaked %q: %s", forbidden, storeData)
		}
	}
}

func TestV2AppServerStoresAndResumesThread(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	cfg, server := newV2TestServer(t, runner)
	cfg.RunnerBackend = "appserver"
	server.cfg = cfg

	first := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"first"
	}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	var firstPayload decomposeResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstPayload); err != nil {
		t.Fatal(err)
	}
	if firstPayload.Session.CodexSessionID != "thr_test" {
		t.Fatalf("codex session id = %q", firstPayload.Session.CodexSessionID)
	}
	if contains(firstPayload.Warnings, "codex_session_resume_unavailable") {
		t.Fatalf("unexpected resume fallback warning: %#v", firstPayload.Warnings)
	}

	second := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"second"
	}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", second.Code, second.Body.String())
	}
	if runner.appThreadID != "thr_test" {
		t.Fatalf("app server resumed thread = %q", runner.appThreadID)
	}
	if strings.Contains(runner.appMessage, "You are a structured work-item decomposer") {
		t.Fatalf("app server resume message included bootstrap: %s", runner.appMessage)
	}
}

func TestV2AppServerFallsBackToExec(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{appServerErr: errors.New("app server down")}
	cfg, server := newV2TestServer(t, runner)
	cfg.RunnerBackend = "appserver"
	cfg.RunnerFallback = "exec"
	server.cfg = cfg
	response := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"input"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload decomposeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !contains(payload.Warnings, "codex_appserver_unavailable") {
		t.Fatalf("warnings = %#v", payload.Warnings)
	}
	if runner.prompt == "" {
		t.Fatal("exec fallback prompt was not used")
	}
}

func TestV2OutcomeWebhookReportsFallback(t *testing.T) {
	t.Parallel()
	var payload outcomePayload
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	runner := &fakeRunner{appServerErr: errors.New("app server down")}
	cfg, server := newV2TestServer(t, runner)
	cfg.RunnerBackend = "appserver"
	cfg.RunnerFallback = "exec"
	cfg.OutcomeWebhookURL = webhook.URL
	server.cfg = cfg

	response := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"input"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if payload.Event != "codex_agent_outcome" || payload.Status != "done" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Runner != "exec" || payload.PrimaryRunner != "appserver" || !payload.FallbackUsed {
		t.Fatalf("runner outcome = %#v", payload)
	}
	if !contains(payload.Warnings, "codex_appserver_unavailable") {
		t.Fatalf("warnings = %#v", payload.Warnings)
	}
}

func TestV2SessionMaxTurnsRotatesAppServerThread(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	cfg, server := newV2TestServer(t, runner)
	cfg.RunnerBackend = "appserver"
	cfg.RunnerFallback = "off"
	cfg.SessionMaxTurns = 1
	server.cfg = cfg

	first := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"first"
	}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	var firstPayload decomposeResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstPayload); err != nil {
		t.Fatal(err)
	}

	second := performDecomposeRequest(t, server.Routes(), cfg.Token, `{
		"thread_id":"projectego-intake","mode":"structured_breakdown","source":"test","text":"second"
	}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", second.Code, second.Body.String())
	}
	var secondPayload decomposeResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondPayload); err != nil {
		t.Fatal(err)
	}
	if !secondPayload.Session.Rotated {
		t.Fatalf("session was not marked rotated: %#v", secondPayload.Session)
	}
	if secondPayload.Session.ID == firstPayload.Session.ID {
		t.Fatalf("session id was reused after max turns: %q", secondPayload.Session.ID)
	}
	if runner.appCalls != 2 {
		t.Fatalf("app calls = %d", runner.appCalls)
	}
}

func testConfig(workdir string) config.Config {
	return config.Config{
		Listen:                   "127.0.0.1:19090",
		Token:                    "agent-token",
		Workdir:                  workdir,
		PromptPath:               filepath.Join(workdir, "prompt.md"),
		PromptPathV2:             filepath.Join(workdir, "prompt-v2.md"),
		Timeout:                  time.Minute,
		CodexBin:                 "codex",
		DefaultMode:              "structured_breakdown",
		DashboardAttachmentToken: "dashboard-token",
		MaxAttachments:           4,
		MaxAttachmentBytes:       1024,
		AllowImageAttachments:    true,
		MultimodalMode:           "auto",
		SessionEnabled:           true,
		SessionMaxTurns:          10,
		SessionMaxAge:            time.Hour,
		SessionPurpose:           "projectego-decompose",
		SessionStorePath:         filepath.Join(workdir, "codex-sessions.json"),
		RunnerBackend:            "exec",
		RunnerFallback:           "exec",
		OutcomeWebhookTimeout:    time.Second,
	}
}

func testLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func performProcessRequest(t *testing.T, handler http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/projectego/process", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Codex-Agent-Token", token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func newV2TestServer(t *testing.T, runner *fakeRunner) (config.Config, *Server) {
	t.Helper()
	cfg := testConfig(t.TempDir())
	if err := os.WriteFile(cfg.PromptPathV2, []byte("v2 bootstrap"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg, NewServer(cfg, jobs.NewStore(cfg.Workdir, testLogger()), runner, testLogger())
}

func performDecomposeRequest(t *testing.T, handler http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v2/projectego/decompose", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Codex-Agent-Token", token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
