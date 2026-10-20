package provider

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// FillEndpoint fills an endpoint preset whose base URL is the user's to give
// (custom) from the user's answers: the base URL and the model, which goes to
// both ANTHROPIC_MODEL and ANTHROPIC_CUSTOM_MODEL_OPTION, and an optional
// display name that defaults to the model. Both add surfaces that take these
// answers (the CLI flags and the line wizard) validate through it, before
// anything is written. The API key is never one of the answers (ADR-0007).
func FillEndpoint(ep Endpoint, baseURL, model, modelName string) (Endpoint, error) {
	base, err := CheckBaseURL(baseURL)
	if err != nil {
		return Endpoint{}, err
	}
	model = strings.TrimSpace(model)
	if err := CheckModel(model); err != nil {
		return Endpoint{}, err
	}
	name := strings.TrimSpace(modelName)
	if name == "" {
		name = model
	}
	ep.BaseURL, ep.Model, ep.CustomModelOption, ep.CustomModelOptionName = base, model, model, name
	return ep, nil
}

// CheckBaseURL validates a custom endpoint's base URL and returns it with
// surrounding space and any trailing slash trimmed. The URL must be https —
// plain http only for the loopback (a local proxy) — with a host, no
// user:pass@ (that would put a secret in a file aiacc wrote), and no query or
// fragment. A refusal never echoes the value, so a pasted secret stays out of
// the terminal scrollback.
func CheckBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("base URL is required (https://…)")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("base URL does not parse as a URL")
	}
	switch {
	case u.User != nil:
		return "", errors.New("base URL must not carry user:password@ — the key goes in the settings file, not the URL")
	case u.Scheme != "https" && u.Scheme != "http":
		return "", errors.New("base URL must start with https://")
	case u.Hostname() == "":
		return "", errors.New("base URL has no host")
	case u.Scheme == "http" && !isLoopback(strings.ToLower(u.Hostname())):
		return "", errors.New("base URL must be https:// (http:// only for localhost, 127.0.0.1 or ::1)")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#"):
		return "", errors.New("base URL must not carry a ?query or #fragment")
	}
	return strings.TrimRight(raw, "/"), nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// CheckModel validates a custom endpoint's model id: non-empty, no
// whitespace, and not the key placeholder pasted into the wrong field.
func CheckModel(model string) error {
	switch {
	case model == "":
		return errors.New("model is required")
	case strings.ContainsFunc(model, unicode.IsSpace):
		return fmt.Errorf("model %q must not contain whitespace", model)
	case model == PlaceholderToken:
		return errors.New("model is the key placeholder; give the endpoint's model id")
	}
	return nil
}
