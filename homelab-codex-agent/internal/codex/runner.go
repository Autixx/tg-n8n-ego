package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrImageAttachmentsUnsupported = errors.New("image_attachments_not_supported_by_current_codex_cli")

type Runner struct {
	codexBin       string
	promptPath     string
	timeout        time.Duration
	multimodalMode string
	logger         *log.Logger

	capabilityOnce sync.Once
	imageSupported bool
	capabilityErr  error
}

func NewRunner(codexBin, promptPath string, timeout time.Duration, multimodalMode string, logger *log.Logger) *Runner {
	return &Runner{
		codexBin:       codexBin,
		promptPath:     promptPath,
		timeout:        timeout,
		multimodalMode: multimodalMode,
		logger:         logger,
	}
}

func (r *Runner) Run(jobID, jobDir string, imagePaths []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	if len(imagePaths) > 0 {
		supported, err := r.supportsImages()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrImageAttachmentsUnsupported, err)
		}
		if !supported {
			return fmt.Errorf("%w: installed Codex CLI does not expose codex exec --image", ErrImageAttachmentsUnsupported)
		}
	}

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

	prompt := fmt.Sprintf("Используй инструкцию из %s. Обработай input.md согласно mode.txt. Создай result.json в текущей директории. Сохрани существующие записи eventlog.jsonl и дополни файл кратким журналом своих действий.", r.promptPath)
	args := BuildExecArgs(prompt, imagePaths)
	cmd := exec.CommandContext(ctx, r.codexBin, args...)
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

func (r *Runner) supportsImages() (bool, error) {
	if r.multimodalMode == "disabled" {
		return false, errors.New("multimodal mode is disabled")
	}
	r.capabilityOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, r.codexBin, "exec", "--help").CombinedOutput()
		if err != nil {
			r.capabilityErr = fmt.Errorf("inspect codex exec capabilities: %w", err)
			return
		}
		r.imageSupported = HelpSupportsImages(string(output))
	})
	return r.imageSupported, r.capabilityErr
}

func HelpSupportsImages(help string) bool {
	return strings.Contains(help, "--image")
}

func BuildExecArgs(prompt string, imagePaths []string) []string {
	args := []string{"exec", "--sandbox", "workspace-write", "--skip-git-repo-check"}
	for _, imagePath := range imagePaths {
		args = append(args, "--image", imagePath)
	}
	return append(args, prompt)
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
