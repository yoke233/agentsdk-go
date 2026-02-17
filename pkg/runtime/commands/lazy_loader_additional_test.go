package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadCommandDirNotDirectory(t *testing.T) {
	root := t.TempDir()
	commandsPath := filepath.Join(root, ".claude", "commands")
	if err := os.MkdirAll(filepath.Dir(commandsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(commandsPath, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, errs := loadCommandDir(commandsPath, resolveFileOps(LoaderOptions{}), resolveWalkDirFunc(LoaderOptions{}))
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "not a directory") {
		t.Fatalf("expected not-a-directory error, got %v", errs)
	}
}

func TestReadFrontMatterMetadata_NoFrontmatter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.md")
	if err := os.WriteFile(path, []byte("no frontmatter"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	meta, err := readFrontMatterMetadata(path, resolveFileOps(LoaderOptions{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != (CommandMetadata{}) {
		t.Fatalf("expected empty metadata, got %#v", meta)
	}
}

func TestLazyCommandBodyStatError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "commands", "staterr.md")
	mustWrite(t, path, "ok")

	regs, errs := LoadFromFS(LoaderOptions{ProjectRoot: root})
	if len(errs) != 0 || len(regs) != 1 {
		t.Fatalf("failed to load: %v", errs)
	}
	handler := regs[0].Handler

	restore := SetCommandFileOpsForTest(nil, nil, func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	})
	defer restore()

	if _, err := handler.Handle(context.Background(), Invocation{}); err == nil {
		t.Fatalf("expected stat error")
	}
}

func TestLazyCommandBodyParseErrorAfterReload(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "commands", "reload.md")
	mustWrite(t, path, "initial body")

	regs, errs := LoadFromFS(LoaderOptions{ProjectRoot: root})
	if len(errs) != 0 || len(regs) != 1 {
		t.Fatalf("failed to load: %v", errs)
	}
	handler := regs[0].Handler

	if _, err := handler.Handle(context.Background(), Invocation{}); err != nil {
		t.Fatalf("initial handle failed: %v", err)
	}

	broken := "---\nfoo: ["
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatalf("rewrite broken file: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if _, err := handler.Handle(context.Background(), Invocation{}); err == nil {
		t.Fatalf("expected parse error after corruption")
	}
}

func TestLoadCommandDirDuplicateNames(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mustWrite(t, filepath.Join(dir, "foo.md"), "one")
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	mustWrite(t, filepath.Join(dir, "subdir", "foo.md"), "two")

	_, errs := loadCommandDir(dir, resolveFileOps(LoaderOptions{}), resolveWalkDirFunc(LoaderOptions{}))
	if len(errs) == 0 || !hasError(errs, "duplicate command") {
		t.Fatalf("expected duplicate command error, got %v", errs)
	}
}

func TestLoadFromFSMergeOrderAndOverrideByCommandDirs(t *testing.T) {
	root := t.TempDir()

	defaultPath := filepath.Join(root, ".claude", "commands", "shared.md")
	extraOne := filepath.Join(root, "extra-one")
	extraTwo := filepath.Join(root, "extra-two")
	sharedOverridePath := filepath.Join(extraTwo, "shared.md")

	mustWrite(t, defaultPath, "from default")
	mustWrite(t, filepath.Join(extraOne, "shared.md"), "from extra-one")
	mustWrite(t, sharedOverridePath, "from extra-two")
	mustWrite(t, filepath.Join(extraTwo, "unique.md"), "unique body")

	regs, errs := LoadFromFS(LoaderOptions{
		ProjectRoot: root,
		CommandDirs: []string{extraOne, extraTwo},
	})
	if len(regs) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(regs))
	}

	regByName := map[string]CommandRegistration{}
	for _, reg := range regs {
		regByName[reg.Definition.Name] = reg
	}

	shared, ok := regByName["shared"]
	if !ok {
		t.Fatalf("expected shared command to be loaded")
	}

	sharedRes, err := shared.Handler.Handle(context.Background(), Invocation{})
	if err != nil {
		t.Fatalf("handle shared: %v", err)
	}
	out, ok := sharedRes.Output.(string)
	if !ok {
		t.Fatalf("expected string output, got %T", sharedRes.Output)
	}
	if out != "from extra-two" {
		t.Fatalf("expected later directory override, got %q", out)
	}
	if sharedRes.Metadata == nil || sharedRes.Metadata["source"] != sharedOverridePath {
		t.Fatalf("expected source to point to override file, got %#v", sharedRes.Metadata)
	}

	if !hasError(errs, "commands: warning: overriding command \"shared\"") {
		t.Fatalf("expected override warning, got %v", errs)
	}
	if !hasError(errs, defaultPath) {
		t.Fatalf("expected warning to include default path, got %v", errs)
	}
	if !hasError(errs, sharedOverridePath) {
		t.Fatalf("expected warning to include override path, got %v", errs)
	}
}
