package operations

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func newOrgInventoryCmd() *op.Definition {
	cmd := &op.Definition{Use: "inventory", Short: "Manage Bitwave organization inventory views"}
	cmd.AddCommand(newOrgInventoryListCmd(), newOrgInventoryCreateCmd(), newOrgInventoryUpdateCmd(), newOrgInventoryUpdatesCmd(), newOrgInventoryCancelCmd(), newOrgInventoryDeleteCmd(), newOrgInventoryAPICmd())
	return cmd
}

func newOrgInventoryListCmd() *op.Definition {
	var orgID string
	cmd := &op.Definition{
		Use: "list", Short: "List organization inventory views",
		RunE: func(cmd *op.Call, _ []string) error {
			orgID, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), orgID)
			views, err := client.InventoryViews(cmd.Context(), orgID)
			if err != nil {
				return fmt.Errorf("list inventory views: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "organization": orgID, "inventoryViews": views})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func newOrgInventoryCreateCmd() *op.Definition {
	var f transactionMutationFlags
	var input string
	cmd := &op.Definition{
		Use: "create", Short: "Create an inventory view from a complete Bitwave JSON request",
		RunE: func(cmd *op.Call, _ []string) error {
			operation := "create-inventory-view"
			request, err := readJSONObject(cmd.Context(), input, cmd.InOrStdin())
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			request, err = useOrgDefaultInventoryPricing(request)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: request})
			}
			if !f.yes {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to create an inventory view without --yes"))
			}
			client := newReportsClient(cmd.Context(), orgID)
			result, err := client.CreateInventoryView(cmd.Context(), orgID, request)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result})
		},
	}
	addMutationFlags(cmd, &f)
	cmd.Flags().StringVarP(&input, "input", "i", "", "Complete inventory-view JSON request, or - for stdin (required)")
	return cmd
}

// useOrgDefaultInventoryPricing mirrors the inventory-view UI. The backend can
// fall back to the organization policy when the field is absent, but persisting
// the explicit value prevents the view from displaying an unknown methodology.
func useOrgDefaultInventoryPricing(request json.RawMessage) (json.RawMessage, error) {
	var object map[string]any
	if err := json.Unmarshal(request, &object); err != nil {
		return nil, fmt.Errorf("decode inventory-view request: %w", err)
	}
	config, ok := object["config"].(map[string]any)
	if !ok {
		config = map[string]any{}
		object["config"] = config
	}
	config["impairmentMethodology"] = "org-default"
	result, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode inventory-view request: %w", err)
	}
	return result, nil
}

func newOrgInventoryUpdateCmd() *op.Definition {
	var f transactionMutationFlags
	var asOf, referenceRun, referenceEndDate string
	var historical bool
	cmd := &op.Definition{
		Use: "update VIEW_ID_OR_NAME", Short: "Start an inventory-view calculation", Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			operation := "update-inventory-view"
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			client := newReportsClient(cmd.Context(), orgID)
			org, err := client.Org(cmd.Context(), orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			asOf, err = resolveInventoryUpdateDate(asOf, org.Timezone, time.Now())
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			views, err := client.InventoryViews(cmd.Context(), orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			view, err := resolveInventoryView(args[0], views)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			if (referenceRun == "") != (referenceEndDate == "") {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("--reference-run and --reference-end-date must be supplied together"))
			}
			request := orgreports.InventoryViewUpdateRequest{RunIDReference: referenceRun, StartingDate: referenceEndDate, EndingDate: asOf, TransferAtHistoricalCost: historical}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: request})
			}
			if !f.yes {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to start an inventory calculation without --yes"))
			}
			result, err := client.TriggerInventoryViewUpdate(cmd.Context(), orgID, view.ID, request)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result})
		},
	}
	addMutationFlags(cmd, &f)
	cmd.Flags().StringVar(&asOf, "as-of", "", "Run end date in YYYY-MM-DD (default: yesterday in the organization timezone)")
	cmd.Flags().StringVar(&referenceRun, "reference-run", "", "Optional prior inventory update ID")
	cmd.Flags().StringVar(&referenceEndDate, "reference-end-date", "", "Prior run end date in YYYY-MM-DD")
	cmd.Flags().BoolVar(&historical, "transfer-at-historical-cost", false, "Set transferAtHistoricalCost on the update request")
	return cmd
}

func newOrgInventoryUpdatesCmd() *op.Definition {
	var orgID string
	cmd := &op.Definition{
		Use: "updates VIEW_ID_OR_NAME", Short: "List inventory calculation runs and errors", Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			orgID, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), orgID)
			views, err := client.InventoryViews(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			view, err := resolveInventoryView(args[0], views)
			if err != nil {
				return err
			}
			updates, err := client.InventoryViewUpdates(cmd.Context(), orgID, view.ID)
			if err != nil {
				return err
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "organization": orgID, "inventoryView": view, "updates": updates})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func newOrgInventoryCancelCmd() *op.Definition {
	var f transactionMutationFlags
	cmd := &op.Definition{
		Use: "cancel VIEW_ID_OR_NAME UPDATE_ID", Short: "Cancel a running inventory calculation", Args: op.ExactArgs(2),
		RunE: func(cmd *op.Call, args []string) error {
			operation := "cancel-inventory-update"
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			client := newReportsClient(cmd.Context(), orgID)
			views, err := client.InventoryViews(cmd.Context(), orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			view, err := resolveInventoryView(args[0], views)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			request := map[string]any{"inventoryViewId": view.ID, "updateId": args[1]}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: request})
			}
			if !f.yes {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to cancel an inventory calculation without --yes"))
			}
			result, err := client.CancelInventoryViewUpdate(cmd.Context(), orgID, view.ID, strings.TrimSpace(args[1]))
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result})
		},
	}
	addMutationFlags(cmd, &f)
	return cmd
}

func newOrgInventoryDeleteCmd() *op.Definition {
	var f transactionMutationFlags
	cmd := &op.Definition{
		Use: "delete VIEW_ID_OR_NAME", Short: "Delete an inventory view", Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			operation := "delete-inventory-view"
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			client := newReportsClient(cmd.Context(), orgID)
			views, err := client.InventoryViews(cmd.Context(), orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			view, err := resolveInventoryView(args[0], views)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: view})
			}
			if !f.yes {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to delete an inventory view without --yes"))
			}
			if err := client.DeleteInventoryView(cmd.Context(), orgID, view.ID); err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: view})
		},
	}
	addMutationFlags(cmd, &f)
	return cmd
}

func resolveInventoryUpdateDate(requested, timezone string, now time.Time) (string, error) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return "", fmt.Errorf("organization timezone %q is invalid: %w", timezone, err)
	}
	maximum := now.In(location).AddDate(0, 0, -1).Format("2006-01-02")
	if strings.TrimSpace(requested) == "" {
		return maximum, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", requested, location)
	if err != nil {
		return "", errors.New("date must use YYYY-MM-DD")
	}
	maxDate, _ := time.ParseInLocation("2006-01-02", maximum, location)
	if parsed.After(maxDate) {
		return "", fmt.Errorf("latest allowed date is %s", maximum)
	}
	return requested, nil
}
