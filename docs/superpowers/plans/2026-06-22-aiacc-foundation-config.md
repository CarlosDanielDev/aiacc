# aiacc Foundation + Config Core — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a config-aware `aiacc` CLI that registers, removes, and lists AI-account config directories — the working base every later feature builds on.

**Architecture:** A cobra root command dispatches subcommands. An `internal/config` package owns TOML persistence of providers/accounts; an `internal/provider` package resolves the env-var + directory for a provider (merging the built-in Claude preset). Commands are thin wrappers that call those packages.

**Tech Stack:** Go 1.23, `github.com/spf13/cobra` (CLI), `github.com/BurntSushi/toml` (config file).

## Global Constraints

- Module path: `github.com/CarlosDanielDev/aiacc` (verbatim).
- Go floor: `1.23`.
- `gofmt -l .` clean and `go vet ./...` clean before every commit.
- Conventional Commit subjects, imperative, ≤ 72 chars; sign off with `-s` (DCO).
- A provider is `{name, env_var, config_dir}` (ADR-0002); Claude is a built-in preset, never special-cased in command code.
- Config file lives at `$XDG_CONFIG_HOME/aiacc/config.toml` (default `~/.config/aiacc/config.toml`).
- Prefer the standard library; the only new deps are cobra and BurntSushi/toml.

**Scope of this plan:** issues #1, #2, #3, #4 (milestones v0.1.0 + v0.2.0). Switching (#5–#8) and monitoring (#9–#11) are separate plans.

---

### Task 1: CLI skeleton (#1)

**Files:**
- Modify: `main.go`
- Create: `internal/cli/root.go`
- Create: `internal/cli/stubs.go`
- Test: `internal/cli/root_test.go`

**Interfaces:**
- Produces: `cli.NewRoot() *cobra.Command` — root command with subcommands `add`, `remove`, `list`, `use`, `status`, `usage`, `shell-init` attached, and a `--version` flag. `cli.Execute() error` runs it.

- [ ] **Step 1: Add cobra dependency**

Run:
```bash
cd /Users/carlos/projects/aiacc
go get github.com/spf13/cobra@latest
```
Expected: `go.mod` now requires `github.com/spf13/cobra`.

- [ ] **Step 2: Write the failing test**

Create `internal/cli/root_test.go`:
```go
package cli

import (
	"sort"
	"testing"
)

func TestNewRootHasAllSubcommands(t *testing.T) {
	root := NewRoot()
	var got []string
	for _, c := range root.Commands() {
		got = append(got, c.Name())
	}
	sort.Strings(got)
	want := []string{"add", "list", "remove", "shell-init", "status", "usage", "use"}
	if len(got) != len(want) {
		t.Fatalf("subcommands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("subcommands = %v, want %v", got, want)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestNewRootHasAllSubcommands -v`
Expected: FAIL — `undefined: NewRoot`.

- [ ] **Step 4: Write the root command**

Create `internal/cli/root.go`:
```go
// Package cli wires the aiacc command tree.
package cli

import "github.com/spf13/cobra"

// version is overridden at build time via -ldflags.
var version = "dev"

// NewRoot builds the aiacc command tree.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:     "aiacc",
		Short:   "Switch and monitor multiple AI-CLI accounts",
		Version: version,
	}
	root.AddCommand(
		newAddCmd(),
		newRemoveCmd(),
		newListCmd(),
		newUseCmd(),
		newStatusCmd(),
		newUsageCmd(),
		newShellInitCmd(),
	)
	return root
}

// Execute runs the root command.
func Execute() error { return NewRoot().Execute() }
```

- [ ] **Step 5: Write the command stubs**

Create `internal/cli/stubs.go`:
```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func notImplemented(name string) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, _ []string) error {
		fmt.Fprintf(c.OutOrStdout(), "%s: not implemented yet\n", name)
		return nil
	}
}

func newAddCmd() *cobra.Command {
	return &cobra.Command{Use: "add <provider> <account>", Short: "Register an account", RunE: notImplemented("add")}
}
func newRemoveCmd() *cobra.Command {
	return &cobra.Command{Use: "remove <provider> <account>", Short: "Unregister an account", RunE: notImplemented("remove")}
}
func newListCmd() *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List providers and accounts", RunE: notImplemented("list")}
}
func newUseCmd() *cobra.Command {
	return &cobra.Command{Use: "use <provider> <account>", Short: "Switch the shell to an account", RunE: notImplemented("use")}
}
func newStatusCmd() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show active account per provider", RunE: notImplemented("status")}
}
func newUsageCmd() *cobra.Command {
	return &cobra.Command{Use: "usage [provider]", Short: "Show token usage per account", RunE: notImplemented("usage")}
}
func newShellInitCmd() *cobra.Command {
	return &cobra.Command{Use: "shell-init <bash|zsh|fish>", Short: "Print the shell hook", RunE: notImplemented("shell-init")}
}
```

- [ ] **Step 6: Point main.go at cli.Execute**

Replace `main.go` with:
```go
// Command aiacc switches and monitors multiple AI-CLI accounts via per-account
// config directories and environment variables.
package main

import (
	"fmt"
	"os"

	"github.com/CarlosDanielDev/aiacc/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "aiacc:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 7: Tidy, run test, verify build**

Run:
```bash
go mod tidy && go test ./internal/cli/ -v && gofmt -l . && go vet ./... && go build ./...
```
Expected: test PASS, no gofmt output, build succeeds.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -s -m "feat(cmd): cobra root + subcommand skeleton + --version

Refs: #1"
```

---

### Task 2: config model + TOML load/save (#2)

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type Account struct { Dir string `toml:"dir"`; Quota int `toml:"quota,omitempty"` }`
  - `type Provider struct { EnvVar string `toml:"env_var"`; Accounts map[string]Account `toml:"accounts"` }`
  - `type Config struct { Providers map[string]Provider `toml:"providers"` }`
  - `func DefaultPath() (string, error)` — `$XDG_CONFIG_HOME/aiacc/config.toml` or `~/.config/...`.
  - `func Load(path string) (*Config, error)` — missing file returns an empty `*Config` (non-nil maps), nil error.
  - `func Save(path string, c *Config) error` — creates parent dir, writes TOML.

- [ ] **Step 1: Add TOML dependency**

Run: `go get github.com/BurntSushi/toml@latest`
Expected: `go.mod` requires `github.com/BurntSushi/toml`.

- [ ] **Step 2: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config

import (
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	in := &Config{Providers: map[string]Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]Account{
			"work": {Dir: "~/.claude-work", Quota: 0},
			"personal": {Dir: "~/.claude-personal", Quota: 100},
		}},
	}}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Providers["claude"].EnvVar != "CLAUDE_CONFIG_DIR" {
		t.Fatalf("env_var lost: %+v", out.Providers["claude"])
	}
	if out.Providers["claude"].Accounts["personal"].Quota != 100 {
		t.Fatalf("quota lost: %+v", out.Providers["claude"].Accounts["personal"])
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	out, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if out.Providers == nil || len(out.Providers) != 0 {
		t.Fatalf("want empty non-nil providers, got %+v", out.Providers)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: Config` / `Save` / `Load`.

- [ ] **Step 4: Write the implementation**

Create `internal/config/config.go`:
```go
// Package config persists aiacc providers and accounts as TOML.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Account struct {
	Dir   string `toml:"dir"`
	Quota int    `toml:"quota,omitempty"`
}

type Provider struct {
	EnvVar   string             `toml:"env_var"`
	Accounts map[string]Account `toml:"accounts"`
}

type Config struct {
	Providers map[string]Provider `toml:"providers"`
}

// DefaultPath returns the config file location, honoring XDG_CONFIG_HOME.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "aiacc", "config.toml"), nil
}

// Load reads the config; a missing file yields an empty, ready-to-use Config.
func Load(path string) (*Config, error) {
	c := &Config{Providers: map[string]Provider{}}
	if _, err := toml.DecodeFile(path, c); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return nil, err
	}
	if c.Providers == nil {
		c.Providers = map[string]Provider{}
	}
	return c, nil
}

// Save writes the config, creating the parent directory if needed.
func Save(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: both tests PASS.

- [ ] **Step 6: Commit**

```bash
go mod tidy && gofmt -l . && go vet ./...
git add -A
git commit -s -m "feat(config): config model + TOML load/save with XDG path

Refs: #2"
```

---

### Task 3: provider driver + Claude preset (#3)

**Files:**
- Create: `internal/provider/provider.go`
- Test: `internal/provider/provider_test.go`

**Interfaces:**
- Consumes: `config.Config`, `config.Provider`, `config.Account`.
- Produces:
  - `var Presets = map[string]string{"claude": "CLAUDE_CONFIG_DIR"}`
  - `func EnvVar(c *config.Config, provider string) (string, error)` — user config wins; else preset; else `ErrUnknownProvider`.
  - `func AccountDir(c *config.Config, provider, account string) (string, error)` — returns the account's `Dir` with a leading `~` expanded to the home dir; errors if provider or account is unknown.
  - `var ErrUnknownProvider = errors.New("unknown provider")`
  - `var ErrUnknownAccount = errors.New("unknown account")`

- [ ] **Step 1: Write the failing test**

Create `internal/provider/provider_test.go`:
```go
package provider

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

func TestEnvVarFromPresetWhenAbsent(t *testing.T) {
	c := &config.Config{Providers: map[string]config.Provider{}}
	got, err := EnvVar(c, "claude")
	if err != nil || got != "CLAUDE_CONFIG_DIR" {
		t.Fatalf("EnvVar = %q, %v; want CLAUDE_CONFIG_DIR", got, err)
	}
}

func TestEnvVarUnknownProvider(t *testing.T) {
	c := &config.Config{Providers: map[string]config.Provider{}}
	if _, err := EnvVar(c, "nope"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("want ErrUnknownProvider, got %v", err)
	}
}

func TestAccountDirExpandsHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	c := &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]config.Account{
			"work": {Dir: "~/.claude-work"},
		}},
	}}
	got, err := AccountDir(c, "claude", "work")
	if err != nil {
		t.Fatalf("AccountDir: %v", err)
	}
	if got != filepath.Join(home, ".claude-work") {
		t.Fatalf("AccountDir = %q, want %q", got, filepath.Join(home, ".claude-work"))
	}
}

func TestAccountDirUnknownAccount(t *testing.T) {
	c := &config.Config{Providers: map[string]config.Provider{
		"claude": {EnvVar: "CLAUDE_CONFIG_DIR", Accounts: map[string]config.Account{}},
	}}
	if _, err := AccountDir(c, "claude", "ghost"); !errors.Is(err, ErrUnknownAccount) {
		t.Fatalf("want ErrUnknownAccount, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -v`
Expected: FAIL — `undefined: EnvVar`.

- [ ] **Step 3: Write the implementation**

Create `internal/provider/provider.go`:
```go
// Package provider resolves env vars and account directories, merging
// built-in presets with user config (ADR-0002).
package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

// Presets are the env vars for providers aiacc knows out of the box.
var Presets = map[string]string{
	"claude": "CLAUDE_CONFIG_DIR",
}

var (
	ErrUnknownProvider = errors.New("unknown provider")
	ErrUnknownAccount  = errors.New("unknown account")
)

// EnvVar returns the environment variable a provider switches on. User config
// overrides the preset; a provider known by neither is an error.
func EnvVar(c *config.Config, provider string) (string, error) {
	if p, ok := c.Providers[provider]; ok && p.EnvVar != "" {
		return p.EnvVar, nil
	}
	if env, ok := Presets[provider]; ok {
		return env, nil
	}
	return "", ErrUnknownProvider
}

// AccountDir returns the account's directory with a leading ~ expanded.
func AccountDir(c *config.Config, provider, account string) (string, error) {
	p, ok := c.Providers[provider]
	if !ok {
		return "", ErrUnknownProvider
	}
	a, ok := p.Accounts[account]
	if !ok {
		return "", ErrUnknownAccount
	}
	return expandHome(a.Dir)
}

func expandHome(dir string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(dir, "~")), nil
	}
	return dir, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/ -v`
Expected: all four tests PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add -A
git commit -s -m "feat(provider): generic env-var driver + Claude preset

Refs: #3"
```

---

### Task 4: add / remove / list commands (#4)

**Files:**
- Modify: `internal/cli/stubs.go` (remove `add`, `remove`, `list` stubs)
- Create: `internal/cli/accounts.go`
- Test: `internal/cli/accounts_test.go`

**Interfaces:**
- Consumes: `config.Load`, `config.Save`, `provider.EnvVar`, `provider.Presets`.
- Produces: real `newAddCmd`, `newRemoveCmd`, `newListCmd`. All resolve the config path from a package var `configPath` (defaults to `config.DefaultPath()`), so tests can point it at a temp file.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/accounts_test.go`:
```go
package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

func withTempConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	old := configPath
	configPath = func() (string, error) { return p, nil }
	t.Cleanup(func() { configPath = old })
	return p
}

func TestAddThenListShowsAccount(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "work", "--dir", dir})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}

	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Providers["claude"].Accounts["work"].Dir != dir {
		t.Fatalf("account not saved: %+v", c.Providers["claude"])
	}

	list := newListCmd()
	var out bytes.Buffer
	list.SetOut(&out)
	if err := list.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "work") || !strings.Contains(out.String(), "claude") {
		t.Fatalf("list output missing account: %q", out.String())
	}
}

func TestRemoveDeletesAccount(t *testing.T) {
	withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "work", "--dir", dir})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	rm := newRemoveCmd()
	rm.SetArgs([]string{"claude", "work"})
	if err := rm.Execute(); err != nil {
		t.Fatalf("remove: %v", err)
	}

	path, _ := configPath()
	c, _ := config.Load(path)
	if _, ok := c.Providers["claude"].Accounts["work"]; ok {
		t.Fatalf("account still present after remove")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestAdd|TestRemove' -v`
Expected: FAIL — `undefined: configPath` (and stub commands ignore args).

- [ ] **Step 3: Remove the three stubs**

In `internal/cli/stubs.go`, delete the `newAddCmd`, `newRemoveCmd`, and `newListCmd` functions (they move to `accounts.go`). Keep the rest and `notImplemented`.

- [ ] **Step 4: Write the real commands**

Create `internal/cli/accounts.go`:
```go
package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/spf13/cobra"
)

// configPath is indirected so tests can point it at a temp file.
var configPath = config.DefaultPath

func newAddCmd() *cobra.Command {
	var dir string
	var quota int
	cmd := &cobra.Command{
		Use:   "add <provider> <account>",
		Short: "Register an account",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			providerName, account := args[0], args[1]
			if dir == "" {
				return fmt.Errorf("--dir is required")
			}
			env, err := provider.EnvVar(&config.Config{Providers: map[string]config.Provider{}}, providerName)
			if err != nil && providerName != "" {
				env = "" // unknown provider with no preset; user must define env via config later
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			p := c.Providers[providerName]
			if p.Accounts == nil {
				p.Accounts = map[string]config.Account{}
			}
			if p.EnvVar == "" {
				p.EnvVar = env
			}
			p.Accounts[account] = config.Account{Dir: dir, Quota: quota}
			c.Providers[providerName] = p
			return config.Save(path, c)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "config directory for this account (required)")
	cmd.Flags().IntVar(&quota, "quota", 0, "optional manual plan size")
	return cmd
}

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <provider> <account>",
		Short: "Unregister an account (keeps the directory)",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			providerName, account := args[0], args[1]
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			if p, ok := c.Providers[providerName]; ok {
				delete(p.Accounts, account)
				c.Providers[providerName] = p
			}
			return config.Save(path, c)
		},
	}
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List providers and accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := configPath()
			if err != nil {
				return err
			}
			c, err := config.Load(path)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "PROVIDER\tACCOUNT\tDIR")
			providers := make([]string, 0, len(c.Providers))
			for name := range c.Providers {
				providers = append(providers, name)
			}
			sort.Strings(providers)
			for _, pn := range providers {
				accounts := make([]string, 0, len(c.Providers[pn].Accounts))
				for a := range c.Providers[pn].Accounts {
					accounts = append(accounts, a)
				}
				sort.Strings(accounts)
				for _, a := range accounts {
					fmt.Fprintf(w, "%s\t%s\t%s\n", pn, a, c.Providers[pn].Accounts[a].Dir)
				}
			}
			return w.Flush()
		},
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/ -v`
Expected: all tests PASS (skeleton test from Task 1 still green).

- [ ] **Step 6: Manual smoke test**

Run:
```bash
go build -o /tmp/aiacc . && XDG_CONFIG_HOME=/tmp/aiacc-cfg /tmp/aiacc add claude work --dir /tmp/claude-work && XDG_CONFIG_HOME=/tmp/aiacc-cfg /tmp/aiacc list
```
Expected: a table row `claude  work  /tmp/claude-work`.

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./...
git add -A
git commit -s -m "feat(cmd): add / remove / list account commands

Closes #4"
```

---

## Self-Review

- **Spec coverage:** Tasks 1–4 cover #1 (skeleton), #2 (config), #3 (provider+preset), #4 (commands). Switching/monitoring intentionally out of scope (own plans).
- **Type consistency:** `config.Config/Provider/Account`, `provider.EnvVar/AccountDir/Presets`, `cli.configPath` used identically across tasks. `configPath` is `func() (string, error)` everywhere.
- **Known simplification:** in `newAddCmd`, an unknown provider with no preset is registered with an empty `env_var`; the user defines it later by editing config or a future `provider add` command. Acceptable for this slice — switching (#5/#6) is where a missing env_var becomes an error.

## Execution Handoff

Plan saved. Two execution options: Subagent-Driven (fresh subagent per task, review between) or Inline (executing-plans, batch with checkpoints).
