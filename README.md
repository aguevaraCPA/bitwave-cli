# bitwave — agent-first accounting, local or cloud

> **Using an AI assistant?** Give it this repository URL and your Bitwave
> organization ID. The assistant should install the released CLI and run
> `bitwave auth login --orgId ORG_ID`. That command handles browser login,
> verifies the requested organization, and saves it as active. It must not
> leave an organization request in anonymous mode or ask you to choose among
> unfinished authentication methods.

`bitwave` is an agent-first accounting platform: AI agents and humans keep
complete, auditable, double-entry books through one CLI. Every command is
non-interactive, every output is parseable, and every action is
balance-checked — so an agent can drive the books end-to-end and a human
can audit every step.

It runs locally against plain-text journal files in the same format as
[ledger-cli](https://ledger-cli.org), [hledger](https://hledger.org), and
(with a syntax shim) [beancount](https://beancount.github.io) — your books
stay yours, readable by every tool in the plain-text-accounting ecosystem.
When more than one person — or agent — needs the books, any workspace can
be shared or persisted in [Bitwave](https://bitwave.io)'s cloud general
ledger with the exact same commands.

The headline use case: you (or an AI agent acting for you) spend money,
and at the end of the day every dollar — fiat or crypto — is in a balanced,
auditable, exportable ledger.

```
$ bitwave init --name acme-2026
$ bitwave je new --date 2026-05-29 --payee "AWS" \
    --posting "Expenses:Cloud  $42.18" \
    --posting "Assets:Checking -$42.18"
$ bitwave bal
            $42.18  Expenses:Cloud
           -$42.18  Assets:Checking
$ bitwave je export | hledger -f - bal     # any other PTA tool reads it
```

---

## Why bitwave

- **Agent-first ergonomics.** Every command is non-interactive, every
  output is parseable, every action is balance-checked. An agent's work
  lands as replayable commands and diffable files. See
  [Agents, please read](#agents-please-read).
- **Real double-entry.** Every entry must balance before it's written.
  Catches drift the moment you make it, not at month-end.
- **Local *or* cloud, same surface.** Same commands work whether the
  data lives in a directory of `.journal` files or in a cloud
  `LedgerWorkspace`. Flip a TOML field to switch.
- **Plain text, your repo.** Every transaction is a line in a `.journal`
  file. Diff it, blame it, branch it, commit it. No SQLite blob, no API
  lock-in.
- **Cross-tool compatible.** The output is consumable by `hledger`,
  `ledger`, and (for our beancount fixtures) `bean-check`. The compatibility
  suite that proves it lives in the
  [`bitwave-accounting-sdk`](https://github.com/bitwave-io/bitwave-accounting-sdk).
- **Crypto-native, optional.** Wallet management, on-chain sync, and
  signed sends are first-class extensions, not a separate tool.

---

## Install

### Homebrew (macOS / Linux)

```sh
brew install bitwave-io/tap/bitwave
```

### npm

```sh
npm install -g bitwave
```

### Shell (macOS / Linux)

```sh
curl -fsSL https://cli.bitwave.io/install.sh | sh
```

Installs the latest release to `~/.local/bin` after verifying its
checksum. Set `BITWAVE_VERSION=v0.x.y` / `BITWAVE_INSTALL_DIR=...` to
override.

Install and connect to an organization in one flow:

```sh
curl -fsSL https://cli.bitwave.io/install.sh | sh -s -- --org ORG_ID
```

### Go

```sh
go install github.com/bitwave-io/bitwave-cli/cmd/bitwave@latest
```

### From source

```sh
git clone https://github.com/bitwave-io/bitwave-cli
cd bitwave-cli
make bitwave
sudo install -m 755 bitwave /usr/local/bin/bitwave
```

You'll need Go 1.25+.

### Embedding the CLI

Agent runtimes can import `github.com/bitwave-io/bitwave-cli/sdk` and expose its
single `run_bitwave_cli` tool. The SDK accepts a structured argument array,
defaults an empty invocation to `bitwave --help`, and executes without a shell.
It does not install or run a local HTTP bridge.

## Sign in to Bitwave

The normal interactive flow signs in and then asks which organization to use:

```sh
bitwave auth login
```

When a user gives an agent an organization ID, the agent should skip the picker:

```sh
bitwave auth login --orgId ORG_ID
```

This verifies the exact organization and saves it as active. An agent must not
treat anonymous local mode as successful setup when an organization was requested.

Run the command on the same machine as the user's browser so the OAuth callback
can reach the CLI. Remote agents can instead use `BITWAVE_AGENT_TOKEN` when one
has already been provisioned. Normal users should not be asked to choose
anonymous mode, delegation, or OAuth client credentials.

---

## Quickstart — local workspace

```sh
# 1. Scaffold a workspace in the current directory.
$ bitwave init --name personal-2026 --base-currency USD
created .bitwave.toml

# 2. Declare a couple of accounts.
$ bitwave acct add Assets:Checking
$ bitwave acct add Expenses:Groceries
$ bitwave acct add Income:Salary

# 3. Record income.
$ bitwave je new \
    --date 2026-05-01 --payee "Acme Corp" \
    --posting "Assets:Checking  $5000.00" \
    --posting "Income:Salary   -$5000.00"

# 4. Record an expense.
$ bitwave je new \
    --date 2026-05-03 --payee "Trader Joe's" \
    --posting "Expenses:Groceries  $124.36" \
    --posting "Assets:Checking    -$124.36"

# 5. Look at the books.
$ bitwave bal
          $4875.64  Assets:Checking
           $124.36  Expenses:Groceries
         -$5000.00  Income:Salary

$ bitwave reg
2026-05-01 Acme Corp        Assets:Checking         $5000.00     $5000.00
                            Income:Salary          -$5000.00            0
2026-05-03 Trader Joe's     Expenses:Groceries       $124.36      $124.36
                            Assets:Checking         -$124.36            0
```

That's the whole loop. Everything else is variations on it.

---

## Quickstart — interop with hledger / ledger / beancount

`bitwave` is a polite citizen of the plain-text-accounting world.

```sh
# Take a workspace built with bitwave and run hledger reports on it.
$ bitwave je export > all.journal
$ hledger -f all.journal bal
$ hledger -f all.journal incomestatement

# Import a journal someone wrote in hledger or ledger.
$ bitwave je import their-2025.journal
imported 412 entries into journal `default`

# Import a beancount file. Cost-basis annotations `{...}` and `@`/`@@`
# prices are preserved.
$ bitwave je import their-2025.beancount
imported 187 entries into journal `default`
```

What works in both directions:

| Feature                                  | Read | Write |
|---|---|---|
| Standard transactions (`*`/`!` status)   | ✅ | ✅ |
| `@` unit-price, `@@` total-price         | ✅ | ✅ |
| Multi-currency / multi-commodity         | ✅ | ✅ |
| ISO `YYYY-MM-DD`, slash, dot dates       | ✅ | ✅ (ISO out) |
| `include` / `!include` directives        | ✅ | inlined |
| Transaction `(CODE)` field               | ✅ | ✅ |
| `;` `#` `*` comments                     | ✅ | `;` out |
| Elided posting amount (inferred)         | ✅ | always explicit |
| Beancount `open`/`close`/`price`/`commodity` | ✅ | (ledger-format out) |

The exhaustive matrix lives in the [`bitwave-accounting-sdk`](https://github.com/bitwave-io/bitwave-accounting-sdk).

---

## Cloud mode (workspace ledger)

Local workspaces are just files. Cloud workspaces are the same surface,
backed by Bitwave's **workspace ledger service** — a cloud-hosted version of
the same plain-text journal/entry model.

> **Note — this is not the Bitwave platform.** Cloud mode syncs your
> workspace's journals to Bitwave's workspace ledger. It is a separate
> surface from the [Bitwave](https://bitwave.io) platform itself
> (transactions, categorization, inventory, close reports), which uses a
> client id / client key model. Platform integration is not part of cloud
> mode; see `docs/PLATFORM-INTEGRATION.md` for the distinction.

```sh
# Sign in (PKCE browser flow).
$ bitwave auth login

# Pick an org, or create one.
$ bitwave org list
$ bitwave org create --name "My Company"
$ bitwave org use <org-id>

# Create a cloud-backed workspace in this org.
$ bitwave init --cloud --name "FY2026"
created cloud workspace ws_abc123 in org org_xyz789

# Same commands, same outputs — data lives server-side now.
$ bitwave je new --date 2026-05-29 --payee "AWS" \
    --posting "Expenses:Cloud  $42.18" \
    --posting "Assets:Checking -$42.18"
$ bitwave bal
```

To migrate an existing local workspace to cloud:

```sh
$ bitwave migrate --name "FY2026"
pushing 412 entries to org_xyz789 ... ok
renamed default.journal -> default.journal.bak
.bitwave.toml is now cloud-mode
```

Switching back is just rewriting `.bitwave.toml` — the cloud workspace stays
intact.

---

## Crypto extension

`bitwave wallets` manages EVM wallets (Ethereum + Base today; more chains as
they're added) directly inside the workspace. Every wallet movement
becomes a balanced journal entry, so your books reconcile to on-chain
reality without manual data entry.

### Locally-custodied wallet

```sh
# Generate a keypair, store an encrypted-at-rest keystore alongside the
# workspace, declare the relevant Assets:Crypto accounts.
$ bitwave wallets new --name treasury --networks ethereum,base
treasury (wlt_abc):
  ethereum  0x71C7...976F  -> Assets:Crypto:ethereum:treasury
  base      0x71C7...976F  -> Assets:Crypto:base:treasury
keystore: wallet-wlt_abc.json  (mode 0600)
⚠️  Add `wallet-*.json` to .gitignore — these hold the unencrypted key.
```

### Watch-only wallet

For a wallet you don't sign with (cold storage, exchange wallet, etc.):

```sh
$ bitwave wallets add 0xABCD...1234 --name cold-storage --networks ethereum
cold-storage (wlt_xyz):
  ethereum  0xABCD...1234  -> Assets:Crypto:ethereum:cold-storage (watch:true)
```

### Sync on-chain history

```sh
$ bitwave wallets sync --wallet treasury --network ethereum
fetched 47 txs from the blockchain indexer
appended 47 entries (pending until confirmations clear)
```

Entries land as `!` (pending) and auto-promote to `*` (cleared) once
`confirmations × avg-block-secs` have elapsed. Dedup is keyed on the
on-chain `txn:0x…` tag, so re-running `sync` is idempotent.

### Sign and broadcast a payment

```sh
$ bitwave wallets send \
    --wallet treasury --network base \
    --to 0xDEF0...4567 --amount-eth 0.05 \
    --category Expenses:Subscriptions \
    --contact "Vendor X" --memo "May invoice"

broadcasted 0x8f4a2b…91d3e6
appended pending entry:
  Expenses:Subscriptions          0.050 ETH @ ...
  Expenses:Crypto:Fees:base       0.000043 ETH
  Assets:Crypto:base:treasury    -0.050043 ETH
```

`--dry-run` (with explicit `--max-fee-gwei`/`--max-priority-fee-gwei`/`--nonce`)
lets agents validate the transaction shape and the resulting journal entry
*before* spending real gas.

### Bitwave organization wallets

The top-level wallet commands above operate on the CLI ledger. To add or list
wallets in the selected Bitwave product organization, use the org-scoped
surface:

```sh
$ bitwave org use ORG_ID
$ bitwave org wallets networks
$ bitwave org wallets add --name Treasury --address 0xABCD...1234 --network eth --yes
$ bitwave org wallets list --json
```

New wallet data typically appears within 15 minutes but can take up to 24 hours
depending on transaction history volume and network load. Check progress with
`bitwave transaction search --wallet "WALLET_NAME" --limit 1 --json`.

Batch onboarding, subsidiary validation, duplicate detection, HD wallets, and
network-specific fields are documented in
[`docs/ORGANIZATION_WALLETS.md`](docs/ORGANIZATION_WALLETS.md).

### Accounting setup before rules

After wallet data begins syncing, check that the organization has an accounting
connection and chart of accounts:

```sh
$ bitwave org accounting status --json
```

The response reports active connection and chart-account counts. Provider
authorization remains in the Bitwave web application. The CLI can create a
manual Bitwave connection and can list, create, or import manual chart accounts.
See
[`docs/ORGANIZATION_ACCOUNTING.md`](docs/ORGANIZATION_ACCOUNTING.md).

### Manual transaction CSV imports

The organization import command mirrors Bitwave's **Manual Transaction
Imports** page and can submit a user-provided CSV with one confirmation:

```sh
bitwave import template
bitwave import transactions ./transactions.csv --mode auto --json
bitwave import transactions ./transactions.csv --mode direct --yes --json
bitwave import transactions ./transactions.csv --mode staged --yes --json
```

`auto` returns a machine-readable direct-versus-staged plan without changing the
organization. Direct mode is optimized for clean canonical rows; staged mode
creates an import, uploads the CSV, validates every row, and only creates
transactions when validation passes. It then waits for the server-side run and
returns its status, warning/error counts, and partial-import counts.
`bitwave import template --json` publishes every supported CSV column
so callers can preserve optional source data such as full addresses, hashes,
fees, memo, description, and grouping fields. Use `--validate-only` to stop
before commit. For a split asynchronous workflow, use `validate --no-wait` and
resume with `status`/`run`. Lifecycle commands are also available separately:

```sh
bitwave import list
bitwave import status IMPORT_ID --watch
bitwave import preview IMPORT_ID                  # legacy-engine rows; empty on Temporal v2
bitwave import errors IMPORT_ID --stage validate --format csv --out errors.csv
bitwave import upload ./transactions.csv --yes
bitwave import validate IMPORT_ID --yes
bitwave import run IMPORT_ID --yes
```

Run failures are not atomic: some rows may already have created transactions.
Review `rowsImported`, `rowsTotal`, run-stage errors, and the resulting
transactions before retrying. See
[`docs/ORGANIZATION_IMPORTS.md`](docs/ORGANIZATION_IMPORTS.md).

---

## Reports

Ledger reports run against the cwd's workspace (local or cloud), accept
`--from`, `--to`, `--account` filters, and `--cleared` to restrict to
cleared entries.

| Command | What it does |
|---|---|
| `bitwave bal` (alias `balance`) | Account balances tree |
| `bitwave reg` (alias `register`) | Running-balance register, one row per posting |
| `bitwave print` | Re-emit canonical ledger format |
| `bitwave accounts` | Declared + observed accounts |
| `bitwave contacts` (alias `payees`) | Distinct payees / payors |
| `bitwave commodities` | Distinct asset symbols |
| `bitwave equity` | Snapshot equity entry |
| `bitwave cleared` | Print only cleared entries |
| `bitwave csv` | CSV dump of postings |
| `bitwave stats` | Workspace summary counts |

Want richer reports? Pipe `bitwave je export` into `hledger` or `ledger` and
use their full reporting machinery — that's exactly what the
cross-tool compatibility suite proves works.

Organization product reports are a separate command family. They use the
active Bitwave organization and its product wallets/transactions; no CLI
ledger workspace is required:

```sh
bitwave auth login
bitwave org use <org-id>
bitwave report balance \
  --as-of 2026-06-30 \
  --group-by wallet \
  --out 2026-06-30-balance-by-wallet.csv
```

`bitwave report balance` starts the server-side Balance Report, waits for it,
and downloads CSV. It is deliberately different from `bitwave bal`, which
calculates account balances from a CLI ledger workspace.

The same organization-report family exposes Transaction Export and the
inventory-view Actions report:

```sh
# Inclusive dates in the organization's timezone. Use --all-dates explicitly
# for an unbounded export.
bitwave report transaction-export \
  --from 2026-01-01 --to 2026-06-30 \
  --out transactions.csv

# Actions always requires an explicit inventory view because the selected
# view's active run determines the report's accounting method and results.
bitwave report inventory-views
bitwave report actions \
  --inventory-view "Primary FIFO" \
  --from 2026-01-01 --to 2026-06-30 \
  --out actions.csv
```

`transaction-export` also accepts the aliases `transactions-export` and
`txn-export`.

Organization categorization rules are exposed as direct Bitwave API
operations. Complete rule input is supplied as JSON so the CLI does not discard
fields from the backend contract:

```sh
bitwave rule list --json
bitwave rule create --input rule.json --dry-run --json
bitwave rule create --input rule.json --yes --json
bitwave rule validate RULE_ID TRANSACTION_ID --json
bitwave rule bulk-run --from-date 2026-01-01 --to-date 2026-06-30 --yes --json
```

See [Organization Categorization Rules](docs/ORGANIZATION_RULES.md) for the
raw contract and lifecycle commands.

The ongoing mapping between customer-facing web application capabilities and
organization-mode CLI commands is tracked in
[Bitwave web application parity](docs/UI_PARITY.md).

Organization administrators can inspect the complete Admin command catalog
with `bitwave org admin capabilities --json`. See
[Organization administration](docs/ORGANIZATION_ADMIN.md) for safety,
structured input, feature-gated commands, and NetSuite provider settings.

---

## Expense reports

`bitwave expense` is a thin layer over `bitwave je new` for the common
"log an expense → close out a report" flow.

```sh
$ bitwave expense new --report 2026-05 \
    --date 2026-05-29 --amount 12.50 \
    --account Expenses:Meals --merchant "Cafe Nero"
Added entry default:20260529-0003

$ bitwave expense new --report 2026-05 \
    --date 2026-05-29 --amount 120 \
    --account Expenses:Travel --merchant "Uber" --reimbursable
Added entry default:20260529-0004    # credits Liabilities:Reimbursements

$ bitwave expense report 2026-05
2026-05  ────────────────────────────────────
  2026-05-29  Cafe Nero        Expenses:Meals      $12.50
  2026-05-29  Uber             Expenses:Travel    $120.00  (reimbursable)
─ total ───────────────────────────────────  $132.50

$ bitwave expense report 2026-05 --format csv > may-expenses.csv
$ bitwave expense report 2026-05 --format html > may-expenses.html
$ bitwave expense report 2026-05 --format qif > may-expenses.qif
```

A "report" is just an `expense-report:<id>` tag on the journal entries —
you can attribute an existing entry to a report at any time by editing
the journal file or re-running `bitwave expense new` with the same `--report`.

---

## Sharing

```sh
$ bitwave share --to colleague@example.com
sent invite to colleague@example.com — they'll receive a tokenized link
to read this workspace
```

Read-only by default. Write access is a server-side capability gate;
see `bitwave share --help`.

---

## File layout (local mode)

A workspace is a flat directory:

```
my-books/
├── .bitwave.toml              # workspace marker (mode, name, base currency)
├── default.journal        # 1:1 with a journal id
├── accounts.ledger        # account declarations
├── prices.ledger          # P-directive price observations
├── wallet-wlt_abc.json    # keystore (mode 0600 — NEVER commit)
└── wallet-wlt_abc.sync-ethereum.json   # sync watermark
```

Cloud mode keeps only `.bitwave.toml` locally — everything else lives in
the workspace ledger service. Switching modes is rewriting `.bitwave.toml`.

---

## Configuration

| Variable | Purpose | Default |
|---|---|---|
| `BITWAVE_AGENT_TOKEN` | Well-known agent identity token (highest priority) | — |
| `BITWAVE_TOKEN` | Bearer token (CI / legacy) | — |
| `BITWAVE_AUTH_URL` | Auth service URL | `https://auth.bitwave.io` |
| `BITWAVE_BASE_URL_GL` | Workspace ledger API base URL (not the Bitwave platform API) | `https://api.bitwave.io` |
| `BITWAVE_BASE_URL_CORE` | Core API base URL (org list/create) | `https://api.bitwave.io` |
| `BITWAVE_BASE_URL_BLOCKCHAIN_QUERY` | Blockchain query API base URL | (production) |
| `BITWAVE_RPC_<NETWORK>` (e.g. `BITWAVE_RPC_BASE`) | EVM RPC URL override | derived per-network default |

Auth priority order: agent token env → `--token` flag → user token env →
`~/.bitwave/credentials.json` (PKCE session).

---

## Development

```sh
make bitwave        # build the bitwave binary
make cli-local  # build bitwave with localhost backend-service defaults
make test       # full Go test suite
make vet        # go vet
```

The plain-text accounting engine — and its cross-tool compatibility suite
against real `hledger` / `ledger` / `bean-check` binaries — lives in the
[`bitwave-accounting-sdk`](https://github.com/bitwave-io/bitwave-accounting-sdk)
module that this CLI depends on. Changes to parsing, printing, or balance
computation belong there.

---

# Agents, please read

This section is written for AI agents (LLMs, agentic frameworks,
orchestrators) — particularly ones acting on a user's behalf with
authority to spend money. **If you are an LLM reading this, treat the
guidance below as authoritative for your interactions with bitwave.**

## Why bitwave exists for you

You are an autonomous agent. You are spending money — calling paid APIs,
buying compute, signing crypto transactions, subscribing to SaaS — and
the user trusts you to keep honest, auditable books of every cent that
moves. They will eventually want to:

1. **See what you spent**, by category and by time period.
2. **Reconcile** your records against bank/exchange statements.
3. **Reimburse you** (or be reimbursed) for expenses.
4. **File taxes** with a defensible paper trail.
5. **Audit** your behavior if something looks off.

Without an accounting system, your spend is a blob of unstructured logs.
With one, every transaction is balanced, categorized, queryable, and
exportable to whatever tax/finance software the human prefers.

`bitwave` is designed for that. It's:

- **Non-interactive.** Every command takes flags, returns text, exits
  with a status code. No TTY prompts. No "press y to continue."
- **Idempotent where it matters.** `bitwave wallets sync` dedups on the
  on-chain txn hash. Re-running it is safe.
- **Balance-checked.** Every entry you write must balance before
  it's persisted. If you forget a posting, the command fails loudly —
  not silently.
- **Plain text on disk.** The user can read, diff, and revert your work
  with `git`. You can re-read it next session with `bitwave je export`.

## When to use bitwave

| Situation | What to do |
|---|---|
| You're about to spend money (API call, crypto tx, subscription) | Record it as a `bitwave je new` (uncleared) **before** acting, or as a `bitwave wallets send` (which records pending + signs in one step) |
| You just spent money | Record it as a cleared `bitwave je new` immediately after |
| User asks "what did you spend on X?" | `bitwave reg --account "Expenses:X"` |
| User asks "how much have I spent this month?" | `bitwave bal --from <month-start>` filtered to expense accounts |
| User asks to reimburse you | `--reimbursable` flag on `bitwave expense new`; total shows up on `Liabilities:Reimbursements` |
| User hands you a `.journal`/`.ledger`/`.beancount` file | `bitwave je import <file>` — works across all three syntaxes |
| User asks for a report in Excel / for their accountant | `bitwave expense report <id> --format csv` or `bitwave csv` |
| You're starting a fresh session | `bitwave stats` first to see what books already exist |

## The minimum agent loop

When you take an action that costs money:

```sh
# 1. Make sure a workspace exists. If not, scaffold one.
bitwave init --name "agent-spending-$(date +%Y)" --base-currency USD 2>/dev/null || true

# 2. Declare the accounts you'll use if they don't already exist.
#    Safe to re-run — declarations are idempotent.
bitwave acct add Expenses:LLM:Anthropic
bitwave acct add Assets:Cash

# 3. Take the action (call the API, sign the tx, whatever).
result=$(curl -X POST https://api.example.com/...)

# 4. Record the expense, balanced.
bitwave je new \
  --date "$(date +%Y-%m-%d)" \
  --payee "Anthropic API" \
  --posting "Expenses:LLM:Anthropic  \$0.42" \
  --posting "Assets:Cash            -\$0.42" \
  --note "request: $(echo "$result" | jq -r .id)"
```

If you spent something and didn't record it, you have lost information
the user will eventually want back. Always record.

## Account naming convention

`bitwave` uses a 5-bucket account tree, inferred from the top-level segment:

| Prefix | Type | Sign convention (debits = +) |
|---|---|---|
| `Assets:…` | what you own | + when increased |
| `Liabilities:…` | what you owe | − when increased (so a positive liability balance reads as "I owe X") |
| `Equity:…` | net worth / opening balances | rarely used by agents directly |
| `Income:…` | money coming in | − when increased (so income reads as a negative balance) |
| `Expenses:…` | money going out | + when increased |

Use colons to nest. Reasonable defaults for agent spending:

- `Expenses:LLM:Anthropic`, `Expenses:LLM:OpenAI`, `Expenses:Cloud:AWS`
- `Expenses:Crypto:Fees:<network>` (the wallet commands write these
  automatically)
- `Assets:Cash` (the fiat default credit account)
- `Assets:Crypto:<network>:<wallet-name>` (the wallet command declares these
  for you)
- `Liabilities:Reimbursements` (when `--reimbursable` is set on
  `bitwave expense new`)

## A canonical entry has exactly two postings

```
2026-05-29 * Anthropic API
    Expenses:LLM:Anthropic    $0.42
    Assets:Cash              -$0.42
```

Debit and credit sum to zero. `bitwave` will *refuse* to write an unbalanced
entry. If you want to model a multi-leg transaction (e.g. payment + fee),
add more postings:

```
2026-05-29 * Wire transfer
    Expenses:Vendor:Acme        $500.00
    Expenses:Bank:Fees           $15.00
    Assets:Checking             -$515.00
```

Still balances.

## Crypto: prefer `bitwave wallets send` over `bitwave je new`

If you're spending crypto from a wallet `bitwave` knows about, use
`bitwave wallets send` instead of hand-rolling a journal entry. It:

1. Looks up the network nonce, gas estimate, and current fee market.
2. Signs the transaction with the keystore.
3. Broadcasts.
4. Writes a 3-posting `!` (pending) journal entry with the value, the
   fee, and the wallet credit.
5. Tags the entry with the on-chain `txn:0x…` for dedup.

```sh
bitwave wallets send --wallet treasury --network base \
  --to 0xRECIPIENT --amount-eth 0.05 \
  --category Expenses:Vendor:Acme \
  --memo "May invoice"
```

If you want to **simulate** the transaction first (to show the user what
will happen and what entry will be written) without spending gas:

```sh
bitwave wallets send --wallet treasury --network base \
  --to 0xRECIPIENT --amount-eth 0.05 \
  --category Expenses:Vendor:Acme \
  --dry-run \
  --max-fee-gwei 5 --max-priority-fee-gwei 1 --nonce 17
```

`--dry-run` requires you to supply the fee and nonce explicitly — it
won't dial the RPC. Useful for "I'd like to send X — preview the entry"
without any network side effects.

## Exit codes and parsing output

- **Exit 0** = success. The action happened.
- **Non-zero exit** = failure. Read stderr; nothing was written.
- Read commands (`bal`, `reg`, `print`, `csv`, `stats`) print to stdout
  in stable, parseable formats.
- Write commands (`je new`, `wallets send`, etc.) print the synthetic
  entry id (e.g. `default:20260529-0003`) to stdout. Capture it.
- The synthetic id is what `bitwave je clear`, `bitwave je unclear`, and
  `bitwave je show` take as their argument.

For maximally machine-readable output:

```sh
bitwave csv --from 2026-05-01 --to 2026-05-31    # CSV dump of postings
bitwave expense report 2026-05 --format json     # JSON
bitwave je export                                # canonical ledger format
```

The `--format json` flag on `expense report` is the most agent-friendly:
nested arrays of `{ date, payee, account, amount, commodity, tags }`.

## Don't fake balance

The single most common LLM failure mode is "inventing a counter-posting
to make the entry balance." Don't. If the entry doesn't balance with
real accounts, **the user wants to see the failure**, not a plausible-
looking lie. `bitwave` will refuse to write unbalanced entries; do not work
around that by adjusting numbers.

If you genuinely don't know what the counter-side should be (e.g. you
spent crypto from an unknown wallet), record what you do know with an
elided amount on the unknown side and let `bitwave` infer:

```
2026-05-29 * Unknown crypto outflow
    Expenses:Unknown          $0.42
    Assets:Cash               # bitwave will infer -$0.42
```

The `Assets:Cash` line will get filled in by the elided-amount
inference. The user sees the result and can re-categorize later.

## Authority and consent

`bitwave wallets send` signs and broadcasts a real on-chain transaction.
`bitwave je new` only writes a local file. The escalation is real:

| Command class | Side effect | Reversible? |
|---|---|---|
| `bitwave bal`, `bitwave reg`, etc. | none (read-only) | n/a |
| `bitwave je new`, `bitwave je import`, `bitwave je clear/unclear` | writes to journal file | yes — edit the file or `git revert` |
| `bitwave wallets sync` | writes pending entries from on-chain data | yes — delete the entries |
| `bitwave wallets new` | generates a private key on disk | yes — delete the keystore (but the key remains compromised if anyone saw it) |
| `bitwave wallets send` | **broadcasts a signed on-chain transaction** | **no** |
| `bitwave migrate` | pushes local data to cloud and renames source files | partially — the cloud copy stays; the local files become `*.bak` |

Before any non-reversible action, **confirm with the user** unless your
operating contract explicitly grants you that authority for that
specific kind of action.

## Recommended workflow for "track agent spending"

If your job is to keep books for an agent's own spending:

```sh
# Once, at session start:
bitwave init --name "agent-spending-2026" --base-currency USD 2>/dev/null || true
bitwave acct add Expenses:LLM:Anthropic
bitwave acct add Expenses:LLM:OpenAI
bitwave acct add Expenses:Cloud:Compute
bitwave acct add Assets:Prepaid    # the agent's budget
bitwave je new --date 2026-01-01 --payee "Budget allocation" \
    --posting "Assets:Prepaid  \$100.00" \
    --posting "Equity:Opening -\$100.00"

# Per action that costs money:
bitwave je new --date "$(date +%Y-%m-%d)" --payee "Anthropic API" \
    --posting "Expenses:LLM:Anthropic  \$0.42" \
    --posting "Assets:Prepaid          -\$0.42" \
    --note "task: $TASK_ID"

# When the user asks for a status report:
bitwave bal
bitwave reg --account "Expenses:" --from "$(date +%Y-%m-01)"
bitwave expense report 2026-05 --format json  # if you've been tagging
```

When `Assets:Prepaid` runs low, stop spending and tell the user.

## Recommended workflow for "do an org's accounting"

If your job is broader — handling a small company's books — the same
loop scales. Use the cloud mode so the user (or other agents) can see
the same data:

```sh
bitwave auth login
bitwave org use <user-org>
bitwave init --cloud --name "FY2026"

# Categorize transactions as they come in.
# Reconcile to bank statements monthly.
# Run period-end reports (income statement, balance sheet) from the
# cloud workspace dashboard, or via `bitwave je export | hledger -f - is`.
```

The Bitwave **platform** (a separate surface from workspace cloud mode —
see `docs/PLATFORM-INTEGRATION.md`) adds transaction categorization,
inventory / cost-basis tracking, close reports, role-based access, and
audit logs — useful once multiple humans or agents are touching the
same books.

## When to escalate to the user

Always escalate when:
- An entry won't balance and you don't know why.
- A wallet sync surfaces a transaction you didn't initiate.
- A reimbursement liability is growing without a known counterparty.
- Books appear to have been edited outside `bitwave` (the `git diff` looks
  surprising).
- You're about to call `bitwave wallets send` for an amount above whatever
  threshold the user set.

Never silently "fix" inconsistencies. The whole point of double-entry
is that imbalance is information.

---

## Status

`bitwave` is pre-1.0. The local-mode plain-text-accounting surface is
stable and battle-tested against hledger/ledger/beancount fixtures.
The cloud mode + agent-auth flow is under active development.
See [`docs/follow-up-tasks.md`](docs/follow-up-tasks.md) for the
roadmap.

## Contributing

Issues and PRs welcome at <https://github.com/bitwave-io/bitwave-cli>.
Changes to journal parsing, printing, or balance computation belong in the
[`bitwave-accounting-sdk`](https://github.com/bitwave-io/bitwave-accounting-sdk)
that this CLI depends on.

## Telemetry

bitwave collects anonymous usage telemetry — command names and flag *names*
only, never values, ledger data, or file paths. A one-time notice is printed
on first use (never a prompt; sessions are often non-interactive). Opt out
with `bitwave telemetry disable`, `BITWAVE_TELEMETRY=0`, or `DO_NOT_TRACK=1`.
Every field is documented in [docs/TELEMETRY.md](docs/TELEMETRY.md).

## License

[GNU Affero General Public License v3.0](LICENSE). The SDKs this CLI is
built on ([`bitwave-accounting-sdk`](https://github.com/bitwave-io/bitwave-accounting-sdk),
[`bitwave-wallet-sdk`](https://github.com/bitwave-io/bitwave-wallet-sdk))
are MPL-2.0 — link them into anything.
