package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
)

func TestBuildLoaderOptionsIncludesSkillDirConfig(t *testing.T) {
	opts := Options{
		ProjectRoot:                 t.TempDir(),
		SkillDirs:                   []string{"a", "b"},
		DisableDefaultProjectSkills: true,
	}

	loader := buildLoaderOptions(opts)
	if loader.ProjectRoot != opts.ProjectRoot {
		t.Fatalf("ProjectRoot=%q, want %q", loader.ProjectRoot, opts.ProjectRoot)
	}
	if !loader.DisableDefaultProjectSkills {
		t.Fatalf("DisableDefaultProjectSkills should be true")
	}
	if len(loader.SkillDirs) != 2 || loader.SkillDirs[0] != "a" || loader.SkillDirs[1] != "b" {
		t.Fatalf("SkillDirs=%v, want [a b]", loader.SkillDirs)
	}

	opts.SkillDirs[0] = "changed"
	if loader.SkillDirs[0] != "a" {
		t.Fatalf("loader.SkillDirs should be a defensive copy, got %v", loader.SkillDirs)
	}
}

func TestBuildSkillsRegistryDefaultAndExtraDirs(t *testing.T) {
	root := t.TempDir()
	extraDir := filepath.Join(root, "external-skills")
	defaultShared := filepath.Join(root, ".claude", "skills", "shared", "SKILL.md")
	extraShared := filepath.Join(extraDir, "shared", "SKILL.md")
	defaultOnly := filepath.Join(root, ".claude", "skills", "default-only", "SKILL.md")

	writeAPISkill(t, defaultShared, "shared", "from default")
	writeAPISkill(t, extraShared, "shared", "from extra")
	writeAPISkill(t, defaultOnly, "default-only", "default only body")

	reg, errs := buildSkillsRegistry(Options{
		ProjectRoot: root,
		SkillDirs:   []string{extraDir},
	})
	if reg == nil {
		t.Fatalf("expected registry")
	}

	shared, ok := reg.Get("shared")
	if !ok {
		t.Fatalf("expected shared skill")
	}
	sharedRes, err := shared.Execute(context.Background(), skills.ActivationContext{})
	if err != nil {
		t.Fatalf("execute shared: %v", err)
	}
	sharedOutput, ok := sharedRes.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", sharedRes.Output)
	}
	if sharedOutput["body"] != "from extra" {
		t.Fatalf("expected extra dir to override default, got %v", sharedOutput["body"])
	}

	if _, ok := reg.Get("default-only"); !ok {
		t.Fatalf("expected default directory skill to remain available")
	}

	warnings := strings.Join(errorStringsFromBuild(errs), "\n")
	if !strings.Contains(warnings, "overriding skill \"shared\"") {
		t.Fatalf("expected override warning, got %v", errs)
	}
	if !strings.Contains(warnings, defaultShared) || !strings.Contains(warnings, extraShared) {
		t.Fatalf("expected warning to include source paths, got %v", errs)
	}
}

func TestBuildSkillsRegistryDisableDefaultProjectSkills(t *testing.T) {
	root := t.TempDir()
	extraDir := filepath.Join(root, "external-skills")

	writeAPISkill(t, filepath.Join(root, ".claude", "skills", "default-only", "SKILL.md"), "default-only", "default body")
	writeAPISkill(t, filepath.Join(extraDir, "extra-only", "SKILL.md"), "extra-only", "extra body")

	reg, errs := buildSkillsRegistry(Options{
		ProjectRoot:                 root,
		DisableDefaultProjectSkills: true,
		SkillDirs:                   []string{extraDir},
	})
	if len(errs) != 0 {
		t.Fatalf("expected no warnings, got %v", errs)
	}
	if reg == nil {
		t.Fatalf("expected registry")
	}

	if _, ok := reg.Get("default-only"); ok {
		t.Fatalf("default project skills should be disabled")
	}
	extraSkill, ok := reg.Get("extra-only")
	if !ok {
		t.Fatalf("expected extra-only skill")
	}
	res, err := extraSkill.Execute(context.Background(), skills.ActivationContext{})
	if err != nil {
		t.Fatalf("execute extra-only: %v", err)
	}
	output, ok := res.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", res.Output)
	}
	if output["body"] != "extra body" {
		t.Fatalf("unexpected output body %v", output["body"])
	}
}

func writeAPISkill(t *testing.T, path, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := strings.Join([]string{
		"---",
		"name: " + name,
		"description: test",
		"---",
		body,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func errorStringsFromBuild(errs []error) []string {
	if len(errs) == 0 {
		return nil
	}
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		out = append(out, err.Error())
	}
	return out
}
