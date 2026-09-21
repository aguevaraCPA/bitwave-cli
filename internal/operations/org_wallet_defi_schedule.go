package operations

import (
	"errors"
	"fmt"
	"strings"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

type orgWalletDefiScheduleFlags struct {
	transactionMutationFlags
	all            bool
	network        string
	cadenceSeconds int
	trigger        bool
}

func newOrgWalletDefiScheduleCmd() *op.Definition {
	var f orgWalletDefiScheduleFlags
	cmd := &op.Definition{
		Use:   "defi-schedule [WALLET_ID_OR_NAME]",
		Short: "Start (or confirm) the DeFi position sync schedule for a DeFi wallet",
		Long: `Ask sync-coordinator to create the daily DeFi position sync schedule for one
DeFi wallet, or for every DeFi wallet on a network with --all --network.

Creating a DeFi wallet does not start its position sync on its own; this
command does. The backend resolves the protocol from the wallet's vault
address (for example Aerodrome on Base, or Monad native staking on the
0x…1000 precompile), creates one idempotent Temporal schedule, and triggers
the first run immediately. Re-running reports ALREADY_EXISTS and leaves the
existing schedule untouched; add --trigger to fire a run now on an existing
schedule (for example to re-run a failed first pass after a fix).

The Bitwave API does not currently return a network on DeFi wallet records, so
pass --network for a single wallet (the CLI asks for it when it cannot infer
one).

Use --dry-run to print the exact request. Use --yes to create the schedule.`,
		Args: op.RangeArgs(0, 1),
		RunE: func(cmd *op.Call, args []string) error { return runOrgWalletDefiSchedule(cmd, f, args) },
	}
	addMutationFlags(cmd, &f.transactionMutationFlags)
	cmd.Flags().BoolVar(&f.all, "all", false, "Schedule every DeFi wallet on --network instead of one wallet")
	cmd.Flags().StringVar(&f.network, "network", "", "Canonical network ID (required with --all; optional override for one wallet)")
	cmd.Flags().IntVar(&f.cadenceSeconds, "cadence-seconds", 0, "Optional discovery cadence for --all (backend default when 0)")
	cmd.Flags().BoolVar(&f.trigger, "trigger", false, "Fire a run now if the wallet's schedule already exists (single-wallet mode)")
	return cmd
}

func runOrgWalletDefiSchedule(cmd *op.Call, f orgWalletDefiScheduleFlags, args []string) error {
	operation := "schedule-defi-position-sync"
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	client := newReportsClient(cmd.Context(), orgID)
	baseURL := resolveCoreBaseURL(cmd.Context())

	if f.all {
		if len(args) > 0 {
			return mutationError(cmd, operation, f.jsonOutput, errors.New("--all schedules every DeFi wallet on --network; do not pass a wallet"))
		}
		network := normalizeOrgWalletNetwork(f.network)
		if network == "" {
			return mutationError(cmd, operation, f.jsonOutput, errors.New("--network is required with --all"))
		}
		if f.cadenceSeconds < 0 {
			return mutationError(cmd, operation, f.jsonOutput, errors.New("--cadence-seconds cannot be negative"))
		}
		body := map[string]any{"networkId": network}
		if f.cadenceSeconds > 0 {
			body["cadenceSeconds"] = f.cadenceSeconds
		}
		preview := map[string]any{"method": "POST", "url": baseURL + orgreports.DefiNetworkSchedulesPath(orgID), "body": body}
		if f.dryRun {
			return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
		}
		if !f.yes {
			return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to create sync schedules without --yes (use --dry-run to preview)"))
		}
		result, err := client.ScheduleDefiWalletsForNetwork(cmd.Context(), orgID, network, f.cadenceSeconds)
		if err != nil {
			return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("schedule defi wallets for %s: %w", network, err))
		}
		envelope := mutationEnvelope{SchemaVersion: "1", Status: "scheduled", Operation: operation, Organization: orgID, Request: preview, Result: map[string]any{"network": network, "schedule": result}}
		human := fmt.Sprintf("DeFi discovery scheduled for network %s: schedule=%s status=%s\n%s\n", network, result.ScheduleID, result.Status, result.Message)
		return outputMutation(cmd, f.jsonOutput, envelope, human)
	}

	if len(args) != 1 {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("WALLET_ID_OR_NAME is required (or use --all --network)"))
	}
	wallet, err := resolveOrganizationDefiWallet(cmd, client, orgID, args[0], f.network)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	request := orgreports.DefiScheduleRequest{WalletAddress: wallet.Address, ContractAddress: wallet.VaultAddress, NetworkID: wallet.NetworkID, TriggerNow: f.trigger}
	preview := map[string]any{"method": "POST", "url": baseURL + orgreports.DefiWalletSchedulePath(orgID, wallet.ID), "body": request}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview, Result: map[string]any{"wallet": wallet}})
	}
	if !f.yes {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to create a sync schedule without --yes (use --dry-run to preview)"))
	}
	result, err := client.ScheduleDefiWallet(cmd.Context(), orgID, wallet.ID, request)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("schedule defi position sync for wallet %s: %w", wallet.ID, err))
	}
	status := "scheduled"
	switch {
	case strings.EqualFold(result.Status, "ALREADY_EXISTS"):
		status = "already_exists"
	case strings.EqualFold(result.Status, "TRIGGERED"):
		status = "triggered"
	}
	envelope := mutationEnvelope{SchemaVersion: "1", Status: status, Operation: operation, Organization: orgID, Request: preview, Result: map[string]any{"wallet": wallet, "schedule": result}}
	human := fmt.Sprintf("DeFi position sync for %s (%s): protocol=%s schedule=%s status=%s\n%s\nCheck progress: bitwave transaction search --wallet %q --limit 5 --json\n",
		wallet.Name, wallet.ID, result.Protocol, result.ScheduleID, result.Status, result.Message, wallet.Name)
	return outputMutation(cmd, f.jsonOutput, envelope, human)
}

// resolveOrganizationDefiWallet finds one organization wallet by id or name
// through the REST wallet list (the only surface that exposes vaultAddress)
// and confirms it is a DeFi position wallet. The REST list omits networkId
// for DeFi wallets, so the network comes from networkOverride when given,
// otherwise from the GraphQL wallet record.
func resolveOrganizationDefiWallet(cmd *op.Call, client *orgreports.Client, orgID, value, networkOverride string) (*orgreports.Wallet, error) {
	wallets, err := client.Wallets(cmd.Context(), orgID)
	if err != nil {
		return nil, fmt.Errorf("list organization wallets: %w", err)
	}
	var match *orgreports.Wallet
	for i := range wallets {
		if wallets[i].ID == value {
			match = &wallets[i]
			break
		}
		if strings.EqualFold(wallets[i].Name, value) {
			if match != nil {
				return nil, fmt.Errorf("wallet name %q is ambiguous; use the wallet ID", value)
			}
			match = &wallets[i]
		}
	}
	if match == nil {
		return nil, fmt.Errorf("wallet %q was not found", value)
	}
	if match.VaultAddress == "" {
		return nil, fmt.Errorf("wallet %s (%s) is not a DeFi position wallet (no vault address); create one with: bitwave org wallets add --type defi --vault-address CONTRACT ...", match.Name, match.ID)
	}
	if match.Address == "" && len(match.Addresses) > 0 {
		match.Address = match.Addresses[0]
	}
	if match.Address == "" {
		return nil, fmt.Errorf("wallet %s (%s) is missing its wallet address", match.Name, match.ID)
	}
	if network := normalizeOrgWalletNetwork(networkOverride); network != "" {
		match.NetworkID = network
	}
	if match.NetworkID == "" {
		graphWallets, err := client.OrgWallets(cmd.Context(), orgID)
		if err != nil {
			return nil, fmt.Errorf("resolve wallet network: %w", err)
		}
		for _, candidate := range graphWallets {
			if candidate.ID == match.ID {
				match.NetworkID = strings.ToLower(strings.TrimSpace(candidate.NetworkID))
				break
			}
		}
	}
	if match.NetworkID == "" {
		return nil, fmt.Errorf("wallet %s (%s) has no network on record; pass --network", match.Name, match.ID)
	}
	return match, nil
}

// normalizeOrgWalletNetwork lowercases a network id and applies the common
// name aliases (ethereum -> eth, stellar -> xlm, ...). Empty stays empty.
func normalizeOrgWalletNetwork(value string) string {
	network := strings.ToLower(strings.TrimSpace(value))
	if alias := organizationWalletNetworkAliases[network]; alias != "" {
		return alias
	}
	return network
}
