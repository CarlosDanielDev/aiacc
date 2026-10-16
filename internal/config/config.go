// Package config persists aiacc providers and accounts as TOML.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
)

type Account struct {
	Dir   string `toml:"dir"`
	Quota int    `toml:"quota,omitempty"`
	// Settings is an extra settings file the launcher hands the CLI, e.g. an
	// alternate API endpoint. Only the path is stored, never its contents.
	Settings string `toml:"settings,omitempty"`
}

type Provider struct {
	EnvVar   string             `toml:"env_var"`
	Command  string             `toml:"command,omitempty"`
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
	return c, nil
}

// Save writes the config, creating the parent directory if needed. The write
// is atomic: the TOML is encoded into a temp file in the same directory and
// renamed over the target, so a concurrent reader sees either the old config
// or the new one — never a half-written file a decode would choke on. It
// refuses to overwrite a file that holds keys this build does not know, since
// the encoder writes only struct fields and would silently drop them (see
// guardUnknownKeys).
func Save(path string, c *Config) error {
	if err := guardUnknownKeys(path); err != nil {
		return err
	}
	// Follow a symlinked config so the rename lands on the real file: renaming
	// onto the link itself would replace it with a regular file and silently
	// break setups that link config.toml into a dotfiles repo.
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Keep the existing mode across the rename: a bare temp file would
	// otherwise reset a hand-tightened config.toml to 0600 on every save.
	mode := os.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(target)+".tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has moved it away
	if err := toml.NewEncoder(tmp).Encode(c); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

// Update runs mutate on the loaded config under an exclusive file lock that
// spans the whole load-modify-save cycle, so two concurrent mutating commands
// serialize instead of both reading the same state and racing to a
// last-writer-wins save that silently drops one side's change. A mutate that
// returns an error aborts the update without saving.
//
// The lock file (path+".lock") is deliberately left behind after the update:
// deleting it on the way out would race a concurrent mutation that just
// opened it, so it persists as a harmless zero-byte sibling of the config.
func Update(path string, mutate func(*Config) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	c, err := Load(path)
	if err != nil {
		return err
	}
	if err := mutate(c); err != nil {
		return err
	}
	return Save(path, c)
}

// guardUnknownKeys refuses a save that would silently destroy keys this
// build's structs do not carry: decoding ignores them and the encoder writes
// only struct fields, so a key added by a newer aiacc (or a hand edit) under
// a provider or account would vanish on the next mutation. Known configs
// round-trip untouched; foreign ones stop the mutation with the keys named.
func guardUnknownKeys(path string) error {
	var fresh Config
	md, err := toml.DecodeFile(path, &fresh)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // nothing on disk to clobber
		}
		return err // a file we cannot parse is not one we should overwrite
	}
	var unknown []string
	for _, key := range md.Undecoded() {
		unknown = append(unknown, key.String())
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	return fmt.Errorf("refusing to save %s: unknown keys would be lost: %s — "+
		"the file holds keys this aiacc does not know (a newer version or a hand "+
		"edit added them); remove these keys from the file, or run a newer aiacc "+
		"that understands them, before mutating the config", path, strings.Join(unknown, ", "))
}
