package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromFS_Basic(t *testing.T) {
	root := t.TempDir()
	cmdDir := filepath.Join(root, ".claude", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	filePath := filepath.Join(cmdDir, "ping.md")
	content := "hello $ARGUMENTS"
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	regs, errs := LoadFromFS(LoaderOptions{ProjectRoot: root})
	if len(errs) != 0 {
		t.Fatalf("unexpected error: %v", errs)
	}
	if len(regs) != 1 {
		t.Fatalf("expected 1 registration, got %d", len(regs))
	}
	reg := regs[0]
	if reg.Definition.Name != "ping" {
		t.Fatalf("unexpected name %q", reg.Definition.Name)
	}

	res, err := reg.Handler.Handle(context.Background(), Invocation{Args: []string{"world"}})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if res.Output != "hello world" {
		t.Fatalf("unexpected output %q", res.Output)
	}
}

func TestLoadFromFS_IgnoresUserDir(t *testing.T) {
	projectRoot := t.TempDir()
	userHome := t.TempDir()

	projectDir := filepath.Join(projectRoot, ".claude", "commands")
	userDir := filepath.Join(userHome, ".claude", "commands")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir user: %v", err)
	}

	mustWrite(t, filepath.Join(userDir, "user-only.md"), "user version")
	mustWrite(t, filepath.Join(projectDir, "deploy.md"), "project version")

	regs, errs := LoadFromFS(LoaderOptions{ProjectRoot: projectRoot, UserHome: userHome, EnableUser: true})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(regs) != 1 {
		t.Fatalf("expected only project registrations, got %d", len(regs))
	}

	deploy := findRegistration(t, regs, "deploy")
	res, err := deploy.Handler.Handle(context.Background(), Invocation{})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if res.Output != "project version" {
		t.Fatalf("expected project body, got %q", res.Output)
	}
	for _, reg := range regs {
		if reg.Definition.Name == "user-only" {
			t.Fatalf("user-level command should be ignored")
		}
	}
}

func TestLoadFromFS_NoProjectDir(t *testing.T) {
	projectRoot := t.TempDir()
	userHome := t.TempDir()

	userDir := filepath.Join(userHome, ".claude", "commands")
	mustWrite(t, filepath.Join(userDir, "ignored.md"), "user body")

	regs, errs := LoadFromFS(LoaderOptions{ProjectRoot: projectRoot, UserHome: userHome, EnableUser: true})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(regs) != 0 {
		t.Fatalf("expected no registrations, got %d", len(regs))
	}
}

func TestLoadFromFS_YAML(t *testing.T) {
	root := t.TempDir()
	cmdDir := filepath.Join(root, ".claude", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	body := strings.Join([]string{
		"---",
		"description: say hi",
		"allowed-tools: shell,read",
		"argument-hint: '<name>'",
		"model: claude-3",
		"disable-model-invocation: true",
		"---",
		"Hello $1",
	}, "\n")
	path := filepath.Join(cmdDir, "hi.md")
	mustWrite(t, path, body)

	regs, errs := LoadFromFS(LoaderOptions{ProjectRoot: root})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(regs) != 1 {
		t.Fatalf("expected 1 registration, got %d", len(regs))
	}

	reg := regs[0]
	if reg.Definition.Description != "say hi" {
		t.Fatalf("unexpected description %q", reg.Definition.Description)
	}

	res, err := reg.Handler.Handle(context.Background(), Invocation{Args: []string{"there"}})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if res.Output != "Hello there" {
		t.Fatalf("unexpected output %q", res.Output)
	}

	if res.Metadata == nil {
		t.Fatalf("expected metadata")
	}
	if res.Metadata["allowed-tools"] != "shell,read" {
		t.Fatalf("metadata allowed-tools mismatch: %#v", res.Metadata)
	}
	if res.Metadata["argument-hint"] != "<name>" {
		t.Fatalf("metadata argument-hint mismatch: %#v", res.Metadata)
	}
	if res.Metadata["model"] != "claude-3" {
		t.Fatalf("metadata model mismatch: %#v", res.Metadata)
	}
	if res.Metadata["disable-model-invocation"] != true {
		t.Fatalf("metadata disable-model-invocation missing: %#v", res.Metadata)
	}
	if res.Metadata["source"] != path {
		t.Fatalf("metadata source mismatch: %#v", res.Metadata)
	}
}

func TestLoadFromFS_Errors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mustWrite(t, filepath.Join(dir, "bad name.md"), "invalid")
	mustWrite(t, filepath.Join(dir, "broken.md"), "---\nfoo: [\n")
	mustWrite(t, filepath.Join(dir, "ok.md"), "hello")

	regs, errs := LoadFromFS(LoaderOptions{ProjectRoot: root})
	if len(regs) != 1 {
		t.Fatalf("expected 1 registration, got %d", len(regs))
	}
	if len(errs) < 2 {
		t.Fatalf("expected aggregated errors, got %v", errs)
	}
	if !hasError(errs, "invalid command name") {
		t.Fatalf("missing invalid name error: %v", errs)
	}
	if !hasError(errs, "missing closing frontmatter") && !hasError(errs, "yaml") {
		t.Fatalf("missing frontmatter error: %v", errs)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func findRegistration(t *testing.T, regs []CommandRegistration, name string) CommandRegistration {
	t.Helper()
	for _, reg := range regs {
		if reg.Definition.Name == name {
			return reg
		}
	}
	t.Fatalf("registration %s not found", name)
	return CommandRegistration{}
}

func hasError(errs []error, substr string) bool {
	for _, err := range errs {
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), substr) {
			return true
		}
	}
	return false
}
