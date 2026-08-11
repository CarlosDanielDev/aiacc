package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAggregateSumsValidUsageLines(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "p1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	lines := []string{
		`{"message":{"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":2,"cache_read_input_tokens":1}}}`,
		`{"message":{"usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":20,"cache_read_input_tokens":10}}}`,
		`{"message":{"usage":`,        // malformed JSON -> skipped
		`{"message":{"role":"user"}}`, // valid JSON, no usage -> contributes 0
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := Aggregate(root)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := Totals{Input: 110, Output: 55, CacheCreate: 22, CacheRead: 11}
	if got != want {
		t.Fatalf("totals = %+v, want %+v", got, want)
	}
	if got.Total() != 198 {
		t.Fatalf("Total() = %d, want 198", got.Total())
	}
}

func TestAggregateMissingProjectsDir(t *testing.T) {
	got, err := Aggregate(t.TempDir())
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if (got != Totals{}) {
		t.Fatalf("want zero Totals, got %+v", got)
	}
}

func TestAggregateSkipsNonJSONLFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "nested", "deep")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A .json (not .jsonl) file must be ignored entirely.
	other := `{"message":{"usage":{"input_tokens":999,"output_tokens":999}}}`
	if err := os.WriteFile(filepath.Join(dir, "ignore.json"), []byte(other), 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	jsonl := `{"message":{"usage":{"input_tokens":7,"output_tokens":3}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	got, err := Aggregate(root)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := Totals{Input: 7, Output: 3}
	if got != want {
		t.Fatalf("totals = %+v, want %+v", got, want)
	}
}

func TestAggregateHandlesLongLines(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "p1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A line well beyond bufio.Scanner's default 64KB limit.
	pad := strings.Repeat("x", 200*1024)
	line := `{"pad":"` + pad + `","message":{"usage":{"input_tokens":4,"output_tokens":6}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "big.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := Aggregate(root)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := Totals{Input: 4, Output: 6}
	if got != want {
		t.Fatalf("totals = %+v, want %+v", got, want)
	}
}
