package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// loginOf reports the account logged into a Claude config dir. A dir with no
// recorded login, and a dir that does not exist, both read as not logged in.
func TestLoginOf(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T) string
		wantMail string
		wantOrg  string
		wantOK   bool
	}{
		{
			name: "logged in",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				writeFile(t, filepath.Join(dir, ".claude.json"),
					`{"oauthAccount":{"emailAddress":"ana@example.com","organizationName":"Acme"}}`)
				return dir
			},
			wantMail: "ana@example.com",
			wantOrg:  "Acme",
			wantOK:   true,
		},
		{
			name: "logged out",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				writeFile(t, filepath.Join(dir, ".claude.json"), `{"numStartups":3}`)
				return dir
			},
		},
		{
			name: "missing dir",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "absent")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			email, org, ok := loginOf(tc.setup(t))
			if email != tc.wantMail || org != tc.wantOrg || ok != tc.wantOK {
				t.Fatalf("loginOf() = (%q, %q, %v), want (%q, %q, %v)",
					email, org, ok, tc.wantMail, tc.wantOrg, tc.wantOK)
			}
		})
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
