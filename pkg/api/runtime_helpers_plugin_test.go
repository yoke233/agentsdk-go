package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
)

func TestBuildLoaderAndRuntimesIncludePluginManifestDirs(t *testing.T) {
	root := t.TempDir()
	writePluginManifest(t, root, `{
  "commands": ["commands"],
  "agents": ["agents"],
  "skills": ["skills"]
}`)

	writeCommandFile(t, filepath.Join(root, ".claude-plugin", "commands", "ping.md"), "pong")
	writeSkillFile(t, filepath.Join(root, ".claude-plugin", "skills", "plugin-skill", "SKILL.md"), "plugin-skill", "from plugin")
	writeSubagentFile(t, filepath.Join(root, ".claude-plugin", "agents", "plugin-agent.md"), "plugin-agent", "plugin agent", "plugin prompt")

	loader := buildLoaderOptions(Options{ProjectRoot: root})
	if len(loader.CommandDirs) != 1 {
		t.Fatalf("expected 1 command dir, got %v", loader.CommandDirs)
	}
	if len(loader.SubagentDirs) != 1 {
		t.Fatalf("expected 1 subagent dir, got %v", loader.SubagentDirs)
	}
	if len(loader.SkillDirs) != 1 {
		t.Fatalf("expected 1 skill dir, got %v", loader.SkillDirs)
	}

	cmdExec, cmdErrs := buildCommandsExecutor(Options{ProjectRoot: root})
	if len(cmdErrs) != 0 {
		t.Fatalf("unexpected command build errors: %v", cmdErrs)
	}
	cmdRes, err := cmdExec.Run(context.Background(), "/ping")
	if err != nil {
		t.Fatalf("run /ping: %v", err)
	}
	if len(cmdRes) != 1 || cmdRes[0].Output != "pong" {
		t.Fatalf("unexpected command result: %+v", cmdRes)
	}

	skillReg, skillErrs := buildSkillsRegistry(Options{ProjectRoot: root})
	if len(skillErrs) != 0 {
		t.Fatalf("unexpected skill build errors: %v", skillErrs)
	}
	pluginSkill, ok := skillReg.Get("plugin-skill")
	if !ok {
		t.Fatalf("expected plugin skill to load")
	}
	skillRes, err := pluginSkill.Execute(context.Background(), skills.ActivationContext{})
	if err != nil {
		t.Fatalf("execute plugin skill: %v", err)
	}
	out, ok := skillRes.Output.(map[string]any)
	if !ok || out["body"] != "from plugin" {
		t.Fatalf("unexpected skill output: %#v", skillRes.Output)
	}

	subMgr, subErrs := buildSubagentsManager(Options{ProjectRoot: root})
	if len(subErrs) != 0 {
		t.Fatalf("unexpected subagent build errors: %v", subErrs)
	}
	if subMgr == nil {
		t.Fatalf("expected subagent manager")
	}
	subRes, err := subMgr.Dispatch(subagents.WithTaskDispatch(context.Background()), subagents.Request{
		Target:      "plugin-agent",
		Instruction: "do",
	})
	if err != nil {
		t.Fatalf("dispatch plugin-agent: %v", err)
	}
	if subRes.Output != "plugin prompt" {
		t.Fatalf("unexpected subagent output: %q", subRes.Output)
	}
}

func TestBuildLoaderOptionsUsesPluginRootOverride(t *testing.T) {
	root := t.TempDir()
	customPluginRoot := filepath.Join(root, "plugins", "myplugin")
	writePluginManifestAt(t, filepath.Join(customPluginRoot, "plugin.json"), `{
  "commands": ["commands"]
}`)
	writeCommandFile(t, filepath.Join(customPluginRoot, "commands", "rooted.md"), "rooted")

	loader := buildLoaderOptions(Options{ProjectRoot: root, PluginRoot: customPluginRoot})
	if len(loader.CommandDirs) != 1 {
		t.Fatalf("expected 1 command dir, got %v", loader.CommandDirs)
	}
	if want := filepath.Join(customPluginRoot, "commands"); loader.CommandDirs[0] != want {
		t.Fatalf("unexpected command dir %q, want %q", loader.CommandDirs[0], want)
	}

	exec, errs := buildCommandsExecutor(Options{ProjectRoot: root, PluginRoot: customPluginRoot})
	if len(errs) != 0 {
		t.Fatalf("unexpected command build errors: %v", errs)
	}
	res, err := exec.Run(context.Background(), "/rooted")
	if err != nil || len(res) != 1 || res[0].Output != "rooted" {
		t.Fatalf("unexpected command result: res=%+v err=%v", res, err)
	}
}

func TestBuildLoaderOptionsUsesPluginManifestPathOverride(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "plugin-manifests", "plugin.json")
	writePluginManifestAt(t, manifestPath, `{
  "agents": ["agents"]
}`)
	writeSubagentFile(t, filepath.Join(filepath.Dir(manifestPath), "agents", "manifest-agent.md"), "manifest-agent", "manifest agent", "manifest prompt")

	loader := buildLoaderOptions(Options{ProjectRoot: root, PluginManifestPath: manifestPath})
	if len(loader.SubagentDirs) != 1 {
		t.Fatalf("expected 1 subagent dir, got %v", loader.SubagentDirs)
	}
	if want := filepath.Join(filepath.Dir(manifestPath), "agents"); loader.SubagentDirs[0] != want {
		t.Fatalf("unexpected subagent dir %q, want %q", loader.SubagentDirs[0], want)
	}

	mgr, errs := buildSubagentsManager(Options{ProjectRoot: root, PluginManifestPath: manifestPath})
	if len(errs) != 0 {
		t.Fatalf("unexpected subagent build errors: %v", errs)
	}
	if mgr == nil {
		t.Fatalf("expected subagent manager")
	}
	res, err := mgr.Dispatch(subagents.WithTaskDispatch(context.Background()), subagents.Request{
		Target:      "manifest-agent",
		Instruction: "go",
	})
	if err != nil || res.Output != "manifest prompt" {
		t.Fatalf("unexpected dispatch result: res=%+v err=%v", res, err)
	}
}

func writePluginManifest(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, ".claude-plugin", "plugin.json")
	writePluginManifestAt(t, path, content)
}

func writePluginManifestAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func writeCommandFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write command: %v", err)
	}
}

func writeSkillFile(t *testing.T, path, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: plugin skill\n---\n" + body
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func writeSubagentFile(t *testing.T, path, name, desc, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n" + body
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write subagent: %v", err)
	}
}
