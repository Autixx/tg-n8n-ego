package httpapi

import (
	"bytes"
	"encoding/json"
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
	unsupported bool
	imageCount  int
	result      string
}

func (r *fakeRunner) Run(_ string, jobDir string, imagePaths []string) error {
	return r.RunPrompt("", jobDir, "", imagePaths)
}

func (r *fakeRunner) RunPrompt(_ string, jobDir, _ string, imagePaths []string) error {
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
