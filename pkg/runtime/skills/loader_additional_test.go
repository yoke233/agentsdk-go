package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFrontMatterMissingClosing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	mustWrite(t, path, "---\nname: test\ndescription: desc\n")

	if _, err := readFrontMatter(path); err == nil || !strings.Contains(err.Error(), "closing frontmatter") {
		t.Fatalf("expected closing frontmatter error, got %v", err)
	}
}

func TestValidateMetadataErrors(t *testing.T) {
	cases := []SkillMetadata{
		{Name: "", Description: "d"},
		{Name: "Bad!", Description: "d"},
		{Name: "ok", Description: ""},
		{Name: "ok", Description: strings.Repeat("x", 1025)},
	}

	for i, meta := range cases {
		if err := validateMetadata(meta); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestLoadSupportFilesErrors(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "scripts"), []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("prep: %v", err)
	}

	restore := SetReadFileForTest(func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "reference.md") {
			return nil, fs.ErrPermission
		}
		return os.ReadFile(path)
	})
	defer restore()

	support, errs := loadSupportFiles(dir)
	if len(errs) < 2 { // one for scripts not being a dir, one for reference read failure
		t.Fatalf("expected aggregated errors, got %v", errs)
	}
	if support != nil {
		t.Fatalf("expected no support files, got %v", support)
	}
}

func TestLoadSupportFilesNoFiles(t *testing.T) {
	dir := t.TempDir()

	support, errs := loadSupportFiles(dir)
	if support != nil {
		t.Fatalf("expected nil support map, got %v", support)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestLoadSkillBodyReadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	mustWrite(t, path, "---\nname: fail\ndescription: desc\n---\nbody")

	restore := SetReadFileForTest(func(string) ([]byte, error) {
		return nil, fs.ErrPermission
	})
	defer restore()

	if _, err := loadSkillBody(path); err == nil {
		t.Fatalf("expected read error")
	}
}

func TestLoadSkillBodyParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	mustWrite(t, path, "---\nname: fail\ndescription: desc\n")

	if _, err := loadSkillBody(path); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestParseFrontMatterInvalidYAML(t *testing.T) {
	if _, _, err := parseFrontMatter("---\nname: [\n---\nbody"); err == nil {
		t.Fatalf("expected YAML decode error")
	}
}

func TestReadFrontMatterWithBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	mustWrite(t, path, "\uFEFF---\nname: bom\ndescription: desc\n---\n")

	meta, err := readFrontMatter(path)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if meta.Name != "bom" {
		t.Fatalf("unexpected name %q", meta.Name)
	}
}

func TestLoadSkillContentSupportError(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")
	reference := filepath.Join(dir, "reference.md")
	writeSkill(t, skillPath, "lazy", "body")
	mustWrite(t, reference, "ref")

	restore := SetReadFileForTest(func(path string) ([]byte, error) {
		if path == reference {
			return nil, fs.ErrPermission
		}
		return os.ReadFile(path)
	})
	defer restore()

	if _, err := loadSkillContent(SkillFile{Path: skillPath, Metadata: SkillMetadata{Name: "lazy"}}); err == nil {
		t.Fatalf("expected support load error")
	}
}

func TestLoadSkillDirNotDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notdir")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatalf("prep: %v", err)
	}

	_, errs := loadSkillDir(file)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "not a directory") {
		t.Fatalf("expected not a directory error, got %v", errs)
	}
}

func TestLoadSkillDirMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there")
	if result, errs := loadSkillDir(missing); result != nil || len(errs) != 0 {
		t.Fatalf("expected nil results and no errors, got %v %v", result, errs)
	}
}

func TestSkillBodyLengthVariants(t *testing.T) {
	if size := skillBodyLength(Result{}); size != 0 {
		t.Fatalf("expected zero for empty result, got %d", size)
	}
	if size := skillBodyLength(Result{Output: map[string]any{"body": []byte("abc")}}); size != 3 {
		t.Fatalf("expected byte slice length, got %d", size)
	}
	if size := skillBodyLength(Result{Output: map[string]any{"body": 123}}); size != 0 {
		t.Fatalf("expected unsupported body types to return zero, got %d", size)
	}
}
