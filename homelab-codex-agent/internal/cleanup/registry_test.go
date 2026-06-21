package cleanup

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupRemovesExpiredFileAndRegistryEntry(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	registry := NewRegistry(workdir, filepath.Join(workdir, "attachment-registry.xml"), 24*time.Hour, time.Hour, discardLogger())
	if err := registry.Initialize(); err != nil {
		t.Fatal(err)
	}
	oldPath := createAttachment(t, workdir, "old.png")
	newPath := createAttachment(t, workdir, "new.png")
	now := time.Now().UTC()
	if err := registry.Register([]string{oldPath}, now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register([]string{newPath}, now.Add(-23*time.Hour)); err != nil {
		t.Fatal(err)
	}

	removed, err := registry.Cleanup(now)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d", removed)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old attachment still exists: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new attachment missing: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "attachment-registry.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "old.png") || !strings.Contains(string(data), "new.png") {
		t.Fatalf("registry content = %s", data)
	}
}

func TestRegisterRejectsPathOutsideWorkdir(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	registry := NewRegistry(workdir, filepath.Join(workdir, "registry.xml"), 24*time.Hour, time.Hour, discardLogger())
	if err := registry.Initialize(); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := registry.Register([]string{outside}, time.Now()); err == nil {
		t.Fatal("Register() unexpectedly accepted outside path")
	}
}

func TestRegisterRejectsNonAttachmentPath(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	registry := NewRegistry(workdir, filepath.Join(workdir, "registry.xml"), 24*time.Hour, time.Hour, discardLogger())
	if err := registry.Initialize(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workdir, "jobs", "job-1", "result.json")
	if err := registry.Register([]string{path}, time.Now()); err == nil {
		t.Fatal("Register() unexpectedly accepted a non-attachment path")
	}
}

func TestSchedulerRunsCleanup(t *testing.T) {
	workdir := t.TempDir()
	registry := NewRegistry(workdir, filepath.Join(workdir, "registry.xml"), time.Hour, 10*time.Millisecond, discardLogger())
	if err := registry.Initialize(); err != nil {
		t.Fatal(err)
	}
	path := createAttachment(t, workdir, "scheduled.png")
	if err := registry.Register([]string{path}, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry.Start(ctx)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scheduler did not remove expired attachment")
}

func createAttachment(t *testing.T, workdir, name string) string {
	t.Helper()
	dir := filepath.Join(workdir, "jobs", "job-1", "attachments")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func discardLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}
