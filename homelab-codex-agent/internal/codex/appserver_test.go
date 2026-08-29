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

func TestThreadStartUsesCodexSandboxVariant(t *testing.T) {
	t.Parallel()
	transport := &fakeAppTransport{
		read: [][]byte{[]byte(`{"id":1,"result":{"thread":{"id":"thread-1"}}}`)},
	}
	client := &appClient{transport: transport, nextID: 1}

	threadID, err := client.threadStart("/tmp/job")
	if err != nil {
		t.Fatal(err)
	}
	if threadID != "thread-1" {
		t.Fatalf("threadID = %q, want thread-1", threadID)
	}
	var payload struct {
		Method string `json:"method"`
		Params struct {
			Sandbox string `json:"sandbox"`
		} `json:"params"`
	}
	if err := json.Unmarshal(transport.written[0], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Method != "thread/start" || payload.Params.Sandbox != "workspace-write" {
		t.Fatalf("unexpected thread/start payload: %#v", payload)
	}
}

func TestAppClientAnswersServerRequestsWhileWaiting(t *testing.T) {
	t.Parallel()
	transport := &fakeAppTransport{
		read: [][]byte{
			[]byte(`{"id":42,"method":"item/permissions/requestApproval","params":{"reason":"test"}}`),
			[]byte(`{"id":1,"result":{"ok":true}}`),
		},
	}
	client := &appClient{transport: transport, nextID: 1}

	if _, err := client.call("thread/start", map[string]any{"cwd": "/tmp/job"}); err != nil {
		t.Fatal(err)
	}
	if len(transport.written) != 2 {
		t.Fatalf("written messages = %d, want 2", len(transport.written))
	}
	var response struct {
		ID     int `json:"id"`
		Result struct {
			Scope       string         `json:"scope"`
			Permissions map[string]any `json:"permissions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(transport.written[1], &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != 42 || response.Result.Scope != "turn" || response.Result.Permissions == nil {
		t.Fatalf("unexpected server request response: %#v", response)
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
