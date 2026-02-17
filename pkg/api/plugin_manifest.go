package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/config"
)

const (
	defaultPluginRootDir  = ".claude-plugin"
	defaultPluginManifest = "plugin.json"
)

var (
	allowedPluginManifestKeys = map[string]struct{}{
		"commands":   {},
		"agents":     {},
		"skills":     {},
		"claudecode": {},
	}
	allowedPluginManifestSectionKeys = map[string]struct{}{
		"commands": {},
		"agents":   {},
		"skills":   {},
	}
)

type pluginDirs struct {
	CommandDirs  []string
	SubagentDirs []string
	SkillDirs    []string
}

type pluginPathList []string

func (l *pluginPathList) UnmarshalJSON(data []byte) error {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 || string(data) == "null" {
		*l = nil
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if strings.TrimSpace(single) == "" {
			*l = nil
			return nil
		}
		*l = []string{single}
		return nil
	}

	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*l = append((*l)[:0], many...)
		return nil
	}
	return errors.New("expected string or array of strings")
}

type pluginManifestSection struct {
	Commands pluginPathList `json:"commands"`
	Agents   pluginPathList `json:"agents"`
	Skills   pluginPathList `json:"skills"`
}

type pluginManifestFile struct {
	pluginManifestSection
	ClaudeCode *pluginManifestSection `json:"claudeCode,omitempty"`
}

func loadPluginDirectories(projectRoot string, fsLayer *config.FS, pluginRoot, manifestPath string) (pluginDirs, []error) {
	manifest := resolvePluginManifestPath(projectRoot, pluginRoot, manifestPath)
	data, err := readPluginManifest(manifest, fsLayer)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return pluginDirs{}, nil
		}
		return pluginDirs{}, []error{fmt.Errorf("plugin: read %s: %w", manifest, err)}
	}

	warnings := collectUnknownKeyWarnings(data, manifest)

	var decoded pluginManifestFile
	if err := json.Unmarshal(data, &decoded); err != nil {
		warnings = append(warnings, fmt.Errorf("plugin: decode %s: %w", manifest, err))
		return pluginDirs{}, warnings
	}

	commands := append([]string(nil), decoded.Commands...)
	agents := append([]string(nil), decoded.Agents...)
	skills := append([]string(nil), decoded.Skills...)
	if decoded.ClaudeCode != nil {
		commands = append(commands, decoded.ClaudeCode.Commands...)
		agents = append(agents, decoded.ClaudeCode.Agents...)
		skills = append(skills, decoded.ClaudeCode.Skills...)
	}

	pluginBase := filepath.Dir(manifest)
	commandDirs, commandWarns := normalizePluginPaths(pluginBase, "commands", commands)
	subagentDirs, subagentWarns := normalizePluginPaths(pluginBase, "agents", agents)
	skillDirs, skillWarns := normalizePluginPaths(pluginBase, "skills", skills)
	warnings = append(warnings, commandWarns...)
	warnings = append(warnings, subagentWarns...)
	warnings = append(warnings, skillWarns...)

	return pluginDirs{
		CommandDirs:  commandDirs,
		SubagentDirs: subagentDirs,
		SkillDirs:    skillDirs,
	}, warnings
}

func resolvePluginManifestPath(projectRoot, pluginRoot, manifestPath string) string {
	if trimmed := strings.TrimSpace(manifestPath); trimmed != "" {
		return resolvePathAgainstProjectRoot(projectRoot, trimmed)
	}
	root := resolvePluginRootPath(projectRoot, pluginRoot)
	return filepath.Join(root, defaultPluginManifest)
}

func resolvePluginRootPath(projectRoot, pluginRoot string) string {
	root := strings.TrimSpace(pluginRoot)
	if root == "" {
		root = defaultPluginRootDir
	}
	return resolvePathAgainstProjectRoot(projectRoot, root)
}

func readPluginManifest(path string, fsLayer *config.FS) ([]byte, error) {
	if fsLayer != nil {
		return fsLayer.ReadFile(path)
	}
	return os.ReadFile(path)
}

func normalizePluginPaths(pluginRoot, section string, paths []string) ([]string, []error) {
	normalized := make([]string, 0, len(paths))
	var warnings []error
	seen := map[string]struct{}{}
	absRoot := filepath.Clean(pluginRoot)
	if resolved, err := filepath.Abs(pluginRoot); err == nil {
		absRoot = filepath.Clean(resolved)
	}

	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}

		if filepath.IsAbs(path) {
			warnings = append(warnings, fmt.Errorf("plugin: warning: %s path %q must be relative; ignored", section, path))
			continue
		}

		joined := filepath.Join(absRoot, path)
		absPath := filepath.Clean(joined)
		if resolved, err := filepath.Abs(joined); err == nil {
			absPath = filepath.Clean(resolved)
		}

		if !isWithinRoot(absRoot, absPath) {
			warnings = append(warnings, fmt.Errorf("plugin: warning: %s path %q escapes plugin root; ignored", section, path))
			continue
		}
		path = absPath

		key := path
		if os.PathSeparator == '\\' {
			key = strings.ToLower(path)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, path)
	}

	return normalized, warnings
}

func isWithinRoot(root, target string) bool {
	r := filepath.Clean(root)
	t := filepath.Clean(target)
	if os.PathSeparator == '\\' {
		r = strings.ToLower(r)
		t = strings.ToLower(t)
	}
	if r == t {
		return true
	}
	if strings.HasSuffix(r, string(filepath.Separator)) {
		return strings.HasPrefix(t, r)
	}
	return strings.HasPrefix(t, r+string(filepath.Separator))
}

func collectUnknownKeyWarnings(data []byte, manifestPath string) []error {
	var warnings []error

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil
	}

	for key := range top {
		if _, ok := allowedPluginManifestKeys[strings.ToLower(strings.TrimSpace(key))]; !ok {
			warnings = append(warnings, fmt.Errorf("plugin: warning: unknown key %q in %s", key, manifestPath))
		}
	}

	rawSection, ok := top["claudeCode"]
	if !ok {
		rawSection, ok = top["claudecode"]
	}
	if !ok {
		return warnings
	}
	if strings.TrimSpace(string(rawSection)) == "" || strings.TrimSpace(string(rawSection)) == "null" {
		return warnings
	}

	var section map[string]json.RawMessage
	if err := json.Unmarshal(rawSection, &section); err != nil {
		return warnings
	}
	for key := range section {
		if _, ok := allowedPluginManifestSectionKeys[strings.ToLower(strings.TrimSpace(key))]; !ok {
			warnings = append(warnings, fmt.Errorf("plugin: warning: unknown key %q in %s.claudeCode", key, manifestPath))
		}
	}
	return warnings
}
