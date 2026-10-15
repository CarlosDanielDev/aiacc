package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Endpoint is a known remote API endpoint an account's settings file can
// point at. The model fields left empty are left out of the generated
// skeleton; BaseURL is always written — empty for custom, so the blank is
// loud and the user fills it in.
type Endpoint struct {
	BaseURL               string // the Anthropic-compatible API base URL ("" = user fills it in)
	Model                 string // ANTHROPIC_MODEL ("" = field omitted)
	CustomModelOption     string // ANTHROPIC_CUSTOM_MODEL_OPTION ("" = field omitted)
	CustomModelOptionName string // ANTHROPIC_CUSTOM_MODEL_OPTION_NAME ("" = field omitted)
	Filename              string // suggested settings filename
}

// PlaceholderToken is the obvious stand-in written where the user's API key
// goes. The user replaces it in their editor; aiacc never reads the file back
// (ADR-0007), so it never sees the real value.
const PlaceholderToken = "PASTE-YOUR-KEY-HERE"

var (
	ErrUnknownEndpoint = errors.New("unknown endpoint")
	ErrSettingsExists  = errors.New("settings file already exists")
)

// ResolveEndpoint resolves the named endpoint preset for a provider. Endpoints are
// claude-only — they exist to fill a settings file, and only a provider whose
// CLI has a settings flag can load one — so a provider without a flag is
// refused here exactly the way a --settings path is. An unknown name is
// refused by listing the valid ones.
func ResolveEndpoint(providerName, name string) (Endpoint, error) {
	p, ok := Presets[providerName]
	if !ok {
		return Endpoint{}, fmt.Errorf("%w: %s", ErrUnknownProvider, providerName)
	}
	if p.SettingsFlag == "" {
		return Endpoint{}, fmt.Errorf("%s has no settings flag, so a settings skeleton cannot apply to it", providerName)
	}
	ep, ok := p.Endpoints[name]
	if !ok {
		names := strings.Join(slices.Sorted(maps.Keys(p.Endpoints)), ", ")
		return Endpoint{}, fmt.Errorf("%w: %q (valid: %s)", ErrUnknownEndpoint, name, names)
	}
	return ep, nil
}

// WriteSettingsSkeleton writes a settings-file skeleton for ep into dir. The
// file holds an env block with the endpoint's base URL, PlaceholderToken where
// the key goes, and the endpoint's model fields; it is created with mode 0600,
// written once, and never opened by aiacc again (ADR-0007). A file already at
// the target is refused untouched: the existing path comes back wrapped in
// ErrSettingsExists.
func WriteSettingsSkeleton(ep Endpoint, dir string) error {
	if dir == "" {
		return fmt.Errorf("dir is required: a relative skeleton would land in the working directory")
	}
	path := filepath.Join(dir, ep.Filename)
	// O_EXCL is the whole overwrite guard: creation fails if the file is
	// already there, so the refusal is atomic and the file is never truncated.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w: %s", ErrSettingsExists, path)
		}
		return err
	}
	env := map[string]string{
		"ANTHROPIC_BASE_URL":   ep.BaseURL,
		"ANTHROPIC_AUTH_TOKEN": PlaceholderToken,
	}
	if ep.Model != "" {
		env["ANTHROPIC_MODEL"] = ep.Model
	}
	if ep.CustomModelOption != "" {
		env["ANTHROPIC_CUSTOM_MODEL_OPTION"] = ep.CustomModelOption
	}
	if ep.CustomModelOptionName != "" {
		env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"] = ep.CustomModelOptionName
	}
	doc, err := json.MarshalIndent(struct {
		Env map[string]string `json:"env"`
	}{Env: env}, "", "  ")
	if err == nil {
		_, err = f.Write(append(doc, '\n'))
	}
	if err == nil {
		err = f.Close() // a failed close can leave a short file behind too
	} else {
		f.Close() // best-effort; the error above is the one worth reporting
	}
	if err != nil {
		os.Remove(path) // leave no half-written skeleton for the retry to refuse
		return err
	}
	return nil
}

// EnsureSkeleton generates ep's settings skeleton for an account whose dir
// arrives as the user typed it (~/.claude-foo): the skeleton lands in the real
// location behind dir, and the returned path is the unexpanded join — the form
// the account registers, which SettingsFile/LaunchArgs re-expand at launch.
// Both add surfaces (the picker screen and the CLI flag) register through this
// one sequence so the expansion and write order cannot drift between them.
func EnsureSkeleton(dir string, ep Endpoint) (string, error) {
	realDir, err := ExpandHome(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		return "", err
	}
	if err := WriteSettingsSkeleton(ep, realDir); err != nil {
		return "", err
	}
	return filepath.Join(dir, ep.Filename), nil
}
