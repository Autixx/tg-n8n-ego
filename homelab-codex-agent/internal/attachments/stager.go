package attachments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"homelab-codex-agent/internal/jobs"
)

var unsafeFileNameChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type Config struct {
	Token      string
	MaxCount   int
	MaxBytes   int64
	AllowImage bool
	Registry   FileRegistry
}

type FileRegistry interface {
	Register(paths []string, createdAt time.Time) error
}

type Stager struct {
	cfg    Config
	client *http.Client
}

type event struct {
	Action       string    `json:"action"`
	Timestamp    time.Time `json:"timestamp"`
	AttachmentID string    `json:"attachment_id,omitempty"`
	FileName     string    `json:"file_name,omitempty"`
	Message      string    `json:"message,omitempty"`
}

func NewStager(cfg Config) *Stager {
	return NewStagerWithClient(cfg, &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("attachment redirects are not allowed")
		},
	})
}

func NewStagerWithClient(cfg Config, client *http.Client) *Stager {
	return &Stager{cfg: cfg, client: client}
}

func (s *Stager) Validate(requests []jobs.AttachmentRequest) error {
	if len(requests) == 0 {
		return nil
	}
	if !s.cfg.AllowImage {
		return errors.New("image attachments are disabled")
	}
	if s.cfg.Token == "" {
		return errors.New("CODEX_AGENT_DASHBOARD_ATTACHMENT_TOKEN is required for attachments")
	}
	if len(requests) > s.cfg.MaxCount {
		return fmt.Errorf("too many attachments: got %d, maximum is %d", len(requests), s.cfg.MaxCount)
	}

	for i, attachment := range requests {
		if err := s.validateOne(attachment); err != nil {
			return fmt.Errorf("attachment %d: %w", i+1, err)
		}
	}
	return nil
}

func (s *Stager) validateOne(attachment jobs.AttachmentRequest) error {
	if strings.TrimSpace(attachment.ID) == "" {
		return errors.New("id is required")
	}
	if attachment.Kind != "image" {
		return fmt.Errorf("unsupported kind %q", attachment.Kind)
	}
	if _, ok := allowedExtensions(attachment.MIMEType); !ok {
		return fmt.Errorf("unsupported MIME type %q", attachment.MIMEType)
	}
	if attachment.SizeBytes < 0 || attachment.SizeBytes > s.cfg.MaxBytes {
		return fmt.Errorf("sizeBytes exceeds maximum of %d", s.cfg.MaxBytes)
	}
	if err := validateExtension(attachment.FileName, attachment.MIMEType); err != nil {
		return err
	}
	return ValidateDownloadURL(attachment.DownloadURL)
}

func ValidateDownloadURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid downloadUrl: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("downloadUrl must use http or https")
	}
	if parsed.User != nil {
		return errors.New("downloadUrl userinfo is not allowed")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return errors.New("downloadUrl host is required")
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("downloadUrl must target an explicitly allowed loopback host")
	}
	return nil
}

func (s *Stager) Stage(ctx context.Context, jobDir string, requests []jobs.AttachmentRequest) ([]jobs.StagedAttachment, error) {
	if err := s.Validate(requests); err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return nil, nil
	}

	attachmentsDir := filepath.Join(jobDir, "attachments")
	if err := os.Mkdir(attachmentsDir, 0o750); err != nil {
		return nil, fmt.Errorf("create attachments directory: %w", err)
	}
	if err := AppendEvent(jobDir, "read_attachments", "", "", fmt.Sprintf("received %d attachment(s)", len(requests))); err != nil {
		return nil, err
	}

	usedNames := make(map[string]struct{}, len(requests))
	staged := make([]jobs.StagedAttachment, 0, len(requests))
	stagedPaths := make([]string, 0, len(requests))
	for _, attachment := range requests {
		fileName := uniqueFileName(SanitizeFileName(attachment.FileName, attachment.MIMEType), usedNames)
		if err := AppendEvent(jobDir, "download_attachment", attachment.ID, fileName, "downloading attachment"); err != nil {
			return nil, err
		}
		size, err := s.download(ctx, attachment, filepath.Join(attachmentsDir, fileName))
		if err != nil {
			_ = AppendEvent(jobDir, "attachment_error", attachment.ID, fileName, err.Error())
			return nil, fmt.Errorf("download attachment %s: %w", attachment.ID, err)
		}

		item := jobs.StagedAttachment{
			ID:               attachment.ID,
			Kind:             attachment.Kind,
			OriginalFileName: attachment.FileName,
			FileName:         fileName,
			MIMEType:         attachment.MIMEType,
			SizeBytes:        size,
			RelativePath:     filepath.ToSlash(filepath.Join("attachments", fileName)),
		}
		staged = append(staged, item)
		stagedPaths = append(stagedPaths, filepath.Join(attachmentsDir, fileName))
		if err := AppendEvent(jobDir, "stage_attachment", attachment.ID, fileName, "attachment stored in isolated job directory"); err != nil {
			return nil, err
		}
		if err := AppendEvent(jobDir, "vision_attachment_ready", attachment.ID, fileName, "image ready for Codex vision input"); err != nil {
			return nil, err
		}
	}
	if s.cfg.Registry != nil {
		if err := s.cfg.Registry.Register(stagedPaths, time.Now().UTC()); err != nil {
			for _, path := range stagedPaths {
				_ = os.Remove(path)
			}
			_ = os.Remove(attachmentsDir)
			_ = AppendEvent(jobDir, "attachment_registry_error", "", "", err.Error())
			return nil, fmt.Errorf("register staged attachments: %w", err)
		}
	}

	if err := writeJSONExclusive(filepath.Join(jobDir, "attachments.json"), staged); err != nil {
		return nil, fmt.Errorf("write attachments.json: %w", err)
	}
	if err := appendInputSection(filepath.Join(jobDir, "input.md"), staged); err != nil {
		return nil, fmt.Errorf("append attachments to input.md: %w", err)
	}
	return staged, nil
}

func (s *Stager) download(ctx context.Context, attachment jobs.AttachmentRequest, destination string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, attachment.DownloadURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	req.Header.Set("Accept", attachment.MIMEType)

	response, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("dashboard returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > s.cfg.MaxBytes {
		return 0, fmt.Errorf("download exceeds maximum of %d bytes", s.cfg.MaxBytes)
	}
	contentType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if contentType != "" && contentType != attachment.MIMEType && contentType != "application/octet-stream" {
		return 0, fmt.Errorf("download MIME %q does not match declared MIME %q", contentType, attachment.MIMEType)
	}

	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(destination)
		}
	}()

	limited := io.LimitReader(response.Body, s.cfg.MaxBytes+1)
	written, err := io.Copy(file, limited)
	if err != nil {
		return 0, err
	}
	if written > s.cfg.MaxBytes {
		return 0, fmt.Errorf("download exceeds maximum of %d bytes", s.cfg.MaxBytes)
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	keep = true
	return written, nil
}

func SanitizeFileName(fileName, mimeType string) string {
	base := filepath.Base(strings.ReplaceAll(strings.TrimSpace(fileName), "\\", "/"))
	base = unsafeFileNameChars.ReplaceAllString(base, "_")
	base = strings.Trim(base, "._-")
	if base == "" {
		base = "attachment"
	}
	exts, _ := allowedExtensions(mimeType)
	ext := strings.ToLower(filepath.Ext(base))
	validExt := false
	for _, allowed := range exts {
		if ext == allowed {
			validExt = true
			break
		}
	}
	if !validExt {
		base = strings.TrimSuffix(base, filepath.Ext(base)) + exts[0]
	}
	return base
}

func validateExtension(fileName, mimeType string) error {
	exts, ok := allowedExtensions(mimeType)
	if !ok {
		return fmt.Errorf("unsupported MIME type %q", mimeType)
	}
	ext := strings.ToLower(filepath.Ext(filepath.Base(strings.ReplaceAll(fileName, "\\", "/"))))
	for _, allowed := range exts {
		if ext == allowed {
			return nil
		}
	}
	return fmt.Errorf("file extension %q does not match MIME type %q", ext, mimeType)
}

func allowedExtensions(mimeType string) ([]string, bool) {
	switch mimeType {
	case "image/png":
		return []string{".png"}, true
	case "image/jpeg":
		return []string{".jpg", ".jpeg"}, true
	case "image/svg+xml":
		return []string{".svg"}, true
	case "image/webp":
		return []string{".webp"}, true
	default:
		return nil, false
	}
}

func uniqueFileName(fileName string, used map[string]struct{}) string {
	if _, exists := used[fileName]; !exists {
		used[fileName] = struct{}{}
		return fileName
	}
	ext := filepath.Ext(fileName)
	stem := strings.TrimSuffix(fileName, ext)
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, index, ext)
		if _, exists := used[candidate]; !exists {
			used[candidate] = struct{}{}
			return candidate
		}
	}
}

func appendInputSection(path string, staged []jobs.StagedAttachment) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := io.WriteString(file, "\n\n## Attachments\n\nThe following image attachments are available for visual analysis:\n"); err != nil {
		return err
	}
	for index, attachment := range staged {
		entry := fmt.Sprintf("\n%d. `%s`\n   - MIME: %s\n   - Original filename: %s\n   - ID: %s\n", index+1, attachment.RelativePath, attachment.MIMEType, attachment.OriginalFileName, attachment.ID)
		if _, err := io.WriteString(file, entry); err != nil {
			return err
		}
	}
	return nil
}

func writeJSONExclusive(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(data)
	return err
}

func AppendEvent(jobDir, action, attachmentID, fileName, message string) error {
	file, err := os.OpenFile(filepath.Join(jobDir, "eventlog.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(event{
		Action:       action,
		Timestamp:    time.Now().UTC(),
		AttachmentID: attachmentID,
		FileName:     fileName,
		Message:      message,
	})
}
