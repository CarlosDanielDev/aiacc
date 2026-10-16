# 0001 — Implementation language: Go

Status: Accepted

## Context

aiacc is a CLI that reads config, parses local JSONL session logs, emits shell
snippets, and will later grow a TUI dashboard. It must ship as an easy-to-install
artifact for non-Go users and be approachable for open-source contributors.

Candidates considered: Go, Rust, POSIX shell.

## Decision

Use **Go**.

- Single static binary, trivial cross-compilation (`GOOS`/`GOARCH`), no runtime
  dependency for end users.
- `encoding/json` makes streaming JSONL parsing simple; the workload is I/O-bound,
  so Rust's performance edge is irrelevant here.
- `bubbletea` is the gentlest path to the planned TUI.
- Lower contribution barrier and faster iteration than Rust for a tool this size;
  far more maintainable than POSIX shell once log parsing and a TUI are involved.

## Consequences

- We accept Go's larger binaries and GC over Rust — a non-issue for this workload.
- Shell is still used, but only as *generated output* (the switching hook), never
  as the implementation language.
- Distribution will lean on GoReleaser (see the v0.5.0 milestone).
