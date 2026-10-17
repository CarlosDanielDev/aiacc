package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fixtureAsset is the name install.sh builds for linux/amd64, per
// .goreleaser.yaml's name_template.
const fixtureAsset = "aiacc_linux_amd64.tar.gz"

// fakeNetSh stands in for curl and wget. It understands the argument forms
// install.sh uses (curl -fsSL URL -o FILE, wget -qO FILE URL, wget -qO- URL)
// and serves $FAKE_SERVE/<last path segment of URL>, or latest.json for the
// releases/latest API call. A missing file exits 22, like curl -f.
const fakeNetSh = `#!/bin/sh
url=""; out=""
while [ $# -gt 0 ]; do
	case "$1" in
		-o | -qO) out="$2"; shift ;;
		-qO-) out="" ;;
		-*) ;;
		*) url="$1" ;;
	esac
	shift
done
case "$url" in
	*releases/latest) name=latest.json ;;
	*) name="${url##*/}" ;;
esac
src="$FAKE_SERVE/$name"
[ -f "$src" ] || exit 22
if [ -n "$out" ]; then cp "$src" "$out"; else cat "$src"; fi
`

const fakeUnameSh = `#!/bin/sh
case "$1" in
	-s) echo Linux ;;
	*) echo x86_64 ;;
esac
`

func TestInstallScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is POSIX sh")
	}

	archive := fakeArchive(t)
	good := sha256Hex(archive) + "  " + fixtureAsset + "\n"
	otherAssetOnly := strings.Repeat("0", 64) + "  aiacc_darwin_arm64.tar.gz\n"
	mismatch := strings.Repeat("a", 64) + "  " + fixtureAsset + "\n"

	cases := []struct {
		name     string
		sums     *string // nil: checksums.txt is not published for the tag
		withCurl bool
		withWget bool
		noSha    bool   // withhold both sha256sum and shasum
		wantErr  string // empty: install must succeed
	}{
		{name: "curl verifies checksum and installs", sums: &good, withCurl: true},
		{name: "curl rejects checksum mismatch", sums: &mismatch, withCurl: true, wantErr: "checksum mismatch"},
		{name: "curl refuses when checksums.txt is missing", sums: nil, withCurl: true, wantErr: "failed to download https://github.com/CarlosDanielDev/aiacc/releases/download/v9.9.9/checksums.txt"},
		{name: "curl refuses when asset has no checksum line", sums: &otherAssetOnly, withCurl: true, wantErr: "no checksum for"},
		{name: "wget fallback verifies and installs", sums: &good, withWget: true},
		{name: "wget fallback rejects checksum mismatch", sums: &mismatch, withWget: true, wantErr: "checksum mismatch"},
		{name: "fails when neither curl nor wget exists", sums: &good, wantErr: "curl or wget is required"},
		{name: "fails when no sha256 tool exists", sums: &good, withCurl: true, noSha: true, wantErr: "sha256sum or shasum is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serve := t.TempDir()
			writeFile(t, filepath.Join(serve, "latest.json"), `{"tag_name": "v9.9.9"}`)
			writeFile(t, filepath.Join(serve, fixtureAsset), string(archive))
			if tc.sums != nil {
				writeFile(t, filepath.Join(serve, "checksums.txt"), *tc.sums)
			}

			stubs := t.TempDir()
			writeExec(t, filepath.Join(stubs, "uname"), fakeUnameSh)
			if tc.withCurl {
				writeExec(t, filepath.Join(stubs, "curl"), fakeNetSh)
			}
			if tc.withWget {
				writeExec(t, filepath.Join(stubs, "wget"), fakeNetSh)
			}

			binDir := filepath.Join(t.TempDir(), "bin")
			cmd := exec.Command("sh", "install.sh")
			cmd.Env = []string{
				"PATH=" + stubs + string(os.PathListSeparator) + toolsDir(t, !tc.noSha),
				"HOME=" + t.TempDir(),
				"BIN_DIR=" + binDir,
				"FAKE_SERVE=" + serve,
			}
			out, err := cmd.CombinedOutput()
			dest := filepath.Join(binDir, "aiacc")
			_, statErr := os.Stat(dest)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("install failed: %v\n%s", err, out)
				}
				if statErr != nil {
					t.Fatalf("binary not installed at %s: %v", dest, statErr)
				}
				return
			}
			if err == nil {
				t.Fatalf("install succeeded, want failure containing %q\n%s", tc.wantErr, out)
			}
			if !strings.Contains(string(out), tc.wantErr) {
				t.Fatalf("output lacks %q:\n%s", tc.wantErr, out)
			}
			if statErr == nil {
				t.Fatalf("binary installed at %s despite failure", dest)
			}
		})
	}
}

// toolsDir links the real POSIX tools install.sh needs, but never curl or
// wget, so a case can withhold them from PATH. withSha controls whether the
// sha256sum and shasum digest tools are linked.
func toolsDir(t *testing.T, withSha bool) string {
	t.Helper()
	dir := t.TempDir()
	names := []string{
		"tar", "gzip", "grep", "head", "sed", "awk", "cut", "mktemp", "mkdir",
		"mv", "cp", "chmod", "rm", "cat", "dirname", "basename",
	}
	if withSha {
		names = append(names, "sha256sum", "shasum")
	}
	for _, name := range names {
		p, err := exec.LookPath(name)
		if err != nil {
			continue // sha256sum and shasum: install.sh needs one of them
		}
		if err := os.Symlink(p, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fakeArchive(t *testing.T) []byte {
	t.Helper()
	body := []byte("#!/bin/sh\necho fake aiacc\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "aiacc", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
