# 0009 — The edit action may read and rewrite an account's endpoint keys

Status: Accepted

## Context

ADR-0007 has aiacc generate a settings skeleton once and never open it again,
so the key the user pastes in an editor never passes through aiacc. The cost is
that changing an endpoint or rotating a key means hand-editing JSON. Regenerating
the whole file would destroy any field the user added. A read-merge-write is the
only way to change one key without that loss, and it reads a file that may hold
a key.

## Decision

- The picker's `e` action (edit) reads the account's settings file and rewrites
  only `env.ANTHROPIC_AUTH_TOKEN`, `env.ANTHROPIC_BASE_URL` and
  `env.ANTHROPIC_MODEL`. Every other key is kept as parsed.
- It runs only on that explicit action. Generation, add, rename, launch, doctor
  and handoff still never open the file.
- The token is never displayed, logged, prefilled or put in an error: the token
  field renders as dots, and an empty field keeps the key already in the file.
  Refusals name the file and the reason, never its contents.
- A save refuses a missing file, a directory, invalid JSON, or an `env` that is
  not an object, and writes nothing. It goes through a temp file in the same
  directory and a rename, with mode 0600 or the file's stricter existing mode,
  never wider. A symlink is followed, so the link survives.

## Consequences

- This narrows ADR-0007's "never reads" for one action only. ADR-0007 stands as
  written: generation is still write-once and create-only.
- ADR-0006's "editing endpoint or key values is out of scope" no longer holds
  for the settings file. `config.toml` still holds no secrets.
- Key order in a rewritten file can change (JSON objects are re-marshalled). Keys
  and values are preserved.
