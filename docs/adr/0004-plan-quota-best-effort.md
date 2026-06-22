# 0004 — Plan / rate-limit reporting is best-effort

Status: Accepted

## Context

Users want to see how much of their subscription plan / rate limit each account
has consumed. There is no official, stable, local or API source for "remaining
quota on a subscription plan" for these CLIs. Scraping web dashboards is fragile
and an authentication/ToS hazard.

## Decision

Report usage from sources we actually control, and be explicit about the gap:

- **Token usage** is computed from local session logs (Claude Code writes JSONL
  under `<config_dir>/projects/**`). This is real and accurate for tokens.
- **Plan/quota** is **manual and optional**: a user may set `quota = <number>` on
  an account; `aiacc usage` then shows `used / quota`. With no quota set, only the
  raw used figure is shown.
- We do **not** scrape dashboards or hit unofficial endpoints.

## Consequences

- No surprise breakage when a vendor changes a private endpoint or web page.
- The "remaining plan %" is only as good as the user's manually entered quota — UI
  must label it as user-provided, not authoritative.
- If a vendor ships an official quota API later, add a provider capability and a
  superseding ADR.
