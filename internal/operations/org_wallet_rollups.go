package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func newOrgWalletsRollupCmd() *op.Definition {
	cmd := &op.Definition{Use: "rollup", Short: "Inspect or configure Babel wallet rollups"}
	cmd.AddCommand(newOrgWalletRollupGetCmd(), newOrgWalletRollupSetCmd())
	return cmd
}

func newOrgWalletRollupGetCmd() *op.Definition {
	var orgID string
	cmd := &op.Definition{
		Use: "get WALLET_ID_OR_NAME", Short: "Get a wallet's Babel rollup configuration", Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			orgID, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), orgID)
			wallet, err := resolveOrganizationWallet(cmd, client, orgID, args[0])
			if err != nil {
				return err
			}
			rollup, err := client.WalletRollup(cmd.Context(), orgID, wallet.ID)
			if err != nil {
				return err
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "organization": orgID, "wallet": wallet, "rollup": rollup})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func newOrgWalletRollupSetCmd() *op.Definition {
	var f transactionMutationFlags
	var input string
	cmd := &op.Definition{
		Use: "set WALLET_ID_OR_NAME", Short: "Create or replace a wallet's Babel rollup configuration", Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			operation := "set-wallet-rollup"
			request, err := readWalletRollupRequest(cmd.Context(), input, cmd.InOrStdin())
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			client := newReportsClient(cmd.Context(), orgID)
			wallet, err := resolveOrganizationWallet(cmd, client, orgID, args[0])
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: request})
			}
			if !f.yes {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to change the wallet rollup without --yes"))
			}
			if err := client.UpsertWalletRollup(cmd.Context(), orgID, wallet.ID, request); err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: map[string]any{"wallet": wallet, "ruleCount": len(request.Rules)}})
		},
	}
	addMutationFlags(cmd, &f)
	cmd.Flags().StringVarP(&input, "input", "i", "", "Complete wallet rollup JSON request, or - for stdin (required)")
	return cmd
}

func readWalletRollupRequest(ctx context.Context, path string, stdin io.Reader) (orgreports.WalletRollupRequest, error) {
	var request orgreports.WalletRollupRequest
	if strings.TrimSpace(path) == "" {
		return request, errors.New("--input is required")
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 4<<20))
	} else {
		data, err = op.RuntimeFrom(ctx).Files.ReadFile(path)
	}
	if err != nil {
		return request, err
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return request, err
	}
	if request.Address == "" || request.Type == "" || len(request.Rules) == 0 {
		return request, errors.New("rollup request requires address, type, and at least one rule")
	}
	return request, nil
}

func resolveOrganizationWallet(cmd *op.Call, client *orgreports.Client, orgID, value string) (*orgreports.OrgWallet, error) {
	wallets, err := client.OrgWallets(cmd.Context(), orgID)
	if err != nil {
		return nil, fmt.Errorf("list organization wallets: %w", err)
	}
	for i := range wallets {
		if wallets[i].ID == value || strings.EqualFold(wallets[i].Name, value) {
			return &wallets[i], nil
		}
	}
	return nil, fmt.Errorf("wallet %q was not found", value)
}
