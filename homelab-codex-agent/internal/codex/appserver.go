package codex

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type AppServerResult struct {
	ThreadID   string
	UsedResume bool
}

type appRPCMessage struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *appRPCError    `json:"error,omitempty"`
}

type appRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (r *Runner) RunAppServer(jobID, jobDir, threadID, bootstrap, message string, imagePaths []string) (AppServerResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	transport, err := r.openAppServerTransport(ctx, jobDir)
	if err != nil {
		return AppServerResult{}, err
	}
	defer transport.Close()

	client := &appClient{transport: transport, nextID: 1}
	if err := client.initialize(); err != nil {
		return AppServerResult{}, err
	}

	usedResume := threadID != ""
	if threadID == "" {
		threadID, err = client.threadStart(jobDir)
	} else {
		threadID, err = client.threadResume(threadID, jobDir)
	}
	if err != nil {
		return AppServerResult{}, err
	}

	inputText := message
	if !usedResume && strings.TrimSpace(bootstrap) != "" {
		inputText = bootstrap + "\n\nCurrent request:\n" + message
	}
	if err := client.turnStartAndWait(threadID, inputText, imagePaths); err != nil {
		return AppServerResult{}, err
	}
	return AppServerResult{ThreadID: threadID, UsedResume: usedResume}, nil
}

func (r *Runner) openAppServerTransport(ctx context.Context, jobDir string) (appTransport, error) {
	if r.appServerSocket != "" {
		return dialUnixWebSocket(ctx, r.appServerSocket)
	}
	args := appServerArgs("")
	cmd := exec.CommandContext(ctx, r.codexBin, args...)
	cmd.Dir = jobDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}
	go io.Copy(io.Discard, stderr)
	return newJSONLTransport(stdin, stdout, cmd), nil
}

func appServerArgs(socketPath string) []string {
	if socketPath != "" {
		return []string{"app-server", "proxy", "--sock", socketPath}
	}
	return []string{"app-server"}
}

type appClient struct {
	transport appTransport
	nextID    int
}

func (c *appClient) initialize() error {
	if _, err := c.call("initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "homelab_codex_agent",
			"title":   "Homelab Codex Agent",
			"version": "0.1.0",
		},
		"capabilities": map[string]bool{"experimentalApi": true},
	}); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

func (c *appClient) threadStart(jobDir string) (string, error) {
	result, err := c.call("thread/start", map[string]any{
		"cwd":            jobDir,
		"approvalPolicy": "never",
		"sandbox":        "workspace-write",
		"serviceName":    "homelab-codex-agent",
	})
	if err != nil {
		return "", err
	}
	return parseThreadID(result)
}

func (c *appClient) threadResume(threadID, jobDir string) (string, error) {
	result, err := c.call("thread/resume", map[string]any{
		"threadId": threadID,
		"cwd":      jobDir,
	})
	if err != nil {
		return "", err
	}
	return parseThreadID(result)
}

func (c *appClient) turnStartAndWait(threadID, text string, imagePaths []string) error {
	input := []map[string]any{{"type": "text", "text": text}}
	for _, imagePath := range imagePaths {
		input = append(input, map[string]any{
			"type": "localImage",
			"path": filepath.Clean(imagePath),
		})
	}
	if _, err := c.call("turn/start", map[string]any{
		"threadId": threadID,
		"input":    input,
	}); err != nil {
		return err
	}
	for {
		data, err := c.transport.ReadMessage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("app-server stream ended before turn/completed")
			}
			return fmt.Errorf("read app-server stream: %w", err)
		}
		var message appRPCMessage
		if err := json.Unmarshal(data, &message); err != nil {
			continue
		}
		if message.Method == "turn/completed" {
			return nil
		}
	}
}

func (c *appClient) call(method string, params any) (json.RawMessage, error) {
	id := c.nextID
	c.nextID++
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		data, err := c.transport.ReadMessage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("app-server closed before response to %s", method)
			}
			return nil, fmt.Errorf("read app-server response: %w", err)
		}
		var message appRPCMessage
		if err := json.Unmarshal(data, &message); err != nil {
			continue
		}
		if message.ID != id {
			continue
		}
		if message.Error != nil {
			return nil, fmt.Errorf("app-server %s failed: %s", method, message.Error.Message)
		}
		return message.Result, nil
	}
}

func (c *appClient) notify(method string, params any) error {
	return c.send(map[string]any{"method": method, "params": params})
}

func (c *appClient) send(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return c.transport.WriteMessage(data)
}

type appTransport interface {
	ReadMessage() ([]byte, error)
	WriteMessage([]byte) error
	Close() error
}

type jsonlTransport struct {
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	cmd     *exec.Cmd
}

func newJSONLTransport(stdin io.WriteCloser, stdout io.Reader, cmd *exec.Cmd) *jsonlTransport {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &jsonlTransport{stdin: stdin, scanner: scanner, cmd: cmd}
}

func (t *jsonlTransport) ReadMessage() ([]byte, error) {
	if t.scanner.Scan() {
		return append([]byte(nil), t.scanner.Bytes()...), nil
	}
	if err := t.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (t *jsonlTransport) WriteMessage(data []byte) error {
	data = append(append([]byte(nil), data...), '\n')
	_, err := t.stdin.Write(data)
	return err
}

func (t *jsonlTransport) Close() error {
	_ = t.stdin.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return t.cmd.Wait()
}

type wsUnixTransport struct {
	conn net.Conn
	r    *bufio.Reader
}

func dialUnixWebSocket(ctx context.Context, socketPath string) (*wsUnixTransport, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect app-server socket: %w", err)
	}
	transport := &wsUnixTransport{conn: conn, r: bufio.NewReader(conn)}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	if err := transport.handshake(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return transport, nil
}

func (t *wsUnixTransport) handshake() error {
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return fmt.Errorf("generate websocket key: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	request := strings.Join([]string{
		"GET / HTTP/1.1",
		"Host: localhost",
		"Upgrade: websocket",
		"Connection: Upgrade",
		"Sec-WebSocket-Version: 13",
		"Sec-WebSocket-Key: " + key,
		"",
		"",
	}, "\r\n")
	if _, err := io.WriteString(t.conn, request); err != nil {
		return fmt.Errorf("write websocket handshake: %w", err)
	}
	response, err := http.ReadResponse(t.r, nil)
	if err != nil {
		return fmt.Errorf("read websocket handshake: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("websocket handshake failed: %s", response.Status)
	}
	return nil
}

func (t *wsUnixTransport) ReadMessage() ([]byte, error) {
	for {
		payload, opcode, err := t.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x1, 0x2:
			return payload, nil
		case 0x8:
			return nil, io.EOF
		case 0x9:
			_ = t.writeFrame(0xA, payload)
		}
	}
}

func (t *wsUnixTransport) WriteMessage(data []byte) error {
	return t.writeFrame(0x1, data)
}

func (t *wsUnixTransport) Close() error {
	_ = t.writeFrame(0x8, nil)
	return t.conn.Close()
}

func (t *wsUnixTransport) readFrame() ([]byte, byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(t.r, header); err != nil {
		return nil, 0, err
	}
	opcode := header[0] & 0x0F
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7F)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(t.r, extended[:]); err != nil {
			return nil, 0, err
		}
		length = uint64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(t.r, extended[:]); err != nil {
			return nil, 0, err
		}
		length = binary.BigEndian.Uint64(extended[:])
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(t.r, mask[:]); err != nil {
			return nil, 0, err
		}
	}
	if length > 16*1024*1024 {
		return nil, 0, fmt.Errorf("websocket frame too large: %d bytes", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(t.r, payload); err != nil {
		return nil, 0, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return payload, opcode, nil
}

func (t *wsUnixTransport) writeFrame(opcode byte, payload []byte) error {
	var header []byte
	header = append(header, 0x80|opcode)
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, 0x80|byte(length))
	case length <= 0xFFFF:
		header = append(header, 0x80|126, byte(length>>8), byte(length))
	default:
		header = append(header, 0x80|127)
		var extended [8]byte
		binary.BigEndian.PutUint64(extended[:], uint64(length))
		header = append(header, extended[:]...)
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return fmt.Errorf("generate websocket mask: %w", err)
	}
	header = append(header, mask[:]...)
	masked := append([]byte(nil), payload...)
	for i := range masked {
		masked[i] ^= mask[i%4]
	}
	if _, err := t.conn.Write(header); err != nil {
		return err
	}
	_, err := t.conn.Write(masked)
	return err
}

func parseThreadID(data json.RawMessage) (string, error) {
	var payload struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	if payload.Thread.ID == "" {
		return "", errors.New("app-server response did not include thread.id")
	}
	return payload.Thread.ID, nil
}
