package sessions

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	StatusActive   = "active"
	StatusClosed   = "closed"
	StatusFailed   = "failed"
	StatusRotating = "rotating"
)

type Session struct {
	ID             string    `json:"id"`
	ThreadID       string    `json:"thread_id"`
	Purpose        string    `json:"purpose"`
	CodexSessionID string    `json:"codex_session_id"`
	Status         string    `json:"status"`
	TurnCount      int       `json:"turn_count"`
	MaxTurns       int       `json:"max_turns"`
	Summary        string    `json:"summary"`
	BootstrapHash  string    `json:"bootstrap_hash"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	LastUsedAt     time.Time `json:"last_used_at"`
	LastError      string    `json:"last_error,omitempty"`
}

type Message struct {
	ID              string          `json:"id"`
	SessionID       string          `json:"session_id"`
	ClientRequestID string          `json:"client_request_id,omitempty"`
	Direction       string          `json:"direction"`
	Mode            string          `json:"mode,omitempty"`
	Source          string          `json:"source,omitempty"`
	InputText       string          `json:"input_text,omitempty"`
	ResultJSON      json.RawMessage `json:"result_json,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

type AttachmentMetadata struct {
	ID           string    `json:"id"`
	MessageID    string    `json:"message_id"`
	AttachmentID string    `json:"attachment_id"`
	Kind         string    `json:"kind"`
	FileName     string    `json:"file_name"`
	MIMEType     string    `json:"mime_type"`
	SizeBytes    int64     `json:"size_bytes"`
	LocalPath    string    `json:"local_path,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

type document struct {
	Sessions    []Session            `json:"sessions"`
	Messages    []Message            `json:"messages"`
	Attachments []AttachmentMetadata `json:"attachments,omitempty"`
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) GetActive(threadID, purpose, bootstrapHash string, maxTurns int, maxAge time.Duration, now time.Time) (Session, bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readLocked()
	if err != nil {
		return Session{}, false, "", err
	}
	for index := range doc.Sessions {
		session := &doc.Sessions[index]
		if session.ThreadID != threadID || session.Purpose != purpose || session.Status != StatusActive {
			continue
		}
		if session.TurnCount >= session.MaxTurns || now.Sub(session.CreatedAt) >= maxAge || session.BootstrapHash != bootstrapHash {
			session.Status = StatusClosed
			session.Summary = summarizeMessages(doc.Messages, session.ID)
			session.UpdatedAt = now
			session.LastUsedAt = now
			if err := s.writeLocked(doc); err != nil {
				return Session{}, false, "", err
			}
			return Session{}, false, session.Summary, nil
		}
		return *session, true, "", nil
	}
	return Session{}, false, "", nil
}

func (s *Store) Create(threadID, purpose, bootstrapHash, summary string, maxTurns int, now time.Time) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readLocked()
	if err != nil {
		return Session{}, err
	}
	id, err := newID("sess")
	if err != nil {
		return Session{}, err
	}
	session := Session{
		ID:             id,
		ThreadID:       threadID,
		Purpose:        purpose,
		CodexSessionID: "fallback-" + id,
		Status:         StatusActive,
		MaxTurns:       maxTurns,
		Summary:        summary,
		BootstrapHash:  bootstrapHash,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastUsedAt:     now,
	}
	doc.Sessions = append(doc.Sessions, session)
	if err := s.writeLocked(doc); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Store) RecordTurn(sessionID, clientRequestID, mode, source, inputText string, result json.RawMessage, attachments []AttachmentMetadata, now time.Time) (Session, Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readLocked()
	if err != nil {
		return Session{}, Message{}, err
	}
	sessionIndex := -1
	for index := range doc.Sessions {
		if doc.Sessions[index].ID == sessionID {
			sessionIndex = index
			break
		}
	}
	if sessionIndex < 0 {
		return Session{}, Message{}, errors.New("session not found")
	}
	messageID, err := newID("msg")
	if err != nil {
		return Session{}, Message{}, err
	}
	message := Message{
		ID:              messageID,
		SessionID:       sessionID,
		ClientRequestID: clientRequestID,
		Direction:       "user",
		Mode:            mode,
		Source:          source,
		InputText:       inputText,
		ResultJSON:      append(json.RawMessage(nil), result...),
		CreatedAt:       now,
	}
	doc.Messages = append(doc.Messages, message)
	for _, attachment := range attachments {
		attachment.ID = mustID("att")
		attachment.MessageID = messageID
		attachment.CreatedAt = now
		doc.Attachments = append(doc.Attachments, attachment)
	}
	session := &doc.Sessions[sessionIndex]
	session.TurnCount++
	session.UpdatedAt = now
	session.LastUsedAt = now
	session.Summary = summarizeMessages(doc.Messages, sessionID)
	if err := s.writeLocked(doc); err != nil {
		return Session{}, Message{}, err
	}
	return *session, message, nil
}

func (s *Store) SetCodexSessionID(sessionID, codexSessionID string, now time.Time) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readLocked()
	if err != nil {
		return Session{}, err
	}
	for index := range doc.Sessions {
		if doc.Sessions[index].ID == sessionID {
			doc.Sessions[index].CodexSessionID = codexSessionID
			doc.Sessions[index].UpdatedAt = now
			doc.Sessions[index].LastUsedAt = now
			if err := s.writeLocked(doc); err != nil {
				return Session{}, err
			}
			return doc.Sessions[index], nil
		}
	}
	return Session{}, errors.New("session not found")
}

func (s *Store) MarkFailed(sessionID, message string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readLocked()
	if err != nil {
		return err
	}
	for index := range doc.Sessions {
		if doc.Sessions[index].ID == sessionID {
			doc.Sessions[index].Status = StatusFailed
			doc.Sessions[index].LastError = message
			doc.Sessions[index].UpdatedAt = now
			doc.Sessions[index].LastUsedAt = now
			return s.writeLocked(doc)
		}
	}
	return nil
}

func summarizeMessages(messages []Message, sessionID string) string {
	const maxParts = 5
	parts := make([]string, 0, maxParts)
	for index := len(messages) - 1; index >= 0 && len(parts) < maxParts; index-- {
		message := messages[index]
		if message.SessionID != sessionID || len(message.ResultJSON) == 0 {
			continue
		}
		var result struct {
			SourceSummary string `json:"source_summary"`
		}
		if err := json.Unmarshal(message.ResultJSON, &result); err == nil && result.SourceSummary != "" {
			parts = append(parts, result.SourceSummary)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	data, _ := json.Marshal(parts)
	return string(data)
}

func (s *Store) readLocked() (document, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return document{}, nil
		}
		return document{}, fmt.Errorf("read session store: %w", err)
	}
	if len(data) == 0 {
		return document{}, nil
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return document{}, fmt.Errorf("parse session store: %w", err)
	}
	return doc, nil
}

func (s *Store) writeLocked(doc document) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("create session store directory: %w", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".codex-sessions-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary session store: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary session store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary session store: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace session store: %w", err)
	}
	return nil
}

func mustID(prefix string) string {
	id, err := newID(prefix)
	if err != nil {
		return prefix + "-unknown"
	}
	return id
}

func newID(prefix string) (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(buf[:])), nil
}
