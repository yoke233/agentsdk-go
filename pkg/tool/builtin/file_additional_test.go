package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileToolResolvePathPreventsTraversal(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewFileToolWithRoot(dir)
	if _, err := tool.resolvePath(map[string]any{"path": "../secret"}); err == nil || !strings.Contains(err.Error(), "path not in sandbox") {
		t.Fatalf("expected sandbox violation, got %v", err)
	}
}

func TestFileToolExecuteHandlesCancelledContext(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	target := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(target, []byte("ok"), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewFileToolWithRoot(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, map[string]any{"operation": "read", "path": "data.txt"}); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected cancellation error, got %v", err)
	}
}

func TestFileToolReadFileHonorsSizeLimit(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	target := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(target, []byte("1234"), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewFileToolWithRoot(dir)
	tool.maxBytes = 1
	if _, err := tool.readFile(target); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size limit error, got %v", err)
	}
}

func TestParseOperationValidations(t *testing.T) {
	if _, err := parseOperation(nil); err == nil {
		t.Fatalf("expected nil params error")
	}
	if _, err := parseOperation(map[string]any{}); err == nil {
		t.Fatalf("expected missing operation error")
	}
	if _, err := parseOperation(map[string]any{"operation": "noop"}); err == nil {
		t.Fatalf("expected unsupported operation error")
	}
}

func TestFileToolWriteFilePermissionError(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defer os.Chmod(locked, 0o755)
	tool := NewFileToolWithRoot(dir)
	params := map[string]any{"content": "data"}
	if _, err := tool.writeFile(filepath.Join(locked, "file.txt"), params); err == nil {
		t.Fatalf("expected write failure inside read-only dir")
	}
}

func TestFileToolDeleteFileStatError(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o000); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defer os.Chmod(locked, 0o755)
	tool := NewFileToolWithRoot(dir)
	if _, err := tool.deleteFile(filepath.Join(locked, "ghost.txt")); err == nil || !strings.Contains(err.Error(), "stat file") {
		t.Fatalf("expected stat error, got %v", err)
	}
}

func TestFileToolExecuteNilContext(t *testing.T) {
	tool := NewFileTool()
	if _, err := tool.Execute(nil, map[string]any{"operation": "read", "path": "x"}); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("expected nil context error, got %v", err)
	}
}

func TestFileToolExecuteUninitialised(t *testing.T) {
	var tool FileTool
	if _, err := tool.Execute(context.Background(), map[string]any{"operation": "read", "path": "x"}); err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("expected not initialised error, got %v", err)
	}
}
