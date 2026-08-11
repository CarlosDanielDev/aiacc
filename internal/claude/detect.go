// Package claude reads the non-secret account identity (email, display name,
// organization) that Claude Code records for a config directory, so aiacc can
// show which account a directory is logged in as. It never reads or handles
// credentials or tokens — only the public account label.
package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Info is the non-secret identity of the account logged into a config dir.
type Info struct {
	Email        string
	DisplayName  string
	Organization string
	LoggedIn     bool
}

// configFile mirrors just the oauthAccount label from Claude's config JSON.
type configFile struct {
	OAuthAccount struct {
		Email        string `json:"emailAddress"`
		DisplayName  string `json:"displayName"`
		Organization string `json:"organizationName"`
	} `json:"oauthAccount"`
}

// Detect returns the account logged into the Claude config directory dir, or a
// zero Info (LoggedIn=false) when none is found. It looks in <dir>/.claude.json
// first, then the sibling <dir>.json — Claude's default layout stores ~/.claude's
// config at ~/.claude.json, so the sibling form covers the default account.
func Detect(dir string) Info {
	for _, p := range []string{filepath.Join(dir, ".claude.json"), dir + ".json"} {
		if info, ok := read(p); ok {
			return info
		}
	}
	return Info{}
}

func read(path string) (Info, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Info{}, false
	}
	var c configFile
	if err := json.Unmarshal(b, &c); err != nil {
		return Info{}, false
	}
	if c.OAuthAccount.Email == "" {
		return Info{}, false
	}
	return Info{
		Email:        c.OAuthAccount.Email,
		DisplayName:  c.OAuthAccount.DisplayName,
		Organization: c.OAuthAccount.Organization,
		LoggedIn:     true,
	}, true
}
