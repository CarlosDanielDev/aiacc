package cli

import "github.com/CarlosDanielDev/aiacc/internal/claude"

// loginOf reports the account logged into the Claude config dir dir. ok is
// false when no login is recorded, including when dir does not exist; callers
// choose their own fallback text for that case.
func loginOf(dir string) (email, org string, ok bool) {
	info := claude.Detect(dir)
	if !info.LoggedIn {
		return "", "", false
	}
	return info.Email, info.Organization, true
}
