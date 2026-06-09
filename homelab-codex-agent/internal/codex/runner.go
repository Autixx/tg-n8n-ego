package codex

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Runner struct {
	codexBin   string
	promptPath string
	timeout    time.Duration
	logger     *log.Logger
}

func NewRunner(codexBin, promptPath string, timeout time.Duration, logger *log.Logger) *Runner {
	return &Runner{
		codexBin:   codexBin,
		promptPath: promptPath,
		timeout:    timeout,
		logger:     logger,
	}
}

func (r *Runner) Run(jobID, jobDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	stdoutPath := filepath.Join(jobDir, "stdout.log")
	stderrPath := filepath.Join(jobDir, "stderr.log")

	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open stdout.log: %w", err)
	}
	defer stdout.Close()

	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open stderr.log: %w", err)
	}
	defer stderr.Close()

	prompt := fmt.Sprintf("Используй инструкцию из %s. Обработай input.md согласно mode.txt. Создай result.json и eventlog.jsonl в текущей директории.", r.promptPath)
	cmd := exec.CommandContext(ctx, r.codexBin, "exec", "--sandbox", "workspace-write", "--skip-git-repo-check", prompt)
	cmd.Dir = jobDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	r.logger.Printf("job running job_id=%s timeout=%s", jobID, r.timeout)
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("codex exec timed out after %s", r.timeout)
	}
	if err != nil {
		return fmt.Errorf("codex exec failed: %w", err)
	}
	return nil
}

func TailFile(path string, maxBytes int64) string {
	if maxBytes <= 0 {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return ""
	}
	start := stat.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	return string(data)
}
