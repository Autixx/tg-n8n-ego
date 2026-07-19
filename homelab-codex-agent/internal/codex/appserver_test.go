package codex

import (
	"reflect"
	"testing"
)

func TestAppServerArgsUseProxyForSocket(t *testing.T) {
	t.Parallel()
	want := []string{"app-server", "proxy", "--sock", "/opt/codex-agent/codex-app-server.sock"}
	got := appServerArgs("/opt/codex-agent/codex-app-server.sock")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appServerArgs() = %#v, want %#v", got, want)
	}
}

func TestAppServerArgsUseStdioServerWithoutSocket(t *testing.T) {
	t.Parallel()
	want := []string{"app-server"}
	got := appServerArgs("")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appServerArgs() = %#v, want %#v", got, want)
	}
}
