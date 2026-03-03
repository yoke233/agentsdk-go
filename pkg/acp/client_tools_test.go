package acp

import (
	"testing"

	acpproto "github.com/coder/acp-go-sdk"
)

func TestBuildClientCapabilityTools(t *testing.T) {
	t.Parallel()

	noneTools, noneShadowed := buildClientCapabilityTools("sess-1", nil, acpproto.ClientCapabilities{})
	if len(noneTools) != 0 {
		t.Fatalf("no-capability tool count=%d, want 0", len(noneTools))
	}
	if len(noneShadowed) != 0 {
		t.Fatalf("no-capability shadowed count=%d, want 0", len(noneShadowed))
	}

	caps := acpproto.ClientCapabilities{}
	caps.Fs.ReadTextFile = true
	caps.Fs.WriteTextFile = true
	caps.Terminal = true

	tools, shadowed := buildClientCapabilityTools("sess-2", nil, caps)
	if len(tools) != 3 {
		t.Fatalf("tool count=%d, want 3", len(tools))
	}
	if len(shadowed) != 3 {
		t.Fatalf("shadowed count=%d, want 3", len(shadowed))
	}

	gotNames := make(map[string]struct{}, len(tools))
	for _, tl := range tools {
		gotNames[tl.Name()] = struct{}{}
	}
	for _, name := range []string{"Read", "Write", "Bash"} {
		if _, ok := gotNames[name]; !ok {
			t.Fatalf("missing tool %q in %#v", name, gotNames)
		}
	}
	for _, key := range []string{"file_read", "file_write", "bash"} {
		if !containsString(shadowed, key) {
			t.Fatalf("missing shadowed builtin %q in %#v", key, shadowed)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
