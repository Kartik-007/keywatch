# keywatch — Product Spec (v0.1)

> Working name. `KeyWatch` already exists on GitHub as an unrelated secret sniffer, so pick the final name before the repo goes public.

## One line

A free, open-source CLI that finds every AI API key you've deployed, shows where each one lives and what it's spending, and pings you when spend looks wrong.

## Why now

On 2026-10-21 Railway disclosed that attackers accessed customer environment variables, and they appear to have been after AI inference keys. A stolen AI key works like a stolen credit card for compute. Teams running agents have these keys spread across deploy platforms, CI and `.env` files, and no single tool shows all of them in one place.

## What it does that existing tools don't

| Tool | Sees | Misses |
|---|---|---|
| gitleaks / TruffleHog | Keys in code | Deploy platforms, spend |
| OpenAI / Anthropic console | Keys and spend | Where each key is deployed |
| LiteLLM / Portkey | Spend through the gateway | Keys that bypass it |
| Infisical / Doppler | Secrets already stored in them | Secrets that aren't |

**keywatch joins these into one row per key:** where it lives, how old it is, what it spends, and what's risky about it.

## Who it's for

A small team (1–20 people) running AI features or agents, deploying on Railway, Vercel and GitHub, with no security person. They want an answer to "am I exposed?" in under a minute, and they won't fill in a config file to get it.

## Principles

1. **Value before setup.** The first run needs no account, no config and no keys.
2. **Ask for access when it's useful.** Request each permission at the moment the tool can show what it unlocks.
3. **Read-only by default.** Writes always show a plan first and need a `y`.
4. **Quiet unless wrong.** No dashboards and no digests.
5. **Trust is the product.** No telemetry, never print a full key, every run ends with a receipt, and the source is small enough to audit.

## Access levels

Every run states which level it's operating at.

| Level | Can do | Turned on by |
|---|---|---|
| Look | Scan local files. No network. | Default |
| Read platforms | Read env vars from Railway, Vercel, GitHub | One y/n prompt; reuses existing CLI logins |
| Read spend | Per-key usage from providers | `keywatch connect <provider>` |
| Change | Disable keys (rotate in v0.2) | Confirmed every time, plan shown first |

## v0.1 scope (ship this)

### 1. `keywatch` (scan)

- Scans the current folder, including git history for committed keys, using gitleaks' detection rules as a Go library.
- If `railway`, `vercel` or `gh` logins exist on the machine, asks once (y/n) to read from them.
  - **Railway:** reads variable values for every project, environment and service.
  - **Vercel:** reads env var values. "Sensitive" vars can't be decrypted, so they're listed by name only.
  - **GitHub:** Actions secrets are write-only through the API. keywatch lists secret *names* and matches them by name heuristics (e.g. `OPENAI_API_KEY`), and says plainly that the value couldn't be checked.
- Groups results **by key, not by file.** Keys are fingerprinted as SHA-256 locally and shown as `sk-…a91f`.
- Prints up to 5 findings ranked by severity, each with its next command. Ends with a receipt: "Nothing was changed. Read from: …".

**Findings in v0.1:**

| ID | Finding | Severity |
|---|---|---|
| F1 | Key committed to git (current tree or history) | High |
| F2 | Same key in 2+ services (agents sharing a key) | Medium |
| F3 | Key the provider says is active but found nowhere (forgotten, or living somewhere you can't see) | Medium |
| F4 | No spending cap on the provider project/workspace | Medium (needs connect) |
| F5 | Key older than 90 days | Low (needs connect) |

### 2. `keywatch connect openai|anthropic`

- Opens the provider's admin-key page and takes the key from a hidden prompt.
- Stores it in the macOS Keychain or Linux Secret Service, never in a file.
- Confirms immediately with real data: "Last 7 days: $212 across 4 keys."
- If the provider supports read-only admin keys, tells the user to create one.

**Provider facts (verified 2026-10-21 against the official API docs and OpenAI's OpenAPI spec):**

- **OpenAI:**
  - `GET /v1/organization/projects/{id}/api_keys` returns `redacted_value`, `created_at`, `last_used_at` and `owner` for each key.
  - `GET /v1/organization/costs?group_by=api_key_id` gives **real USD per key**, but only per day (`1d`).
  - For hourly spikes, `GET /v1/organization/usage/completions?group_by=api_key_id&bucket_width=1h` gives tokens per key.
  - `DELETE` on the key is irreversible. The `POST` update endpoint only changes scopes, so it can't be used to deactivate a key.
- **Anthropic:**
  - `GET /v1/organizations/api_keys` returns `partial_key_hint`, `status`, `created_at` and `scope.workspace_id`. Key status can be `active` or `inactive`.
  - `/v1/organizations/usage_report/messages` filters and groups by API key in `1m`, `1h` or `1d` buckets.
  - `/v1/organizations/cost_report` is daily only and groups by workspace or description, **not by key**. So cost per key = tokens × a bundled price table, shown with "≈".
  - Data typically appears within 5 minutes.
  - **The Admin API is not available on individual accounts.** It needs an organization. Say so in `connect`.
- **Matching a found key to a provider key:** compare the prefix and last 4 characters against `redacted_value` (OpenAI) or `partial_key_hint` (Anthropic). No secret ever leaves the machine.
- Alerts are **"within the hour"**, never "instant", in all copy.

### 3. `keywatch status`

Prints one table per key: label, provider, locations, age, spend today and over 7 days, and the normal daily range. Labels are auto-derived from location (e.g. `support-bot`); change one with `keywatch label <id> <name>`.

### 4. `keywatch watch`

- Asks one question: "What's a scary amount for one day of AI spend?" (default $50).
- Shows the workflow file it will write, `.github/workflows/keywatch.yml`, plus the encrypted repo secrets it will set through `gh`, then asks y/N.
- **The job is stateless:** each hourly run pulls the last 14 days of usage and computes each key's normal range itself, so there's nothing to store between runs.
- **Alert rule:** a key's last hour exceeds max(3× its 14-day p95 hourly spend, $5), **or** org spend today crosses the scary number.
- **Default channel:** the job fails, and GitHub emails the user, so no setup is needed. Optional: `keywatch alerts slack|discord <webhook>`.
- **Alert format:** what happened, which key (label and locations), normal vs now, and two commands (`ack` and `disable`), including what disabling will break.

### 5. `keywatch ack <key> <duration>`

Sets a repo variable `KEYWATCH_ACK_<id>=<until>` through `gh`. The job reads it and stays quiet for that key until it expires.

### 6. `keywatch disable <key>`

Shows which locations will stop working, asks y/N, then calls the provider.

- **Anthropic:** sets the key to inactive (reversible; `keywatch enable` undoes it).
- **OpenAI:** deletes the key. Irreversible, so the user must type the last 4 characters to confirm.

### 7. Housekeeping

- `keywatch check`: exits non-zero if F1 is found. For CI and pre-commit.
- `keywatch ignore <finding> --reason "…"`: writes to `.keywatch.toml` in the repo so the team shares the decision.
- `keywatch forget [provider]`: removes Keychain entries and local state.
- Global flags: `--json`, `--verbose` (logs every network call).

## v0.2 (next, not now)

- `keywatch rotate <key>`: preview the plan, take a new key and test it, write it everywhere the old one lives, keep the old key alive until it's had 0 use for 1h, then disable it.
- `keywatch lockdown <platform>`: on a breach day, walk through every key stored on that platform, most-used first, and resume where you stopped.
- More providers (Google, Groq, OpenRouter); Fly, Render, AWS.

## Out of scope

Web dashboard, accounts, telemetry, a hosted service, real-time interception (that needs a gateway; point users to LiteLLM).

## Tech

- **Go**, single static binary. AI key patterns are a small built-in detector (OpenAI and Anthropic only), not gitleaks, so the dependency tree stays small enough to audit. Uses `cobra` for commands, `lipgloss` for output, `go-keyring` for Keychain storage.
- **Distribution:** GitHub Releases via goreleaser, plus `brew install <user>/tap/keywatch`. No npm.
- **Tests:** a fixture repo with obviously fake keys, and recorded provider/platform responses for offline tests.
- **Demo mode:** `keywatch --demo` runs against the fixtures. Used for README visuals and labeled as demo data.

## Success for v0.1

- From `brew install` to the first useful screen takes under 60 seconds with zero config.
- Kartik runs it on his own projects and gets real numbers for the launch tweet.
- 3 people outside Kartik's circle run it and report a finding they didn't know about.

## Open questions

1. Final name (`keywatch` is taken on GitHub; it's free as a brew formula).
2. Make the repo public immediately, or keep it private until v0.1 is tagged?
3. Does the price table bundled in the binary go stale fast enough to need a fetch? (v0.1: bundle it, show "≈" on costs.)
