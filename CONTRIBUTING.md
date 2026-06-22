# Contributing to aiacc

aiacc takes its contribution discipline from FFmpeg — small reviewable changes,
clear authorship, and a documented rationale for every non-obvious decision —
adapted to a GitHub pull-request workflow.

## Ground rules

1. **One logical change per pull request.** A PR maps to one issue. If you find
   a second thing to fix, file a second issue.
2. **Discuss before large work.** Open or claim an issue before writing a big
   patch, so effort isn't wasted on an approach we can't merge.
3. **Every non-obvious decision gets an ADR.** See [docs/adr/](docs/adr/). If
   your change alters architecture, add or update a record in the same PR.
4. **Tests with behavior.** Any non-trivial logic ships with a Go test that
   fails if the logic breaks. CI runs `go vet`, `gofmt -l`, and `go test`.

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org). Subject line
imperative, lower-case, ≤ 72 chars:

```
feat(usage): parse Claude session logs into per-account token totals

Longer body explaining *why*, wrapped at 72 columns. Reference issues.

Refs: #11
Signed-off-by: Your Name <you@example.com>
```

Allowed types: `feat`, `fix`, `docs`, `refactor`, `test`, `ci`, `build`,
`chore`. Scope is the package or surface (`config`, `provider`, `shell`,
`usage`, `cmd`, `ci`).

## Developer Certificate of Origin

Every commit must be signed off with `Signed-off-by: Name <email>` (add `-s` to
`git commit`). This certifies you wrote the patch or have the right to submit it
under the project's MIT license — the [DCO](https://developercertificate.org/).

## Code style

- `gofmt` is law. CI rejects unformatted code.
- Keep packages small and single-purpose; mirror the layout in the design doc.
- Prefer the standard library. A new dependency needs a one-line justification
  in the PR and, if it shapes the architecture, an ADR.

## Review flow

1. Fork, branch from `main` (`feat/usage-parser`).
2. Open a PR using the template; link the issue with `Closes #N`.
3. A maintainer for the touched area (see [MAINTAINERS](MAINTAINERS)) reviews.
4. Green CI + one maintainer approval = merge (squash).

## Where to start

Issues labelled `good-first-issue` are scoped and self-contained. The
[milestones](https://github.com/CarlosDanielDev/aiacc/milestones) show the
dependency order — pick anything whose `Blocked By` issues are already closed.
