package cleanup

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type registryDocument struct {
	XMLName xml.Name       `xml:"attachmentRegistry"`
	Files   []registryFile `xml:"file"`
}

type registryFile struct {
	Path      string    `xml:"path,attr"`
	CreatedAt time.Time `xml:"createdAt,attr"`
}

type Registry struct {
	workdir   string
	path      string
	retention time.Duration
	interval  time.Duration
	logger    *log.Logger
	mu        sync.Mutex
}

func NewRegistry(workdir, path string, retention, interval time.Duration, logger *log.Logger) *Registry {
	return &Registry{
		workdir:   filepath.Clean(workdir),
		path:      filepath.Clean(path),
		retention: retention,
		interval:  interval,
		logger:    logger,
	}
}

func (r *Registry) Initialize() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(r.path), 0o750); err != nil {
		return fmt.Errorf("create attachment registry directory: %w", err)
	}
	if _, err := os.Stat(r.path); err == nil {
		_, err := r.readLocked()
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat attachment registry: %w", err)
	}
	return r.writeLocked(registryDocument{})
}

func (r *Registry) Register(paths []string, createdAt time.Time) error {
	if len(paths) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	document, err := r.readLocked()
	if err != nil {
		return err
	}
	for _, path := range paths {
		relative, err := r.relativePath(path)
		if err != nil {
			return err
		}
		document.Files = append(document.Files, registryFile{Path: filepath.ToSlash(relative), CreatedAt: createdAt.UTC()})
	}
	return r.writeLocked(document)
}

func (r *Registry) Cleanup(now time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	document, err := r.readLocked()
	if err != nil {
		return 0, err
	}
	retained := make([]registryFile, 0, len(document.Files))
	removed := 0
	for _, entry := range document.Files {
		if now.UTC().Sub(entry.CreatedAt.UTC()) < r.retention {
			retained = append(retained, entry)
			continue
		}
		absolutePath, err := r.absolutePath(entry.Path)
		if err != nil {
			r.logger.Printf("attachment cleanup skipped unsafe registry path=%q error=%v", entry.Path, err)
			retained = append(retained, entry)
			continue
		}
		if err := os.Remove(absolutePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			r.logger.Printf("attachment cleanup failed path=%q error=%v", entry.Path, err)
			retained = append(retained, entry)
			continue
		}
		_ = os.Remove(filepath.Dir(absolutePath))
		removed++
		r.logger.Printf("attachment removed path=%q age=%s", entry.Path, now.UTC().Sub(entry.CreatedAt.UTC()).Round(time.Second))
	}
	document.Files = retained
	if err := r.writeLocked(document); err != nil {
		return removed, err
	}
	return removed, nil
}

func (r *Registry) Start(ctx context.Context) {
	go func() {
		r.runCleanup()
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.runCleanup()
			}
		}
	}()
}

func (r *Registry) runCleanup() {
	removed, err := r.Cleanup(time.Now())
	if err != nil {
		r.logger.Printf("attachment cleanup error: %v", err)
		return
	}
	if removed > 0 {
		r.logger.Printf("attachment cleanup complete removed=%d", removed)
	}
}

func (r *Registry) readLocked() (registryDocument, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return registryDocument{}, fmt.Errorf("read attachment registry: %w", err)
	}
	var document registryDocument
	if err := xml.Unmarshal(data, &document); err != nil {
		return registryDocument{}, fmt.Errorf("parse attachment registry: %w", err)
	}
	return document, nil
}

func (r *Registry) writeLocked(document registryDocument) error {
	data, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode attachment registry: %w", err)
	}
	data = append([]byte(xml.Header), data...)
	data = append(data, '\n')
	temporary := r.path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary attachment registry: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return fmt.Errorf("write temporary attachment registry: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return fmt.Errorf("sync temporary attachment registry: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("close temporary attachment registry: %w", err)
	}
	if err := os.Rename(temporary, r.path); err != nil {
		// Windows cannot replace an existing destination; production Debian uses atomic rename.
		if removeErr := os.Remove(r.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			_ = os.Remove(temporary)
			return fmt.Errorf("replace attachment registry: %w", err)
		}
		if retryErr := os.Rename(temporary, r.path); retryErr != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("replace attachment registry: %w", retryErr)
		}
	}
	return nil
}

func (r *Registry) relativePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve attachment path: %w", err)
	}
	workdir, err := filepath.Abs(r.workdir)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}
	relative, err := filepath.Rel(workdir, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("attachment path is outside workdir")
	}
	if err := validateAttachmentRelativePath(relative); err != nil {
		return "", err
	}
	return relative, nil
}

func (r *Registry) absolutePath(relative string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("registry path is outside workdir")
	}
	if err := validateAttachmentRelativePath(cleaned); err != nil {
		return "", err
	}
	current := r.workdir
	parts := strings.Split(filepath.ToSlash(cleaned), "/")
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			return "", fmt.Errorf("inspect attachment path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("registry path contains a symlink directory")
		}
	}
	return filepath.Join(r.workdir, cleaned), nil
}

func validateAttachmentRelativePath(relative string) error {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(relative)), "/")
	if len(parts) != 4 || parts[0] != "jobs" || parts[1] == "" || parts[1] == "." || parts[1] == ".." || parts[2] != "attachments" || parts[3] == "" || parts[3] == "." || parts[3] == ".." {
		return errors.New("registry path is not an isolated job attachment")
	}
	return nil
}
