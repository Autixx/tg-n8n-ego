package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
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

	cmd := exec.CommandContext(ctx, r.codexBin, "app-server")
	cmd.Dir = jobDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return AppServerResult{}, fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return AppServerResult{}, fmt.Errorf("open app-server stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return AppServerResult{}, fmt.Errorf("open app-server stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return AppServerResult{}, fmt.Errorf("start codex app-server: %w", err)
	}
	go io.Copy(io.Discard, stderr)
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	client := &appClient{
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
		nextID:  1,
	}
	client.scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
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

type appClient struct {
	stdin   io.Writer
	scanner *bufio.Scanner
	nextID  int
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
		"sandbox":        "workspaceWrite",
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
	for c.scanner.Scan() {
		var message appRPCMessage
		if err := json.Unmarshal(c.scanner.Bytes(), &message); err != nil {
			continue
		}
		if message.Method == "turn/completed" {
			return nil
		}
	}
	if err := c.scanner.Err(); err != nil {
		return fmt.Errorf("read app-server stream: %w", err)
	}
	return errors.New("app-server stream ended before turn/completed")
}

func (c *appClient) call(method string, params any) (json.RawMessage, error) {
	id := c.nextID
	c.nextID++
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for c.scanner.Scan() {
		var message appRPCMessage
		if err := json.Unmarshal(c.scanner.Bytes(), &message); err != nil {
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
	if err := c.scanner.Err(); err != nil {
		return nil, fmt.Errorf("read app-server response: %w", err)
	}
	return nil, fmt.Errorf("app-server closed before response to %s", method)
}

func (c *appClient) notify(method string, params any) error {
	return c.send(map[string]any{"method": method, "params": params})
}

func (c *appClient) send(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
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
