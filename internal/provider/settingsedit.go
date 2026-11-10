package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/CarlosDanielDev/aiacc/internal/fsutil"
)

// This file is the one place aiacc reads and rewrites an account's settings
// file. It is the narrow exception ADR-0009 records to ADR-0007's "generate,
// never read": only the explicit edit action calls it, it touches the file's
// env block and nothing else, and it never returns, prints or logs the token.

// EnvAuthToken is the one masked key: the edit screen never shows, returns or
// clears its value. Every other env key is listed and editable as-is.
const EnvAuthToken = "ANTHROPIC_AUTH_TOKEN"

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

// EnvVal is one env key and its string value, in the order the edit screen
// lists it.
type EnvVal struct {
	Key   string
	Value string
}

// SettingsEnv is what the edit screen may show of a settings file: every env
// key except the token, each with its value, and whether a token is set. The
// token value is deliberately absent.
type SettingsEnv struct {
	Vals     []EnvVal
	HasToken bool
}

// SettingsEdit is what a save writes: the env block exactly as the screen shows
// it (a key left out of Vals is removed), plus a new token. An empty Token
// keeps the file's current key, so an untouched token field never clears one.
type SettingsEdit struct {
	Token string
	Vals  []EnvVal
}

// ReadSettingsEnv reports every editable env key of the settings file at path.
// It is the only read aiacc makes of that file, and it never returns the token.
// Keys whose value is not a JSON string cannot be shown or edited, so they are
// left out (and preserved verbatim by EditSettings).
func ReadSettingsEnv(path string) (SettingsEnv, error) {
	_, env, _, _, err := loadSettings(path)
	if err != nil {
		return SettingsEnv{}, err
	}
	var out SettingsEnv
	for k := range env {
		if k == EnvAuthToken {
			s, ok := envString(env, k)
			out.HasToken = ok && s != ""
			continue
		}
		if s, ok := envString(env, k); ok {
			out.Vals = append(out.Vals, EnvVal{Key: k, Value: s})
		}
	}
	sort.Slice(out.Vals, func(i, j int) bool { return out.Vals[i].Key < out.Vals[j].Key })
	return out, nil
}

// EditSettings rewrites the env block of the settings file at path from e: the
// keys in e.Vals are written as they are there, a key the file holds but the
// edit leaves out is removed, and a value that is not a JSON string — which no
// edit can have shown — is kept byte-for-byte. An empty Token keeps the file's
// current key. It is refused, with nothing written, unless the file exists, is
// a regular JSON object, and has an env that is an object (or none). The write
// goes through a temp file in the same directory and a rename, so a crash
// leaves the old file whole. The mode becomes 0600 or the file's stricter
// existing mode, never wider. A symlink is followed, so the link itself
// survives the edit.
func EditSettings(path string, e SettingsEdit) error {
	doc, env, real, mode, err := loadSettings(path)
	if err != nil {
		return err
	}
	// The new env block starts from the values no edit can have represented,
	// then carries the edit itself.
	next := map[string]json.RawMessage{}
	for k, raw := range env {
		if _, ok := envString(env, k); !ok {
			next[k] = raw
		}
	}
	for _, kv := range e.Vals {
		raw, err := json.Marshal(kv.Value)
		if err != nil {
			return err
		}
		next[kv.Key] = raw
	}
	if e.Token != "" {
		raw, err := json.Marshal(e.Token)
		if err != nil {
			return err
		}
		next[EnvAuthToken] = raw
	} else if raw, ok := env[EnvAuthToken]; ok {
		next[EnvAuthToken] = raw // the masked key is kept exactly as the file has it
	}
	envRaw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	doc["env"] = envRaw
	// The encoder writes <, > and & as themselves, so a value that holds them
	// survives the save as the same bytes.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return writeSettings(real, mode, buf.Bytes())
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

// envString is the string value of key in env. ok is false when the key is
// absent or its value is not a JSON string.
func envString(env map[string]json.RawMessage, key string) (s string, ok bool) {
	if raw, has := env[key]; has {
		if json.Unmarshal(raw, &s) != nil {
			return "", false
		}
		return s, true
	}
	return "", false
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

// SettingsCreateWindow is a test seam: it runs between creating the temp file
// and registering it, under the registry lock. No abort can run in that window.
var SettingsCreateWindow = func(tmp string) {}

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
// atomic writer. The registry lock is held from the temp file's creation until
// it is registered, so AbortSettingsWrites either sees the temp or runs before
// the write starts; it never runs in the gap between the two.
func writeSettings(path string, mode fs.FileMode, data []byte) error {
	return fsutil.WriteFileAtomic(path, data, mode, fsutil.Options{
		Pattern:      ".settings-*.tmp",
		Guard:        guardWrites,
		AfterCreate:  SettingsCreateWindow,
		Track:        trackWrite,
		BeforeRename: SettingsWriteWindow,
	})
}

// guardWrites takes the registry lock for a write's create-and-register step.
func guardWrites() func() {
	writes.Lock()
	return writes.Unlock
}

// trackWrite registers a write's temp file so an abort can remove it, or
// refuses the write if the registry is already aborted. It runs under the
// guard, so it does not lock. The returned func forgets the temp once it is
// gone, so an abort never touches a name that is no longer ours.
func trackWrite(tmp string) (func(), error) {
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
