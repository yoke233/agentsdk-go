package skills

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// LoaderOptions controls how skills are discovered from the filesystem.
type LoaderOptions struct {
	ProjectRoot string
	// Deprecated: user-level scanning has been removed; this field is ignored.
	UserHome string
	// Deprecated: user-level scanning has been removed; this flag is ignored.
	EnableUser bool
}

// SkillFile captures an on-disk SKILL.md plus its support files.
type SkillFile struct {
	Name         string
	Path         string
	Metadata     SkillMetadata
	Body         string
	SupportFiles map[string]string
}

// readFile is swappable in tests to track filesystem IO.
var readFile = os.ReadFile

// SkillMetadata mirrors the YAML frontmatter fields inside SKILL.md.
type SkillMetadata struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	AllowedTools string `yaml:"allowed-tools"`
}

// SkillRegistration wires a definition to its handler.
type SkillRegistration struct {
	Definition Definition
	Handler    Handler
}

var skillNameRegexp = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// LoadFromFS loads skills from the filesystem. Errors are aggregated so one
// broken file will not block others. Duplicate names are skipped with a
// warning entry in the error list.
func LoadFromFS(opts LoaderOptions) ([]SkillRegistration, []error) {
	var (
		registrations []SkillRegistration
		errs          []error
		allFiles      []SkillFile
	)

	projectDir := filepath.Join(opts.ProjectRoot, ".claude", "skills")
	files, loadErrs := loadSkillDir(projectDir)
	errs = append(errs, loadErrs...)
	allFiles = append(allFiles, files...)

	if len(allFiles) == 0 {
		return nil, errs
	}

	sort.Slice(allFiles, func(i, j int) bool {
		if allFiles[i].Metadata.Name != allFiles[j].Metadata.Name {
			return allFiles[i].Metadata.Name < allFiles[j].Metadata.Name
		}
		return allFiles[i].Path < allFiles[j].Path
	})

	seen := map[string]string{}
	for _, file := range allFiles {
		if prev, ok := seen[file.Metadata.Name]; ok {
			errs = append(errs, fmt.Errorf("skills: duplicate skill %q at %s (already from %s)", file.Metadata.Name, file.Path, prev))
			continue
		}
		seen[file.Metadata.Name] = file.Path

		def := Definition{
			Name:        file.Metadata.Name,
			Description: file.Metadata.Description,
			Metadata:    buildDefinitionMetadata(file),
		}
		reg := SkillRegistration{
			Definition: def,
			Handler:    buildHandler(file),
		}
		registrations = append(registrations, reg)
	}

	return registrations, errs
}

func loadSkillDir(root string) ([]SkillFile, []error) {
	var (
		results []SkillFile
		errs    []error
	)

	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("skills: stat %s: %w", root, err)}
	}
	if !info.IsDir() {
		return nil, []error{fmt.Errorf("skills: path %s is not a directory", root)}
	}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, fmt.Errorf("skills: walk %s: %w", path, walkErr))
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}

		dirName := filepath.Base(filepath.Dir(path))
		file, parseErr := parseSkillFile(path, dirName)
		if parseErr != nil {
			errs = append(errs, parseErr)
			return nil
		}

		results = append(results, file)
		return nil
	})
	if walkErr != nil {
		errs = append(errs, walkErr)
	}
	return results, errs
}

func parseSkillFile(path, dirName string) (SkillFile, error) {
	meta, err := readFrontMatter(path)
	if err != nil {
		return SkillFile{}, fmt.Errorf("skills: read %s: %w", path, err)
	}
	if meta.Name != "" && dirName != "" && meta.Name != dirName {
		return SkillFile{}, fmt.Errorf("skills: name %q does not match directory %q in %s", meta.Name, dirName, path)
	}
	if err := validateMetadata(meta); err != nil {
		return SkillFile{}, fmt.Errorf("skills: validate %s: %w", path, err)
	}

	return SkillFile{
		Name:     meta.Name,
		Path:     path,
		Metadata: meta,
	}, nil
}

func readFrontMatter(path string) (SkillMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return SkillMetadata{}, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	first, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return SkillMetadata{}, err
	}

	first = strings.TrimPrefix(first, "\uFEFF")
	if strings.TrimSpace(first) != "---" {
		return SkillMetadata{}, errors.New("missing YAML frontmatter")
	}

	var lines []string
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return SkillMetadata{}, readErr
		}
		if strings.TrimSpace(line) == "---" {
			metaText := strings.Join(lines, "")
			var meta SkillMetadata
			if err := yaml.Unmarshal([]byte(metaText), &meta); err != nil {
				return SkillMetadata{}, fmt.Errorf("decode YAML: %w", err)
			}
			return meta, nil
		}

		if line != "" {
			lines = append(lines, line)
		}

		if errors.Is(readErr, io.EOF) {
			return SkillMetadata{}, errors.New("missing closing frontmatter separator")
		}
	}
}

func parseFrontMatter(content string) (SkillMetadata, string, error) {
	trimmed := strings.TrimPrefix(content, "\uFEFF") // drop BOM if present
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return SkillMetadata{}, "", errors.New("missing YAML frontmatter")
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return SkillMetadata{}, "", errors.New("missing closing frontmatter separator")
	}

	metaText := strings.Join(lines[1:end], "\n")
	var meta SkillMetadata
	if err := yaml.Unmarshal([]byte(metaText), &meta); err != nil {
		return SkillMetadata{}, "", fmt.Errorf("decode YAML: %w", err)
	}

	body := strings.Join(lines[end+1:], "\n")
	body = strings.TrimPrefix(body, "\n")

	return meta, body, nil
}

func validateMetadata(meta SkillMetadata) error {
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		return errors.New("name is required")
	}
	if !skillNameRegexp.MatchString(name) {
		return fmt.Errorf("invalid name %q", meta.Name)
	}
	desc := strings.TrimSpace(meta.Description)
	if desc == "" {
		return errors.New("description is required")
	}
	if len(desc) > 1024 {
		return errors.New("description exceeds 1024 characters")
	}
	return nil
}

func loadSupportFiles(dir string) (map[string]string, []error) {
	out := map[string]string{}
	var errs []error

	readOptional := func(name string) {
		path := filepath.Join(dir, name)
		data, err := readFile(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("skills: read %s: %w", path, err))
			}
			return
		}
		out[name] = string(data)
	}

	for _, file := range []string{"reference.md", "examples.md"} {
		readOptional(file)
	}

	for _, sub := range []string{"scripts", "templates"} {
		root := filepath.Join(dir, sub)
		info, err := os.Stat(root)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("skills: stat %s: %w", root, err))
			}
			continue
		}
		if !info.IsDir() {
			errs = append(errs, fmt.Errorf("skills: %s is not a directory", root))
			continue
		}
		if walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				errs = append(errs, fmt.Errorf("skills: walk %s: %w", path, walkErr))
				return nil
			}
			if d.IsDir() {
				return nil
			}
			data, err := readFile(path)
			if err != nil {
				errs = append(errs, fmt.Errorf("skills: read %s: %w", path, err))
				return nil
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				rel = d.Name()
			}
			out[filepath.ToSlash(rel)] = string(data)
			return nil
		}); walkErr != nil {
			errs = append(errs, fmt.Errorf("skills: walk %s: %w", root, walkErr))
		}
	}

	if len(out) == 0 {
		return nil, errs
	}
	return out, errs
}

func buildDefinitionMetadata(file SkillFile) map[string]string {
	meta := map[string]string{}
	if file.Metadata.AllowedTools != "" {
		meta["allowed-tools"] = strings.TrimSpace(file.Metadata.AllowedTools)
	}
	if file.Path != "" {
		meta["source"] = file.Path
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func buildHandler(file SkillFile) Handler {
	return &lazySkillHandler{
		loader: func() (Result, error) {
			return loadSkillContent(file)
		},
	}
}

func loadSkillContent(file SkillFile) (Result, error) {
	body, err := loadSkillBody(file.Path)
	if err != nil {
		return Result{}, err
	}

	support, supportErrs := loadSupportFiles(filepath.Dir(file.Path))
	if err := errors.Join(supportErrs...); err != nil {
		return Result{}, err
	}

	output := map[string]any{"body": body}
	meta := map[string]any{}

	allowed := strings.TrimSpace(file.Metadata.AllowedTools)
	if allowed != "" {
		meta["allowed-tools"] = allowed
	}
	meta["source"] = file.Path

	if len(support) > 0 {
		output["support_files"] = support
		meta["support-file-count"] = len(support)
	}

	if len(meta) == 0 {
		meta = nil
	}

	return Result{
		Skill:    file.Metadata.Name,
		Output:   output,
		Metadata: meta,
	}, nil
}

func loadSkillBody(path string) (string, error) {
	data, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("skills: read %s: %w", path, err)
	}
	_, body, err := parseFrontMatter(string(data))
	if err != nil {
		return "", fmt.Errorf("skills: parse %s: %w", path, err)
	}
	return body, nil
}

// SetReadFileForTest swaps the file reader; intended for white-box tests only.
func SetReadFileForTest(fn func(string) ([]byte, error)) (restore func()) {
	prev := readFile
	readFile = fn
	return func() {
		readFile = prev
	}
}

// lazySkillHandler defers loading the skill body until first execution while
// exposing a cheap loaded-body length probe for observability.
type lazySkillHandler struct {
	once    sync.Once
	loader  func() (Result, error)
	cached  Result
	loadErr error
	loaded  atomic.Bool
}

func (h *lazySkillHandler) Execute(_ context.Context, _ ActivationContext) (Result, error) {
	if h == nil || h.loader == nil {
		return Result{}, errors.New("skills: handler is nil")
	}
	h.once.Do(func() {
		h.cached, h.loadErr = h.loader()
		h.loaded.Store(true)
	})
	if h.loadErr != nil {
		return Result{}, h.loadErr
	}
	return h.cached, nil
}

// BodyLength reports the cached body length without triggering a load. The
// second return value indicates whether a body has been loaded.
func (h *lazySkillHandler) BodyLength() (int, bool) {
	if h == nil || !h.loaded.Load() {
		return 0, false
	}
	return skillBodyLength(h.cached), true
}

func skillBodyLength(res Result) int {
	if res.Output == nil {
		return 0
	}
	if output, ok := res.Output.(map[string]any); ok {
		if body, ok := output["body"].(string); ok {
			return len(body)
		}
		if raw, ok := output["body"].([]byte); ok {
			return len(raw)
		}
	}
	return 0
}
