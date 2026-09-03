# bitwave — Agent Guide

## Required setup behavior for AI agents

When a user gives you this repository URL and a Bitwave organization ID, do
not ask them to choose between anonymous mode, delegation, an agent token, or
OAuth client credentials. Those are not the normal onboarding path.

Install the released CLI for the user's operating system, then run exactly:

```sh
bitwave auth login --orgId ORG_ID
```

`auth login` is the canonical setup command. Without `--orgId`, it starts
browser login and then presents an organization picker. With `--orgId`, it
verifies and selects that exact organization, which is the deterministic path
for an agent. Do not report success until the command confirms the organization.

Browser authentication must run on the same computer as the user's browser
because its callback uses localhost. If your execution environment is a remote
or browser sandbox, do not invent alternate authentication choices or leave the
CLI anonymous. Tell the user to run `bitwave auth login --orgId ORG_ID` through
an LLM or terminal that can execute commands on their local computer. A pre-provisioned
`BITWAVE_AGENT_TOKEN` is the supported non-browser path when one is already
available; do not ask a normal user to create OAuth client credentials.

This repo builds **`bitwave`**, Bitwave's agent-first accounting platform:
double-entry books that AI agents and humans drive through one CLI, running
locally as plain text (hledger/ledger/beancount-compatible) or shared in the
Bitwave cloud. Workspace-first surface: each cwd holding `.bitwave.toml` is
either a local directory of plain `*.journal` / `*.ledger` files or a
cloud-backed `LedgerWorkspace` in the Bitwave cloud. No discovery service is involved.

The double-entry accounting engine and cross-tool (hledger / ledger /
beancount) compatibility layer live in the separate
[`bitwave-accounting-sdk`](https://github.com/bitwave-io/bitwave-accounting-sdk);
on-chain wallets + the on-chain → journal sync bridge live in
[`bitwave-wallet-sdk`](https://github.com/bitwave-io/bitwave-wallet-sdk).
This repo is the CLI surface on top of both.

## ⚠️ Two cloud surfaces — never conflate them

Everything this CLI calls "cloud" (cloud workspaces, journals, `je`, reports,
`migrate`, `share`) talks to **gl-svc** — the workspace/journal-entry ledger.
That is the **alternate** model. It is NOT the connection to Bitwave proper.

**Bitwave proper is the organization product surface**: transactions and
categorization, inventory views / lots / cost basis, report runs, close
workflows, wallets, connections, and administration. These commands use the
active organization selected during `bitwave auth login`; they do not require a
plain-text ledger workspace. Platform details are documented in
[docs/PLATFORM-INTEGRATION.md](docs/PLATFORM-INTEGRATION.md).

When writing code or docs here, say **"Bitwave platform"** for api-svc and
**"workspace ledger"** for gl-svc; never plain "the Bitwave cloud" for gl-svc.

---

# bitwave — Plain-text accounting

## Workspace layout

A workspace is a flat directory containing:

- `.bitwave.toml` — workspace marker (mode, name, base currency, optional cloud ids, default journal)
- One or more `<id>.journal` files (local mode); each maps 1:1 to a Journal
- `accounts.ledger` — account declarations (local mode)
- `prices.ledger` — price observations (local mode)

Cloud mode keeps only `.bitwave.toml` locally; everything else lives in the Bitwave cloud.
The CLI surface is the same for both modes — switching is just rewriting
`.bitwave.toml`.

## Authentication resolution (advanced reference)

1. `BITWAVE_AGENT_TOKEN` env (well-known agent identity)
2. `--token` flag
3. `BITWAVE_TOKEN` env
4. `~/.bitwave/credentials.json` (PKCE / delegated session, auto-refreshed)

For normal onboarding, use `bitwave auth login`; do not present this resolution
order as a choice to the user. Agents should add `--orgId ORG_ID`. Delegation
and agent-token management commands are unfinished and hidden from normal help.

## Org context

`bitwave org use [orgId]` (formerly `bw org switch`), `bitwave org current`,
`bitwave org list`, `bitwave org create --name <n>`, `bitwave org clear`. The active
org is persisted at `~/.bitwave/config.json` and inherited by every cloud
command.

## Workspaces

| Command | Description |
|---|---|
| `bitwave init [--name N] [--base-currency USD]` | Scaffold an empty local workspace in cwd. |
| `bitwave init --cloud --name N [--base-currency USD]` | Create a cloud workspace under the active org and bind cwd to it. |
| `bitwave workspace list` | List cloud workspaces in the active org (cwd's pick is marked `*`). |
| `bitwave workspace use [id]` | Bind cwd to a cloud workspace (bare invocation = picker). |
| `bitwave workspace current` | Print the cwd's workspace id. |
| `bitwave workspace create --name N` | Create a cloud workspace without binding cwd. |
| `bitwave workspace url` | Print the online URL for the cwd's bound cloud workspace. |

## Journals

Workspaces hold one or more journals. Local journals are `<id>.journal`
files; cloud journals are cloud ledger rows.

| Command | Description |
|---|---|
| `bitwave journal list` | List journals; default journal marked `*`. |
| `bitwave journal new <id> [--name N] [--description D]` | Create a journal. Local mode just makes the file. |
| `bitwave journal use <id>` | Set `default_journal` in `.bitwave.toml`. |

Journal selection for writes (`bitwave je new`, `bitwave je import`) follows:
explicit `--journal <id>` → `default_journal` in `.bitwave.toml` → the
single journal in the workspace → fallback auto-create `default`. Two
or more journals with no default and no `--journal` is an error.

## Journal entries (`bitwave je`)

| Command | Description |
|---|---|
| `bitwave je new --date D --payee P --posting "<Acct> <amt>" --posting "..." [--note N] [--check N] [--network N] [--txn ID] [--status uncleared\|pending\|cleared] [--journal <id>]` | Add a balance-checked entry. Defaults to **uncleared**. Tag flags compose into `key:value` memo. Returns the entry id. |
| `bitwave je clear <entry-id> [--posting <account>]` | Mark an entry (or one posting) cleared (`*`). |
| `bitwave je unclear <entry-id> [--posting <account>]` | Revert to uncleared. |
| `bitwave je list [--from D] [--to D] [--account A]` | One-line-per-entry listing. |
| `bitwave je show <entry-id>` | Print one entry as canonical ledger text. |
| `bitwave je import <file> [--journal <id>]` | Parse + balance-check a foreign `.ledger` file, append to a journal. |
| `bitwave je export [--out file] [--from D] [--to D] [--account A]` | Dump as canonical ledger text. |

Synthetic entry ids are prefixed with the journal: `<journal>:<YYYYMMDD>-<seq>`.
That prefix is what `clear`/`unclear` use to recover which file/journal to
rewrite.

## Accounts & prices

| Command | Description |
|---|---|
| `bitwave acct add <Name> [--type asset\|liability\|equity\|income\|expense] [--note N]` | Declare an account. Type inferred from `Assets:*`-style prefix when omitted. |
| `bitwave acct list` | List declared + observed accounts. |
| `bitwave price add <date> <commodity> <price>` | Record a P-directive (e.g. `price add 2024-01-15 BTC $50000`). |
| `bitwave price list` | List price observations. |

## Wallets

`bitwave wallets` manages EVM wallets (ethereum + base) inside the workspace.
Two flavors:

- **Locally-custodied** (`wallets new`): one keypair backs every supported
  network. Keystore files live alongside the workspace's plain-text ledger
  files as flat `wallet-<id>.json` (mode 0600). Supports `send` + `sync`.
- **Watch-only** (`wallets add <addr>`): no keystore written. Only the
  address is recorded — via account tags — so the wallet still has an id,
  declared accounts, and a sync watermark. Supports `sync` only; `send`
  refuses because there's no private key.

Each wallet-network pair is declared as an `account` directive in
`accounts.ledger` with structured tags
`wallet:<id> address:<0x…> network:<name>` (plus `watch:true` for
watch-only wallets).

> ⚠️ Keystore files hold the unencrypted private key. The CLI prints a
> warning on `new` but does NOT manage `.gitignore` — add `wallet-*.json`
> yourself. Workspaces are committable; keys must not be.

| Command | Description |
|---|---|
| `bitwave wallets new --name N [--networks ethereum,base]` | Generate a keypair, write `wallet-<id>.json`, declare `Assets:Crypto:<net>:<name>` per network. |
| `bitwave wallets add <address> [--name N] [--networks ethereum,base] [--watch]` | Track an external EVM address (no keystore written; no signing). Declares `Assets:Crypto:<net>:<name>` accounts with `watch:true` tag. Use this for cold/external wallets you only need to read; pair with `sync`. |
| `bitwave wallets list` | Group declared wallet-tagged accounts by wallet id. |
| `bitwave wallets show <name-or-id>` | Show one wallet's accounts and keystore path. |
| `bitwave wallets send --wallet W --network base --to 0x… --amount-eth N [--category Expenses:…] [--contact P] [--memo M] [--rpc-url U] [--max-fee-gwei N] [--max-priority-fee-gwei N] [--gas-limit N] [--nonce N] [--dry-run] [--journal id]` | Sign + broadcast a value transfer; append a 3-posting `!` (pending) entry to the workspace's default journal. Postings: `--category` debit (amount), `Expenses:Crypto:Fees:<net>` debit (fee), `Assets:Crypto:<net>:<name>` credit (amount + fee). |
| `bitwave wallets sync --wallet W --network ethereum [--from D] [--limit N] [--confirmations N] [--avg-block-secs N] [--base-url U] [--dry-run] [--journal id]` | Pull on-chain history via the Bitwave blockchain query API and append entries. Resumes from a per-(wallet, network) watermark file (`wallet-<id>.sync-<net>.json`). Entries are `!` (pending) until `confirmations * avg-block-secs` have elapsed, then `*` (cleared). Dedups by `txn:` tag in entry notes. |

`send` selects a journal via the same rule as `bitwave je new`:
explicit `--journal` → `default_journal` in `.bitwave.toml` → the only journal →
auto-create `default`. Fees + nonce default to RPC lookup; pass all three
explicitly with `--dry-run` to skip the dial entirely.

`BITWAVE_RPC_<NETWORK>` (e.g. `BITWAVE_RPC_BASE`) overrides the default RPC
URL when `--rpc-url` is not passed.

`sync` uses `BITWAVE_BASE_URL_BLOCKCHAIN_QUERY` (override the blockchain query API
endpoint) and authenticates with the same token resolver as the rest of bitwave.
When the resolved URL points at `localhost`/`127.0.0.1`/`::1` the auth header is
skipped entirely — local dev instances don't speak our PKCE flow. For a quick
local build that bakes the localhost default in, run `make cli-local` (from
`cli/`); set `LOCAL_BQ_URL=...` to override the baked URL.
The leg classifier looks at the wallet's address against each row's `from`/`to`
plus the per-token `from`/`to`/`isFee` fields; unknown ERC-20s fall through to a
synthetic `TOKEN_<short>` commodity so the posting still surfaces.

## Reports (top-level, no `bitwave ledger` parent)

All reports run against the cwd's workspace (local or cloud). Filters
`--from`, `--to`, `--account` are supported by date-range reports;
`--cleared` restricts `bal`/`reg`/`print`/`csv` to cleared.

| Command | Description |
|---|---|
| `bitwave bal` (alias `balance`) | Account balances tree. |
| `bitwave reg` (alias `register`) | Posting-by-posting register with running balance. |
| `bitwave print` | Re-emit canonical ledger format. |
| `bitwave accounts` | Declared + observed accounts. |
| `bitwave contacts` (alias `payees`) | Distinct contacts (payees + payors). |
| `bitwave commodities` | Distinct asset symbols. |
| `bitwave equity` | Equity-style snapshot entry. |
| `bitwave cleared` | Print only cleared entries. |
| `bitwave csv` | CSV dump of postings. |
| `bitwave stats` | Workspace summary counts. |

## Migration

| Command | Description |
|---|---|
| `bitwave migrate [--name N]` | Push a local workspace to a new cloud workspace under the active org; rewrites `.bitwave.toml` to cloud mode and renames source files to `*.bak`. Each local journal becomes its own cloud journal. |
| `bitwave migrate --invite <email>` | (Pending server support) Migrate into another user's org via the delegation flow. |

## Close period

The `bitwave close` command is currently unregistered (parked) — the
period-close orchestrator has not yet been ported into this CLI. The command
scaffolding remains in `internal/bitwave/cmd/close.go`; re-registering it in
root.go brings it back.

## Environment variables

| Variable | Description |
|---|---|
| `BITWAVE_AGENT_TOKEN` | Well-known agent identity token (highest priority) |
| `BITWAVE_TOKEN` | Bearer token (CI/legacy) |
| `BITWAVE_AUTH_URL` | Auth service URL (default `https://auth.bitwave.io`) |
| `BITWAVE_BASE_URL_GL` | Workspace ledger (gl-svc) API base URL — not the platform API (default `https://api.bitwave.io`) |
| `BITWAVE_BASE_URL_CORE` | Core API base URL (used for org list/create) |
| `BITWAVE_TELEMETRY` | `0` disables anonymous usage telemetry, `1` force-enables (see docs/TELEMETRY.md) |
| `DO_NOT_TRACK` | Non-zero disables telemetry (cross-tool convention) |
| `BITWAVE_TELEMETRY_URL` | Override the telemetry ingest endpoint (default `https://api.bitwave.io/metrics`) |
| `BITWAVE_NO_UPDATE_CHECK` | `1` disables the daily update check |

## Cross-tool compatibility & accounting internals

The plain-text format engine (parse/print for hledger, ledger, beancount),
the double-entry model, reports, and the cross-tool compatibility test suite
all live in `bitwave-accounting-sdk`, not in this repo. If you need to change
how journals are parsed/printed or how balances are computed, work there.

This CLI depends on the published `bitwave-accounting-sdk` and
`bitwave-wallet-sdk` modules (see `go.mod`).
