package codex

import (
	"bytes"
	"io"
	"reflect"
	"testing"
)

func TestHelpSupportsImages(t *testing.T) {
	t.Parallel()
	if !HelpSupportsImages("  -i, --image <FILE>... Optional images") {
		t.Fatal("expected --image capability")
	}
	if HelpSupportsImages("Usage: codex exec [OPTIONS]") {
		t.Fatal("unexpected image capability")
	}
}

func TestBuildExecArgsAddsImages(t *testing.T) {
	t.Parallel()
	want := []string{
		"exec", "--sandbox", "workspace-write", "--skip-git-repo-check", "-",
		"--image", "/job/attachments/one.png",
		"--image", "/job/attachments/two.jpg",
	}
	got := BuildExecArgs([]string{"/job/attachments/one.png", "/job/attachments/two.jpg"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildExecArgs() = %#v, want %#v", got, want)
	}
}

func TestBuildExecArgsTextOnlyIsUnchanged(t *testing.T) {
	t.Parallel()
	want := []string{"exec", "--sandbox", "workspace-write", "--skip-git-repo-check", "-"}
	if got := BuildExecArgs(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildExecArgs() = %#v, want %#v", got, want)
	}
}

func TestWritePromptAndClose(t *testing.T) {
	t.Parallel()
	stdin := &trackingWriteCloser{}
	if err := WritePromptAndClose(stdin, "ProjectEGO prompt"); err != nil {
		t.Fatal(err)
	}
	if got := stdin.String(); got != "ProjectEGO prompt" {
		t.Fatalf("stdin = %q", got)
	}
	if !stdin.closed {
		t.Fatal("stdin was not closed")
	}
}

type trackingWriteCloser struct {
	bytes.Buffer
	closed bool
}

func (w *trackingWriteCloser) Close() error {
	w.closed = true
	return nil
}

var _ io.WriteCloser = (*trackingWriteCloser)(nil)
