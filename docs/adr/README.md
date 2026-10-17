# Architecture Decision Records

Every non-obvious decision in aiacc is recorded here, FFmpeg-style: a short,
immutable note of *what* was decided and *why*, so contributors don't re-litigate
settled questions or break invariants they didn't know existed.

## Format

One file per decision: `NNNN-short-slug.md`. Use [MADR](https://adr.github.io/madr/)-lite:
Context → Decision → Consequences. Keep it under a page.

## Lifecycle

A record is **Accepted** when merged. It is never edited to change the decision;
instead, a later record **supersedes** it and both link each other. Status values:
`Proposed`, `Accepted`, `Superseded by NNNN`.

## Index

- [0001](0001-language-go.md) — Implementation language: Go
- [0002](0002-generic-provider-driver.md) — Generic env-var provider driver (Superseded by 0008)
- [0003](0003-shell-eval-switching.md) — Shell switching via eval hook (Superseded by 0008)
- [0004](0004-plan-quota-best-effort.md) — Plan/rate-limit reporting is best-effort
- [0005](0005-doctor-diagnoses-never-repairs.md) — Launchers resolve by name; `doctor` diagnoses, never repairs
- [0006](0006-account-settings-path-not-secret.md) — An account can carry a settings file; aiacc stores its path, never its contents
- [0007](0007-generate-never-read.md) — aiacc may generate a settings skeleton it never reads
- [0008](0008-launchers-replace-eval-hook.md) — Launchers are the default switching mechanism; presets carry provider capabilities
- [0009](0009-edit-settings-key.md) — The edit action may read and rewrite an account's endpoint keys (narrows 0007)
- [0010](0010-retire-shell-init.md) — Retire shell-init softly: a hidden stub that still evals cleanly (refines 0008)
