package toolbuiltin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const (
	defaultBashTimeout = 30 * time.Second
	maxBashTimeout     = 2 * time.Minute
)

var bashSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"command": map[string]interface{}{
			"type":        "string",
			"description": "Command string executed via bash without shell metacharacters.",
		},
		"timeout": map[string]interface{}{
			"type":        "number",
			"description": "Optional timeout in seconds (defaults to 30, caps at 120).",
		},
		"workdir": map[string]interface{}{
			"type":        "string",
			"description": "Optional working directory relative to the sandbox root.",
		},
	},
	Required: []string{"command"},
}

// BashTool executes validated commands using bash within a sandbox.
type BashTool struct {
	sandbox *security.Sandbox
	root    string
	timeout time.Duration
}

// NewBashTool builds a BashTool rooted at the current directory.
func NewBashTool() *BashTool {
	return NewBashToolWithRoot("")
}

// NewBashToolWithRoot builds a BashTool rooted at the provided directory.
func NewBashToolWithRoot(root string) *BashTool {
	resolved := resolveRoot(root)
	return &BashTool{
		sandbox: security.NewSandbox(resolved),
		root:    resolved,
		timeout: defaultBashTimeout,
	}
}

// AllowShellMetachars enables shell pipes and metacharacters (CLI mode).
func (b *BashTool) AllowShellMetachars(allow bool) {
	if b != nil && b.sandbox != nil {
		b.sandbox.AllowShellMetachars(allow)
	}
}

func (b *BashTool) Name() string { return "bash_execute" }

func (b *BashTool) Description() string {
	return "Execute validated bash commands inside the agent workspace."
}

func (b *BashTool) Schema() *tool.JSONSchema { return bashSchema }

func (b *BashTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if b == nil || b.sandbox == nil {
		return nil, errors.New("bash tool is not initialised")
	}
	command, err := extractCommand(params)
	if err != nil {
		return nil, err
	}
	if err := b.sandbox.ValidateCommand(command); err != nil {
		return nil, err
	}
	workdir, err := b.resolveWorkdir(params)
	if err != nil {
		return nil, err
	}
	timeout, err := b.resolveTimeout(params)
	if err != nil {
		return nil, err
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(execCtx, "bash", "-c", command)
	cmd.Env = os.Environ()
	cmd.Dir = workdir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	result := &tool.ToolResult{
		Success: runErr == nil,
		Output:  combineOutput(stdout.String(), stderr.String()),
		Data: map[string]interface{}{
			"workdir":     workdir,
			"duration_ms": duration.Milliseconds(),
			"timeout_ms":  timeout.Milliseconds(),
		},
	}

	if runErr != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return result, fmt.Errorf("command timeout after %s", timeout)
		}
		return result, fmt.Errorf("command failed: %w", runErr)
	}
	return result, nil
}

func (b *BashTool) resolveWorkdir(params map[string]interface{}) (string, error) {
	dir := b.root
	if raw, ok := params["workdir"]; ok && raw != nil {
		value, err := coerceString(raw)
		if err != nil {
			return "", fmt.Errorf("workdir must be string: %w", err)
		}
		value = strings.TrimSpace(value)
		if value != "" {
			dir = value
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(b.root, dir)
	}
	dir = filepath.Clean(dir)
	return b.ensureDirectory(dir)
}

func (b *BashTool) ensureDirectory(path string) (string, error) {
	if err := b.sandbox.ValidatePath(path); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("workdir stat: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir %s is not a directory", path)
	}
	return path, nil
}

func (b *BashTool) resolveTimeout(params map[string]interface{}) (time.Duration, error) {
	timeout := b.timeout
	raw, ok := params["timeout"]
	if !ok || raw == nil {
		return timeout, nil
	}
	dur, err := durationFromParam(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout: %w", err)
	}
	if dur == 0 {
		return timeout, nil
	}
	if dur > maxBashTimeout {
		dur = maxBashTimeout
	}
	return dur, nil
}

func extractCommand(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["command"]
	if !ok {
		return "", errors.New("command is required")
	}
	cmd, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("command must be string: %w", err)
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", errors.New("command cannot be empty")
	}
	return cmd, nil
}

func coerceString(value interface{}) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case fmt.Stringer:
		return v.String(), nil
	case []byte:
		return string(v), nil
	case json.Number:
		return v.String(), nil
	default:
		return "", fmt.Errorf("expected string got %T", value)
	}
}

func durationFromParam(value interface{}) (time.Duration, error) {
	switch v := value.(type) {
	case time.Duration:
		if v < 0 {
			return 0, errors.New("duration cannot be negative")
		}
		return v, nil
	case float64:
		return secondsToDuration(v)
	case float32:
		return secondsToDuration(float64(v))
	case int:
		return secondsToDuration(float64(v))
	case int64:
		return secondsToDuration(float64(v))
	case uint:
		return secondsToDuration(float64(v))
	case uint64:
		return secondsToDuration(float64(v))
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, err
		}
		return secondsToDuration(f)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, nil
		}
		if strings.ContainsAny(trimmed, "hms") {
			return time.ParseDuration(trimmed)
		}
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, err
		}
		return secondsToDuration(f)
	default:
		return 0, fmt.Errorf("unsupported duration type %T", value)
	}
}

func secondsToDuration(seconds float64) (time.Duration, error) {
	if seconds < 0 {
		return 0, errors.New("duration cannot be negative")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func combineOutput(stdout, stderr string) string {
	stdout = strings.TrimRight(stdout, "\r\n")
	stderr = strings.TrimRight(stderr, "\r\n")
	switch {
	case stdout == "" && stderr == "":
		return ""
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}

func resolveRoot(dir string) string {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		if cwd, err := os.Getwd(); err == nil {
			trimmed = cwd
		} else {
			trimmed = "."
		}
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return abs
	}
	return filepath.Clean(trimmed)
}
