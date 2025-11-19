package security

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

var (
	// ErrPathNotAllowed is returned when a path escapes the configured sandbox roots.
	ErrPathNotAllowed = errors.New("security: path not in sandbox allowlist")
)

// Sandbox is the first defensive layer: filesystem boundaries and command checks.
type Sandbox struct {
	mu        sync.RWMutex
	allowList []string
	validator *Validator
	resolver  *PathResolver
}

// NewSandbox creates a sandbox rooted at workDir.
func NewSandbox(workDir string) *Sandbox {
	root := normalizePath(workDir)
	if root == "" {
		root = string(filepath.Separator)
	}
	return &Sandbox{
		allowList: []string{root},
		validator: NewValidator(),
		resolver:  NewPathResolver(),
	}
}

// AllowShellMetachars enables shell pipes and metacharacters (CLI mode).
func (s *Sandbox) AllowShellMetachars(allow bool) {
	if s != nil && s.validator != nil {
		s.validator.AllowShellMetachars(allow)
	}
}

// Allow registers additional absolute prefixes that commands may touch.
func (s *Sandbox) Allow(path string) {
	normalized := normalizePath(path)
	if normalized == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.allowList {
		if existing == normalized {
			return
		}
	}
	s.allowList = append(s.allowList, normalized)
}

// ValidatePath ensures the path resolves within the sandbox allow list.
func (s *Sandbox) ValidatePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("security: empty path supplied")
	}

	resolved, err := s.resolver.Resolve(path)
	if err != nil {
		return fmt.Errorf("security: resolve failed: %w", err)
	}

	abs := normalizePath(resolved)

	s.mu.RLock()
	allowCopy := append([]string(nil), s.allowList...)
	s.mu.RUnlock()

	for _, allowed := range allowCopy {
		if withinSandbox(abs, allowed) {
			return nil
		}
	}

	return fmt.Errorf("%w: %s", ErrPathNotAllowed, abs)
}

// ValidateCommand is the second defense line, preventing obviously dangerous commands.
func (s *Sandbox) ValidateCommand(cmd string) error {
	if err := s.validator.Validate(cmd); err != nil {
		return fmt.Errorf("security: %w", err)
	}
	return nil
}

func normalizePath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func withinSandbox(path, prefix string) bool {
	if prefix == "" {
		return false
	}
	path = filepath.Clean(path)
	prefix = filepath.Clean(prefix)

	if path == prefix {
		return true
	}
	if prefix == string(filepath.Separator) {
		return true
	}
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(path, prefix)
}
