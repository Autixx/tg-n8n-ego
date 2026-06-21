package codex

import (
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
		"exec", "--sandbox", "workspace-write", "--skip-git-repo-check",
		"--image", "/job/attachments/one.png",
		"--image", "/job/attachments/two.jpg",
		"prompt",
	}
	got := BuildExecArgs("prompt", []string{"/job/attachments/one.png", "/job/attachments/two.jpg"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildExecArgs() = %#v, want %#v", got, want)
	}
}

func TestBuildExecArgsTextOnlyIsUnchanged(t *testing.T) {
	t.Parallel()
	want := []string{"exec", "--sandbox", "workspace-write", "--skip-git-repo-check", "prompt"}
	if got := BuildExecArgs("prompt", nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildExecArgs() = %#v, want %#v", got, want)
	}
}
