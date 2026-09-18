package operations

import (
	"errors"
	"fmt"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"
)

func newOrgWalletResyncCmd() *op.Definition {
	var f transactionMutationFlags
	cmd := &op.Definition{
		Use:   "resync WALLET_ID_OR_NAME",
		Short: "Reset sync state and replay a cloud wallet on its current syncer version",
		Long: `Perform a full source replay for one Bitwave organization wallet.

The backend discovers the syncer job currently registered for the wallet. It
does not switch syncer versions. A full Solana replay clears the wallet cursor
and every discovered owner, token, historical-token, and stake-account cursor
before starting that job.

Use --dry-run to inspect the selected job and state targets. Use --yes to clear
the state and start the asynchronous replay. A replay re-fetches source data;
it does not promise that a deliberately deleted transaction will be restored.`,
		Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			operation := "full-wallet-resync"
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			client := newReportsClient(cmd.Context(), orgID)
			wallet, err := resolveOrganizationWallet(cmd, client, orgID, args[0])
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			if !f.dryRun && !f.yes {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to reset wallet sync state without --yes (use --dry-run to preview)"))
			}

			result, err := client.FullWalletResync(cmd.Context(), orgID, wallet.ID, !f.dryRun)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("full wallet resync: %w", err))
			}
			status := "started"
			if f.dryRun {
				status = "preview"
			}
			envelope := mutationEnvelope{
				SchemaVersion: "1",
				Status:        status,
				Operation:     operation,
				Organization:  orgID,
				DryRun:        f.dryRun,
				Result:        map[string]any{"wallet": wallet, "resync": result},
			}
			human := fmt.Sprintf("Full replay started for %s (%s) on %s. Execution: %s\n", wallet.Name, wallet.ID, result.Syncer, result.ExecutionID)
			if f.dryRun {
				human = fmt.Sprintf("Full replay preview for %s (%s): %d sync-state targets on %s.\n", wallet.Name, wallet.ID, len(result.ClearedState), result.Syncer)
			}
			return outputMutation(cmd, f.jsonOutput, envelope, human)
		},
	}
	addMutationFlags(cmd, &f)
	cmd.Flags().Lookup("dry-run").Usage = "Discover the current syncer and preview state targets without changing the organization"
	return cmd
}
