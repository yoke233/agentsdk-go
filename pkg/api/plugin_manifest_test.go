package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPluginDirectoriesMissingManifest(t *testing.T) {
	root := t.TempDir()
	dirs, errs := loadPluginDirectories(root, nil, "", "")
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if len(dirs.CommandDirs) != 0 || len(dirs.SubagentDirs) != 0 || len(dirs.SkillDirs) != 0 {
		t.Fatalf("expected empty dirs, got %+v", dirs)
	}
}

func TestLoadPluginDirectoriesParsesManifest(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, ".claude-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := `{
  "commands": ["commands", "commands", "more/commands"],
  "agents": "agents",
  "skills": ["skills"],
  "claudeCode": {
    "commands": ["extra-commands"],
    "skills": "extra-skills"
  }
}`
	if err := os.WriteFile(manifestPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	dirs, errs := loadPluginDirectories(root, nil, "", "")
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}

	wantCommands := []string{
		filepath.Join(root, ".claude-plugin", "commands"),
		filepath.Join(root, ".claude-plugin", "more", "commands"),
		filepath.Join(root, ".claude-plugin", "extra-commands"),
	}
	if !equalPaths(dirs.CommandDirs, wantCommands) {
		t.Fatalf("CommandDirs=%v, want %v", dirs.CommandDirs, wantCommands)
	}
	wantAgents := []string{
		filepath.Join(root, ".claude-plugin", "agents"),
	}
	if !equalPaths(dirs.SubagentDirs, wantAgents) {
		t.Fatalf("SubagentDirs=%v, want %v", dirs.SubagentDirs, wantAgents)
	}
	wantSkills := []string{
		filepath.Join(root, ".claude-plugin", "skills"),
		filepath.Join(root, ".claude-plugin", "extra-skills"),
	}
	if !equalPaths(dirs.SkillDirs, wantSkills) {
		t.Fatalf("SkillDirs=%v, want %v", dirs.SkillDirs, wantSkills)
	}
}

func TestLoadPluginDirectoriesInvalidManifest(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, ".claude-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"commands": [}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, errs := loadPluginDirectories(root, nil, "", "")
	if len(errs) == 0 {
		t.Fatalf("expected manifest parse error")
	}
}

func TestLoadPluginDirectoriesWithCustomPluginRoot(t *testing.T) {
	root := t.TempDir()
	customRoot := filepath.Join(root, "plugins", "acme")
	manifestPath := filepath.Join(customRoot, "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := `{"commands":"commands"}`
	if err := os.WriteFile(manifestPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	dirs, errs := loadPluginDirectories(root, nil, customRoot, "")
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	want := []string{filepath.Join(customRoot, "commands")}
	if !equalPaths(dirs.CommandDirs, want) {
		t.Fatalf("CommandDirs=%v, want %v", dirs.CommandDirs, want)
	}
}

func TestLoadPluginDirectoriesWithExplicitManifestPath(t *testing.T) {
	root := t.TempDir()
	customManifest := filepath.Join(root, "manifests", "my-plugin.json")
	if err := os.MkdirAll(filepath.Dir(customManifest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := `{"skills":"skills"}`
	if err := os.WriteFile(customManifest, []byte(raw), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	dirs, errs := loadPluginDirectories(root, nil, "", customManifest)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	want := []string{filepath.Join(filepath.Dir(customManifest), "skills")}
	if !equalPaths(dirs.SkillDirs, want) {
		t.Fatalf("SkillDirs=%v, want %v", dirs.SkillDirs, want)
	}
}

func TestLoadPluginDirectoriesUnknownKeyWarnings(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, ".claude-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := `{
  "commands": ["commands"],
  "name": "demo-plugin",
  "claudeCode": {
    "skills": ["skills"],
    "extra": true
  }
}`
	if err := os.WriteFile(manifestPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	dirs, errs := loadPluginDirectories(root, nil, "", "")
	if len(dirs.CommandDirs) != 1 {
		t.Fatalf("expected command dirs loaded, got %+v", dirs)
	}
	if !hasWarning(errs, `unknown key "name"`) {
		t.Fatalf("expected unknown key warning for top-level key, got %v", errs)
	}
	if !hasWarning(errs, `unknown key "extra"`) {
		t.Fatalf("expected unknown key warning for claudeCode key, got %v", errs)
	}
}

func TestLoadPluginDirectoriesPathSafetyWarnings(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, ".claude-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	absPath := filepath.Join(root, "outside-abs")
	rawMap := map[string]any{
		"commands": []string{
			"commands",
			"../escape",
			absPath,
		},
	}
	raw, err := json.Marshal(rawMap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	dirs, errs := loadPluginDirectories(root, nil, "", "")
	want := []string{filepath.Join(root, ".claude-plugin", "commands")}
	if !equalPaths(dirs.CommandDirs, want) {
		t.Fatalf("CommandDirs=%v, want %v", dirs.CommandDirs, want)
	}
	if !hasWarning(errs, `escapes plugin root`) {
		t.Fatalf("expected escape warning, got %v", errs)
	}
	if !hasWarning(errs, fmt.Sprintf(`path %q must be relative`, absPath)) {
		t.Fatalf("expected absolute path warning, got %v", errs)
	}
}

func TestResolvePluginRootPathRelativeProjectRoot(t *testing.T) {
	projectRoot := filepath.Join("testdata", "proj")

	got := resolvePluginRootPath(projectRoot, "")
	want := resolvePathAgainstProjectRoot(projectRoot, defaultPluginRootDir)
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("resolvePluginRootPath mismatch: got=%q want=%q", got, want)
	}

	dup := filepath.Join(projectRoot, projectRoot, defaultPluginRootDir)
	if filepath.Clean(got) == filepath.Clean(dup) {
		t.Fatalf("plugin root duplicated project root: %q", got)
	}
}

func equalPaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if filepath.Clean(got[i]) != filepath.Clean(want[i]) {
			return false
		}
	}
	return true
}

func hasWarning(errs []error, pattern string) bool {
	for _, err := range errs {
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), pattern) {
			return true
		}
	}
	return false
}
