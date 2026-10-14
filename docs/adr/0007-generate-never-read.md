# 0007 — aiacc may generate a settings skeleton it never reads

Status: Accepted

## Context

ADR-0006 let an account carry a settings file whose path aiacc stores and whose
contents it never touches. Hand-writing that file is still the user's problem:
the `env` block (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`,
`ANTHROPIC_MODEL`) has a shape every endpoint setup repeats, and getting it
wrong fails silently at launch. Letting aiacc collect the key and write the
whole file would fix that — and would put secret material through aiacc's
process, superseding the boundary ADR-0006 just drew.

## Decision

- aiacc ships endpoint presets (today `glm` and `custom`, on the `claude`
  preset — only it has a settings flag) and a generator that writes a settings
  *skeleton*: the `env` block with the endpoint's base URL and model fields,
  and an obvious `PASTE-YOUR-KEY-HERE` placeholder where the key goes.
- **Generation is write-once.** The file is created with mode `0600`, and
  aiacc never opens it again — it does not read, display, validate, edit or
  rotate it. The user pastes their key into the file themselves.
- An existing file is never overwritten: generation refuses and hands back the
  path it refused to touch.
- ADR-0006's "stores the path only" stands unchanged. This record extends the
  boundary in one direction: aiacc may *create* the file, and creation is not
  reading.

## Consequences

- A new user gets a working settings file from a template keyword, without
  aiacc ever holding key material — the key goes from the user's clipboard to
  their editor, never through aiacc.
- The placeholder token can reach a launcher if the user skips the paste step;
  the endpoint then fails auth visibly, which is the honest failure.
- Endpoint presets are data on the existing `Preset` struct, so adding one (a
  proxy, another vendor) is a table entry, not a code path.
