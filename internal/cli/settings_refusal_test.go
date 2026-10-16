package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

// A bare word that is neither a path nor a keyword must be refused in words
// that say what is wrong first, and the wrapped shared refusal must still
// classify as ErrUnknownEndpoint.
func TestSettingsRefusalSaysNeitherPathNorKeyword(t *testing.T) {
	_, err := resolveSettings("claude", t.TempDir(), "kimi")
	if err == nil {
		t.Fatal("--settings kimi was accepted")
	}
	msg := err.Error()
	for _, want := range []string{`"kimi"`, "neither a file path nor a known endpoint keyword", "custom, glm", "aiacc add"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q: %s", want, msg)
		}
	}
	if !strings.HasPrefix(msg, `--settings "kimi" is neither a file path nor`) {
		t.Errorf("refusal does not open by naming the value as invalid: %s", msg)
	}
	if !errors.Is(err, provider.ErrUnknownEndpoint) {
		t.Errorf("refusal does not wrap ErrUnknownEndpoint: %v", err)
	}
}
