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
- [0002](0002-generic-provider-driver.md) — Generic env-var provider driver
- [0003](0003-shell-eval-switching.md) — Shell switching via eval hook
- [0004](0004-plan-quota-best-effort.md) — Plan/rate-limit reporting is best-effort
