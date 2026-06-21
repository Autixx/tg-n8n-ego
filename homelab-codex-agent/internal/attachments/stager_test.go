package attachments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab-codex-agent/internal/jobs"
)

func TestValidateAttachmentMetadata(t *testing.T) {
	t.Parallel()
	stager := NewStager(Config{Token: "service-secret", MaxCount: 2, MaxBytes: 10, AllowImage: true})
	valid := jobs.AttachmentRequest{
		ID: "ATT_1", Kind: "image", FileName: "ui.png", MIMEType: "image/png",
		SizeBytes: 4, DownloadURL: "http://127.0.0.1:19100/internal/ATT_1",
	}
	if err := stager.Validate([]jobs.AttachmentRequest{valid}); err != nil {
		t.Fatalf("valid attachment rejected: %v", err)
	}

	tests := []struct {
		name        string
		attachments []jobs.AttachmentRequest
		want        string
	}{
		{name: "too many", attachments: []jobs.AttachmentRequest{valid, valid, valid}, want: "too many attachments"},
		{name: "oversized", attachments: []jobs.AttachmentRequest{withSize(valid, 11)}, want: "sizeBytes exceeds"},
		{name: "unsupported MIME", attachments: []jobs.AttachmentRequest{withMIME(valid, "application/pdf", "ui.pdf")}, want: "unsupported MIME"},
		{name: "invalid URL", attachments: []jobs.AttachmentRequest{withURL(valid, "http://169.254.169.254/latest/meta-data")}, want: "loopback"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := stager.Validate(tc.attachments)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestStageDownloadsAndWritesArtifacts(t *testing.T) {
	t.Parallel()
	const token = "dashboard-service-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png-data"))
	}))
	defer server.Close()

	jobDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(jobDir, "input.md"), []byte("Analyze this."), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := &fakeRegistry{}
	stager := NewStager(Config{Token: token, MaxCount: 4, MaxBytes: 1024, AllowImage: true, Registry: registry})
	request := jobs.AttachmentRequest{
		ID: "ATT_1", Kind: "image", FileName: "../../ui sketch.png", MIMEType: "image/png",
		SizeBytes: 8, DownloadURL: server.URL + "/attachment",
	}
	staged, err := stager.Stage(context.Background(), jobDir, []jobs.AttachmentRequest{request})
	if err != nil {
		t.Fatalf("Stage() error: %v", err)
	}
	if len(staged) != 1 || staged[0].FileName != "ui_sketch.png" {
		t.Fatalf("staged = %#v", staged)
	}
	if len(registry.paths) != 1 || registry.paths[0] != filepath.Join(jobDir, "attachments", "ui_sketch.png") {
		t.Fatalf("registered paths = %#v", registry.paths)
	}
	data, err := os.ReadFile(filepath.Join(jobDir, "attachments", staged[0].FileName))
	if err != nil || string(data) != "png-data" {
		t.Fatalf("downloaded data = %q, err = %v", data, err)
	}
	metadata, err := os.ReadFile(filepath.Join(jobDir, "attachments.json"))
	if err != nil || !json.Valid(metadata) {
		t.Fatalf("attachments.json invalid: %v", err)
	}
	input, _ := os.ReadFile(filepath.Join(jobDir, "input.md"))
	if !strings.Contains(string(input), "attachments/ui_sketch.png") {
		t.Fatalf("input.md lacks attachment section: %s", input)
	}
	eventlog, _ := os.ReadFile(filepath.Join(jobDir, "eventlog.jsonl"))
	for _, action := range []string{"read_attachments", "download_attachment", "stage_attachment", "vision_attachment_ready"} {
		if !strings.Contains(string(eventlog), `"action":"`+action+`"`) {
			t.Errorf("eventlog lacks %s: %s", action, eventlog)
		}
	}
}

type fakeRegistry struct {
	paths []string
}

func (r *fakeRegistry) Register(paths []string, _ time.Time) error {
	r.paths = append(r.paths, paths...)
	return nil
}

func TestValidateDownloadURL(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{"http://127.0.0.1:19100/a", "https://localhost/a", "http://[::1]:19100/a"} {
		if err := ValidateDownloadURL(valid); err != nil {
			t.Errorf("ValidateDownloadURL(%q) = %v", valid, err)
		}
	}
	for _, invalid := range []string{"file:///tmp/a", "http://example.com/a", "http://10.0.0.1/a", "http://169.254.169.254/a", "http:///missing"} {
		if err := ValidateDownloadURL(invalid); err == nil {
			t.Errorf("ValidateDownloadURL(%q) unexpectedly succeeded", invalid)
		}
	}
}

func withSize(request jobs.AttachmentRequest, size int64) jobs.AttachmentRequest {
	request.SizeBytes = size
	return request
}

func withMIME(request jobs.AttachmentRequest, mimeType, fileName string) jobs.AttachmentRequest {
	request.MIMEType = mimeType
	request.FileName = fileName
	return request
}

func withURL(request jobs.AttachmentRequest, downloadURL string) jobs.AttachmentRequest {
	request.DownloadURL = downloadURL
	return request
}
