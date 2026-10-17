package provider

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/CarlosDanielDev/aiacc/internal/fsutil"
)

// This file is the one place aiacc reads and rewrites an account's settings
// file. It is the narrow exception ADR-0009 records to ADR-0007's "generate,
// never read": only the explicit edit action calls it, it touches the three
// endpoint keys and nothing else, and it never returns, prints or logs the
// token.

// The env keys the edit screen owns. Every other key in the file is kept as is.
const (
	envAuthToken = "ANTHROPIC_AUTH_TOKEN"
	envBaseURL   = "ANTHROPIC_BASE_URL"
	envModel     = "ANTHROPIC_MODEL"
)

// Refusal reasons. Each says what is wrong with the file and never quotes its
// contents, so a refusal is safe to show on screen.
var (
	ErrSettingsMissing = errors.New("is missing")
	ErrSettingsDir     = errors.New("is a directory, not a settings file")
	ErrSettingsInvalid = errors.New("is not valid JSON")
	ErrSettingsEnv     = errors.New("has an env block that is not an object")
)

// SettingsError is a refusal about one settings file. Its message leads with
// the file's base name, so a narrow screen that cuts the tail still names it.
type SettingsError struct {
	Path string
	Err  error
}

func (e SettingsError) Error() string { return filepath.Base(e.Path) + " " + e.Err.Error() }

func (e SettingsError) Unwrap() error { return e.Err }

// SettingsEnv is what the edit screen may show of a settings file: the endpoint
// fields, and whether a token is set. The token value is deliberately absent.
type SettingsEnv struct {
	BaseURL  string
	Model    string
	HasToken bool
}

// SettingsEdit is what a save writes. An empty field keeps the file's current
// value, so an untouched token field never clears a key.
type SettingsEdit struct {
	Token   string
	BaseURL string
	Model   string
}

// ReadSettingsEnv reports the endpoint fields of the settings file at path.
// It is the only read aiacc makes of that file, and it never returns the token.
func ReadSettingsEnv(path string) (SettingsEnv, error) {
	_, env, _, _, err := loadSettings(path)
	if err != nil {
		return SettingsEnv{}, err
	}
	var out SettingsEnv
	out.BaseURL = envString(env, envBaseURL)
	out.Model = envString(env, envModel)
	out.HasToken = envString(env, envAuthToken) != ""
	return out, nil
}

// EditSettings rewrites the endpoint keys of the settings file at path. It is
// refused, with nothing written, unless the file exists, is a regular JSON
// object, and has an env that is an object (or none). The write goes through a
// temp file in the same directory and a rename, so a crash leaves the old file
// whole. The mode becomes 0600 or the file's stricter existing mode, never
// wider. A symlink is followed, so the link itself survives the edit.
func EditSettings(path string, e SettingsEdit) error {
	doc, env, real, mode, err := loadSettings(path)
	if err != nil {
		return err
	}
	for _, kv := range []struct{ key, val string }{
		{envAuthToken, e.Token}, {envBaseURL, e.BaseURL}, {envModel, e.Model},
	} {
		if kv.val == "" { // an empty field keeps the file's value
			continue
		}
		raw, err := json.Marshal(kv.val)
		if err != nil {
			return err
		}
		env[kv.key] = raw
	}
	envRaw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	doc["env"] = envRaw
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeSettings(real, mode, append(out, '\n'))
}

// loadSettings reads and validates the settings file at path. It returns the
// top-level object, the env object (empty when absent), and the real path
// after following any symlink. Errors name the file and the reason only.
func loadSettings(path string) (doc, env map[string]json.RawMessage, real string, mode fs.FileMode, err error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, "", 0, SettingsError{Path: path, Err: ErrSettingsMissing}
		}
		return nil, nil, "", 0, err
	}
	if RefuseSettingsDir(path) != nil {
		return nil, nil, "", 0, SettingsError{Path: path, Err: ErrSettingsDir}
	}
	// Owner read/write at most, and never more than the file already allowed.
	mode = info.Mode().Perm() & 0o600
	real, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, nil, "", 0, err
	}
	raw, err := os.ReadFile(real)
	if err != nil {
		return nil, nil, "", 0, err
	}
	// The decoder's own error text can quote the bytes it tripped on, which
	// may be the token, so it is replaced by a fixed reason.
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return nil, nil, "", 0, SettingsError{Path: path, Err: ErrSettingsInvalid}
	}
	env = map[string]json.RawMessage{}
	if envRaw, ok := doc["env"]; ok && string(envRaw) != "null" {
		if err := json.Unmarshal(envRaw, &env); err != nil || env == nil {
			return nil, nil, "", 0, SettingsError{Path: path, Err: ErrSettingsEnv}
		}
	}
	return doc, env, real, mode, nil
}

// envString is the string value of key in env, or "" when absent or not a string.
func envString(env map[string]json.RawMessage, key string) string {
	var s string
	if raw, ok := env[key]; ok {
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
	}
	return s
}

// writes tracks the temp files of writes in flight. A signal that ends the
// process mid-write exits without running the write's own cleanup, and the
// temp file holds the new token, so the signal path calls AbortSettingsWrites
// first. Once aborted, no new write starts.
var writes = struct {
	sync.Mutex
	temps   map[string]bool
	aborted bool
}{temps: map[string]bool{}}

// errWritesAborted refuses a write that would start after a shutdown signal.
var errWritesAborted = errors.New("settings write cancelled: aiacc is shutting down")

// ResetSettingsWritesForTest reopens the write registry after an abort. Tests
// that trigger AbortSettingsWrites register it with t.Cleanup, so the aborted
// flag never leaks into a later test. Production code never calls it.
func ResetSettingsWritesForTest() {
	writes.Lock()
	defer writes.Unlock()
	writes.aborted = false
}

// SettingsWriteWindow is a test seam: it runs between writing the temp file and
// renaming it, the window a signal must not leave a token behind in.
var SettingsWriteWindow = func() {}

// AbortSettingsWrites removes the temp file of every write in flight and refuses
// any write after it. The signal path calls it before the process exits.
func AbortSettingsWrites() {
	writes.Lock()
	defer writes.Unlock()
	writes.aborted = true
	for name := range writes.temps {
		os.Remove(name)
		delete(writes.temps, name)
	}
}

// writeSettings replaces the settings file at path with data through the shared
// atomic writer. Its temp file is registered for AbortSettingsWrites while the
// write is in flight.
func writeSettings(path string, mode fs.FileMode, data []byte) error {
	return fsutil.WriteFileAtomic(path, data, mode, fsutil.Options{
		Pattern:      ".settings-*.tmp",
		Track:        trackWrite,
		BeforeRename: SettingsWriteWindow,
	})
}

// trackWrite registers a write's temp file so an abort can remove it, or
// refuses the write if the registry is already aborted. The returned func
// forgets the temp once it is gone, so an abort never touches a name that is
// no longer ours.
func trackWrite(tmp string) (func(), error) {
	writes.Lock()
	defer writes.Unlock()
	if writes.aborted {
		return nil, errWritesAborted
	}
	writes.temps[tmp] = true
	return func() {
		writes.Lock()
		delete(writes.temps, tmp)
		writes.Unlock()
	}, nil
}
