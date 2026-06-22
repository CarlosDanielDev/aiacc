# Security policy

## Scope

aiacc selects which config directory an AI CLI uses. It **never reads, writes,
parses, or transmits credentials or tokens** — those live inside each provider's
own config directory, which aiacc only points an environment variable at. The
usage reporter reads token *counts* from local session logs; it does not read
message contents or secrets.

## Reporting a vulnerability

Email the project lead (see [MAINTAINERS](MAINTAINERS)) or open a private
security advisory via GitHub's "Report a vulnerability" button. Do not file a
public issue for a vulnerability. Expect an acknowledgement within a few days.
