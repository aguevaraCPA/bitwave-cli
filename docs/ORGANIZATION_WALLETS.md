# Organization wallets

`bitwave org wallets` manages wallets in the selected Bitwave product
organization. It is intentionally separate from the top-level `bitwave wallets`
command, which manages watch addresses for a local plain-text ledger workspace.

## Connect and inspect

```bash
bitwave auth login
bitwave org use ORG_ID
bitwave org current
bitwave org wallets list --json
bitwave org wallets networks --json
```

The network list contains the union of the current Add Source catalog and legacy
wallet adapter. Creation is forward-compatible: the CLI accepts a new canonical
network ID and lets the Bitwave API validate it, so adding a backend network does
not require an immediate CLI release.

## Add one blockchain wallet

```bash
bitwave org wallets add \
  --name "Treasury" \
  --address 0x1111111111111111111111111111111111111111 \
  --network polygon \
  --yes
```

Use `--subsidiary SUBSIDIARY_ID` when applicable. Common names such as
`ethereum`, `solana`, `aptos`, and `bnb` normalize to Bitwave's canonical IDs.
Address casing is preserved.

## Add a batch

The batch format is designed for non-interactive clients and onboarding scripts:

```json
[
  {
    "name": "Treasury",
    "address": "EXAMPLE_SOLANA_ADDRESS",
    "networkId": "sol"
  },
  {
    "name": "Operations",
    "address": "0x2222222222222222222222222222222222222222",
    "networkId": "eth",
    "subsidiaryId": "SUBSIDIARY_ID"
  }
]
```

```bash
bitwave org wallets add --input wallets.json --dry-run --json
bitwave org wallets add --input wallets.json --yes --json
```

Batch creation uses eight concurrent requests by default rather than paying one
round trip per wallet. Tune this with `--concurrency` (1–50) for the org and
network mix. Authentication is resolved once per batch; wallet sync remains an
asynchronous Bitwave service concern.

## Waiting for wallet data

Creating a wallet confirms that Bitwave accepted the source; it does not mean
all historical transactions are immediately available. Wallet data typically
appears within **15 minutes**, but a large history or a busy network can take up
to **24 hours**.

Check one wallet without downloading its full history:

```bash
bitwave transaction search --wallet "WALLET_NAME" --limit 1 --json
```

A result with `count: 0` during that window means no indexed transactions are
available yet. It does not, by itself, mean wallet creation or sync failed. If
it still returns no expected data after 24 hours, investigate the wallet
address, network selection, and Bitwave sync status.

Before creating anything, the CLI validates supplied subsidiary IDs and checks
for an existing wallet with the same network and address. Existing matches are
reported as `skipped_existing`; use `--allow-duplicate` only when intentional.
If a batch fails partway through, the JSON response identifies created and failed
items so an agent can safely retry.

## Add a DeFi position wallet

A DeFi wallet tracks one position: your wallet address plus the pool, vault, or
staking contract the position lives in. Bitwave's sync-coordinator resolves the
protocol from the vault address and syncs balances, movements, and rewards.

```bash
bitwave org wallets add \
  --name "Monad Staking" \
  --type defi \
  --network monad \
  --address 0xYourDelegatorWallet \
  --vault-address 0x0000000000000000000000000000000000001000 \
  --yes
```

Batch JSON uses `"type": "defi"` with `vaultAddress` (and an optional
informational `protocol`). Duplicate detection for DeFi wallets is per
(network, wallet address, vault address), so one wallet can hold positions in
several pools. Supported vaults today: Soroswap / Aquarius / Blend / Phoenix /
FxDAO pools on Stellar, Aerodrome and Arrakis on Base, Canton synchronizer
traffic (`traffic`), and Monad native staking (the `0x…1000` precompile).

## Schedule DeFi position sync

Creating a DeFi wallet does not start its position sync. Ask sync-coordinator
to create the daily schedule (the first run starts immediately):

```bash
bitwave org wallets defi-schedule "Monad Staking" --dry-run --json
bitwave org wallets defi-schedule "Monad Staking" --yes --json
```

The JSON result carries the resolved `protocol` (for example `MonadStaking` or
`Aerodrome`), the Temporal `scheduleId`, and `status` (`SCHEDULED`, or
`ALREADY_EXISTS` on a re-run, which leaves the existing schedule untouched).
An unsupported vault for the network is rejected with the backend's reason.

To schedule every DeFi wallet on one network at once:

```bash
bitwave org wallets defi-schedule --all --network monad --yes --json
```

Position data then flows into the normal transaction surface: Movement and
Reward transactions land under the wallet, and the staked / pending-withdrawal
balances appear in balance reports.

```bash
bitwave transaction search --wallet "Monad Staking" --limit 20 --json
```

## Network-specific inputs

All normal blockchain addresses use Bitwave's modern
`accountBasedBlockchain` contract. Additional fields supported in batch JSON:

- `syncStartDateSEC`: earliest sync time as Unix seconds
- `isBalanceMonitoringOnly`: create without normal transaction processing
- `viewKey`: private-chain/view-key input such as Aleo
- `metadata`: network-specific input such as XRP destination tags
- `addressType: "hd"`: BTC or DASH xpub/derivation key; uses the legacy watch
  shape

Canton wallets automatically receive the same syncer-version configuration as
Bitwave's Add Source flow.

The API's `WalletInput` currently has no creation-time `description` field. A
non-empty `description` is rejected explicitly rather than silently discarded.
