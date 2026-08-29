package codex

import (
	"encoding/json"
	"io"
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

func TestAppClientCallUsesMessageTransport(t *testing.T) {
	t.Parallel()
	transport := &fakeAppTransport{
		read: [][]byte{[]byte(`{"id":1,"result":{"ok":true}}`)},
	}
	client := &appClient{transport: transport, nextID: 1}

	result, err := client.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"ok":true}` {
		t.Fatalf("result = %s", result)
	}
	if len(transport.written) != 1 {
		t.Fatalf("written messages = %d, want 1", len(transport.written))
	}
	var message appRPCMessage
	if err := json.Unmarshal(transport.written[0], &message); err != nil {
		t.Fatal(err)
	}
	if message.ID != 1 || message.Method != "initialize" {
		t.Fatalf("unexpected message: %#v", message)
	}
}

type fakeAppTransport struct {
	read    [][]byte
	written [][]byte
}

func (t *fakeAppTransport) ReadMessage() ([]byte, error) {
	if len(t.read) == 0 {
		return nil, io.EOF
	}
	message := t.read[0]
	t.read = t.read[1:]
	return message, nil
}

func (t *fakeAppTransport) WriteMessage(data []byte) error {
	t.written = append(t.written, append([]byte(nil), data...))
	return nil
}

func (t *fakeAppTransport) Close() error {
	return nil
}
