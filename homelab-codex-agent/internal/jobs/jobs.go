package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var jobIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type Store struct {
	workdir string
	logger  *log.Logger
}

type Request struct {
	Mode        string              `json:"mode,omitempty"`
	Text        string              `json:"text"`
	Source      string              `json:"source,omitempty"`
	ChatID      string              `json:"chat_id,omitempty"`
	FileName    string              `json:"fileName,omitempty"`
	Attachments []AttachmentRequest `json:"attachments,omitempty"`
}

type AttachmentRequest struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	FileName    string `json:"fileName"`
	MIMEType    string `json:"mimeType"`
	SizeBytes   int64  `json:"sizeBytes"`
	DownloadURL string `json:"downloadUrl"`
}

type StagedAttachment struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	OriginalFileName string `json:"originalFileName"`
	FileName         string `json:"fileName"`
	MIMEType         string `json:"mimeType"`
	SizeBytes        int64  `json:"sizeBytes"`
	RelativePath     string `json:"relativePath"`
}

type Metadata struct {
	JobID       string              `json:"job_id"`
	Mode        string              `json:"mode"`
	Source      string              `json:"source,omitempty"`
	ChatID      string              `json:"chat_id,omitempty"`
	FileName    string              `json:"file_name,omitempty"`
	Attachments []AttachmentRequest `json:"attachments,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
}

type Status struct {
	JobID      string    `json:"job_id"`
	Status     string    `json:"status"`
	Mode       string    `json:"mode"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Error      string    `json:"error,omitempty"`
	ResultPath string    `json:"result_path,omitempty"`
}

type Job struct {
	ID  string
	Dir string
}

func NewStore(workdir string, logger *log.Logger) *Store {
	return &Store{workdir: workdir, logger: logger}
}

func IsValidJobID(jobID string) bool {
	return jobID != "." && jobID != ".." && jobIDPattern.MatchString(jobID)
}

func (s *Store) Create(req Request) (Job, error) {
	jobID, err := newJobID()
	if err != nil {
		return Job{}, err
	}
	if !IsValidJobID(jobID) {
		return Job{}, errors.New("generated invalid job_id")
	}

	jobDir := filepath.Join(s.workdir, "jobs", jobID)
	if err := os.MkdirAll(jobDir, 0o750); err != nil {
		return Job{}, fmt.Errorf("create job directory: %w", err)
	}

	if err := os.WriteFile(filepath.Join(jobDir, "input.md"), []byte(req.Text), 0o600); err != nil {
		return Job{}, fmt.Errorf("write input.md: %w", err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "mode.txt"), []byte(req.Mode+"\n"), 0o600); err != nil {
		return Job{}, fmt.Errorf("write mode.txt: %w", err)
	}

	createdAt := time.Now().UTC()
	metadata := Metadata{
		JobID:       jobID,
		Mode:        req.Mode,
		Source:      req.Source,
		ChatID:      req.ChatID,
		FileName:    req.FileName,
		Attachments: req.Attachments,
		CreatedAt:   createdAt,
	}
	if err := writeJSON(filepath.Join(jobDir, "metadata.json"), metadata); err != nil {
		return Job{}, fmt.Errorf("write metadata.json: %w", err)
	}

	status := Status{
		JobID:     jobID,
		Status:    "created",
		Mode:      req.Mode,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if err := s.WriteStatus(Job{ID: jobID, Dir: jobDir}, status); err != nil {
		return Job{}, err
	}
	if err := createEmptyFile(filepath.Join(jobDir, "stdout.log")); err != nil {
		return Job{}, fmt.Errorf("create stdout.log: %w", err)
	}
	if err := createEmptyFile(filepath.Join(jobDir, "stderr.log")); err != nil {
		return Job{}, fmt.Errorf("create stderr.log: %w", err)
	}

	s.logger.Printf("job created job_id=%s mode=%s source=%s", jobID, req.Mode, req.Source)
	return Job{ID: jobID, Dir: jobDir}, nil
}

func (r *Request) UnmarshalJSON(data []byte) error {
	type requestAlias Request
	var wire struct {
		requestAlias
		LegacyFileName string `json:"file_name"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*r = Request(wire.requestAlias)
	if r.FileName == "" {
		r.FileName = wire.LegacyFileName
	}
	return nil
}

func (s *Store) JobDir(jobID string) (string, error) {
	if !IsValidJobID(jobID) {
		return "", errors.New("invalid job_id")
	}
	return filepath.Join(s.workdir, "jobs", jobID), nil
}

func (s *Store) WriteStatus(job Job, status Status) error {
	status.UpdatedAt = time.Now().UTC()
	if err := writeJSON(filepath.Join(job.Dir, "status.json"), status); err != nil {
		return fmt.Errorf("write status.json: %w", err)
	}
	return nil
}

func newJobID() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate random hex: %w", err)
	}
	return fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102-150405"), hex.EncodeToString(buf[:])), nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func createEmptyFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}
