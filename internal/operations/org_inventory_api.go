package operations

import (
	"fmt"
	"net/http"
	"sort"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"
)

// inventoryAPIOperations is the complete Bitwave endpoint surface used by the
// Inventory Views page. Query parameters remain generic because the UI exposes
// large, evolving filter sets for actions, lots, balances, and reports.
func inventoryAPIOperations() []adminOperation {
	return []adminOperation{
		// Views and calculation runs.
		inventoryREST("views", "views-list", "list", "List inventory views", http.MethodGet, "/orgs/{org}/inventory-views"),
		inventoryREST("views", "view-get", "get VIEW_ID", "Get one inventory view", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}", "viewId"),
		withInput(inventoryREST("views", "view-create", "create", "Create an inventory view", http.MethodPost, "/orgs/{org}/inventory-views")),
		withInput(inventoryREST("views", "view-properties-update", "properties-update VIEW_ID", "Update inventory view properties", http.MethodPatch, "/orgs/{org}/inventory-views/{viewId}", "viewId")),
		inventoryREST("views", "view-delete", "delete VIEW_ID", "Delete an inventory view", http.MethodDelete, "/orgs/{org}/inventory-views/{viewId}", "viewId"),
		inventoryREST("runs", "view-updates-list", "list VIEW_ID", "List inventory calculation runs and errors", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/updates", "viewId"),
		inventoryREST("runs", "view-update-trigger", "trigger VIEW_ID", "Trigger the basic inventory update endpoint", http.MethodPost, "/orgs/{org}/inventory-views/{viewId}/updates", "viewId"),
		withInput(inventoryREST("runs", "view-update-enhanced", "trigger-enhanced VIEW_ID", "Trigger an inventory update with an explicit request", http.MethodPost, "/orgs/{org}/inventory-views/{viewId}/update-requests", "viewId")),
		inventoryREST("runs", "view-update-incremental", "trigger-incremental VIEW_ID RUN_ID START_DATE", "Trigger an incremental inventory update", http.MethodPost, "/orgs/{org}/inventory-views/{viewId}/{runId}/{startDate}/updates", "viewId", "runId", "startDate"),
		inventoryREST("runs", "view-update-cancel", "cancel VIEW_ID UPDATE_ID", "Cancel an inventory calculation", http.MethodPost, "/orgs/{org}/inventory-views/{viewId}/{updateId}/cancel", "viewId", "updateId"),
		inventoryREST("runs", "view-update-download-links", "download-links VIEW_ID UPDATE_ID", "Get inventory-run result download links", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/updates/{updateId}/download-links", "viewId", "updateId"),
		inventoryREST("views", "view-rows", "rows VIEW_ID", "Get raw inventory rows", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/rows", "viewId"),

		// Dashboard balances.
		inventoryREST("balances", "view-balance", "get VIEW_ID", "Get inventory view balances", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/balance", "viewId"),
		inventoryREST("balances", "view-balance-paginated", "get-paginated VIEW_ID", "Get paginated inventory view balances", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/balance-paginated", "viewId"),
		inventoryREST("balances", "view-counts", "counts VIEW_ID", "Get inventory action and lot counts", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/counts", "viewId"),

		// Actions table.
		inventoryREST("actions", "action-columns", "columns VIEW_ID", "List available action columns", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/actions/columns", "viewId"),
		inventoryREST("actions", "action-column-values", "column-values VIEW_ID COLUMN_ID", "List unique action-column values", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/actions/columns/{columnId}/unique", "viewId", "columnId"),
		inventoryREST("actions", "view-actions", "list VIEW_ID", "List or export filtered inventory actions", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/actions", "viewId"),

		// Lots table.
		inventoryREST("lots", "lot-columns", "columns VIEW_ID", "List available lot columns", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/lots/columns", "viewId"),
		inventoryREST("lots", "lot-column-values", "column-values VIEW_ID COLUMN_ID", "List unique lot-column values", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/lots/columns/{columnId}/unique", "viewId", "columnId"),
		inventoryREST("lots", "view-lots", "list VIEW_ID", "List or export filtered inventory lots", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/lots", "viewId"),

		// Inventory groups and underlying inventories.
		inventoryREST("groups", "inventory-groups-list", "list", "List inventory groups", http.MethodGet, "/orgs/{org}/inventory-groups"),
		inventoryREST("groups", "inventory-group-get", "get GROUP_ID", "Get one inventory group", http.MethodGet, "/orgs/{org}/inventory-groups/{groupId}", "groupId"),
		withInput(inventoryREST("groups", "inventory-group-create", "create", "Create an inventory group", http.MethodPost, "/orgs/{org}/inventory-groups")),
		withInput(inventoryREST("groups", "inventory-group-update", "update GROUP_ID", "Update an inventory group and mappings", http.MethodPatch, "/orgs/{org}/inventory-groups/{groupId}", "groupId")),
		inventoryREST("groups", "inventory-group-delete", "delete GROUP_ID", "Delete an inventory group", http.MethodDelete, "/orgs/{org}/inventory-groups/{groupId}", "groupId"),
		inventoryGraphQL("groups", "inventory-groups-ui-list", "ui-list", "List inventory groups through the UI GraphQL contract", gqlInventoryGroupsList),
		withInput(inventoryGraphQL("groups", "inventory-group-ui-create", "ui-create", "Create an inventory group through the UI GraphQL contract", gqlInventoryGroupCreate)),
		withInput(inventoryGraphQL("groups", "inventory-group-ui-update", "ui-update GROUP_ID", "Update inventory mappings through the UI GraphQL contract", gqlInventoryGroupUpdate, "inventoryGroupId")),
		inventoryGraphQL("inventories", "inventories-list", "list", "List underlying inventories", gqlInventoriesList),
		withInput(inventoryGraphQL("inventories", "inventory-create", "create", "Create an underlying inventory", gqlInventoryCreate)),
		inventoryREST("inventories", "inventory-delete", "delete INVENTORY_ID", "Delete an underlying inventory", http.MethodDelete, "/orgs/{org}/inventories/{inventoryId}", "inventoryId"),

		// Inventory view configuration stored through GraphQL.
		withInput(inventoryGraphQL("configuration", "view-config-update", "update VIEW_ID", "Update inventory valuation configuration", gqlInventoryViewConfigUpdate, "inventoryId")),
		inventoryREST("configuration", "label-definitions", "labels", "List custom label definitions available to inventory views", http.MethodGet, "/orgs/{org}/labels"),

		// Reallocation.
		withInput(inventoryREST("reallocation", "reallocation-report-run", "report", "Run an inventory reallocation report", http.MethodPost, "/orgs/{org}/inventory-views/reports/reallocation-report")),
		withInput(inventoryREST("reallocation", "view-reallocation-trigger", "trigger VIEW_ID", "Trigger reallocation for an inventory view", http.MethodPost, "/orgs/{org}/inventory-views/{viewId}/reallocations", "viewId")),

		// Reports reachable from Inventory Views.
		inventoryREST("reports", "actions-je-report", "actions-je VIEW_ID", "Run the Actions Journal Entry report", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/actions-je-report", "viewId"),
		inventoryREST("reports", "actions-summary-report", "actions-summary VIEW_ID", "Run the Actions Summary report", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/actions-summary-report", "viewId"),
		inventoryREST("reports", "actions-tb-report", "actions-trial-balance VIEW_ID", "Run the Actions Trial Balance report", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/actions-tb-report", "viewId"),
		inventoryREST("reports", "advanced-actions-report", "advanced-actions VIEW_ID", "Run the Expanded Actions report", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/advanced-action-report", "viewId"),
		inventoryREST("reports", "advanced-cost-basis-roll-forward", "advanced-cost-basis VIEW_ID", "Run the advanced cost-basis rollforward", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/advanced-cost-basis-roll-forward", "viewId"),
		inventoryREST("reports", "balance-check-report", "balance-check", "Run the balance-check report", http.MethodGet, "/orgs/{org}/inventory-views/reports/balance-check-report"),
		inventoryREST("reports", "balance-check-report-paginated", "balance-check-paginated", "Run the paginated balance-check report", http.MethodGet, "/orgs/{org}/inventory-views/reports/balance-check-report-paginated"),
		withInput(inventoryREST("reports", "balance-check-report-job", "balance-check-job", "Start the balance-check report job", http.MethodPost, "/orgs/{org}/inventory-views/reports/balance-check-report-job")),
		inventoryREST("reports", "cost-basis-roll-forward", "cost-basis VIEW_ID", "Run the cost-basis rollforward", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/cost-basis-roll-forward", "viewId"),
		inventoryREST("reports", "enhanced-gain-loss", "enhanced-gain-loss VIEW_ID", "Run the enhanced gain/loss report", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/enhanced-gain-loss", "viewId"),
		inventoryREST("reports", "je-reclass", "je-reclass VIEW_ID", "Run the JE reclass report", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/je-reclass", "viewId"),
		inventoryREST("reports", "ripple-cost-basis-roll-forward", "expanded-cost-basis VIEW_ID", "Run the expanded cost-basis report", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/ripple-cost-basis-roll-forward", "viewId"),
		inventoryREST("reports", "sage-erp-je-report", "sage-je VIEW_ID", "Run the Sage ERP JE report", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/sage-erp-je-report", "viewId"),
		inventoryREST("reports", "soda-report", "soda VIEW_ID", "Run the SODA report", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/soda-report", "viewId"),
		inventoryREST("reports", "wallet-balance-report", "wallet-balance VIEW_ID", "Run the wallet balance inventory report", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/reports/wallet-balance-report", "viewId"),
		inventoryREST("reports", "gain-loss-summary", "gain-loss-summary VIEW_ID", "Get the inventory gain/loss summary", http.MethodGet, "/orgs/{org}/inventory-views/{viewId}/gain-loss-summary", "viewId"),

		// Export IDs returned by actions, lots, and inventory reports.
		inventoryREST("exports", "inventory-export-get", "get EXPORT_ID", "Get or download an inventory export", http.MethodGet, "/v2/orgs/{org}/exports/{exportId}", "exportId"),
	}
}

func inventoryREST(area, name, use, short, method, path string, args ...string) adminOperation {
	operation := adminRESTOperation(area, name, use, short, method, path)
	operation.ArgumentNames = args
	return operation
}

func inventoryGraphQL(area, name, use, short, document string, args ...string) adminOperation {
	return adminGraphQLOperation(area, name, use, short, document, args...)
}

func newOrgInventoryAPICmd() *op.Definition {
	cmd := &op.Definition{
		Use:   "api",
		Short: "Access the complete Inventory Views backend surface",
		Long: `Access the same Bitwave endpoints used by the Inventory Views page.

Read operations accept repeatable --query key=value filters. Mutations accept
JSON through --input or --data, support --dry-run, and require --yes.`,
	}
	cmd.AddCommand(newOrgInventoryCapabilitiesCmd())
	areas := make(map[string]*op.Definition)
	for _, operation := range inventoryAPIOperations() {
		area := areas[operation.Area]
		if area == nil {
			area = &op.Definition{Use: operation.Area, Short: "Inventory " + operation.Area + " endpoints"}
			areas[operation.Area] = area
			cmd.AddCommand(area)
		}
		area.AddCommand(newAdminOperationCmd(operation))
	}
	return cmd
}

func newOrgInventoryCapabilitiesCmd() *op.Definition {
	var area string
	var jsonOutput bool
	cmd := &op.Definition{
		Use:   "capabilities",
		Short: "List every Inventory Views operation exposed by the CLI",
		RunE: func(cmd *op.Call, _ []string) error {
			operations := inventoryAPIOperations()
			sort.Slice(operations, func(i, j int) bool {
				if operations[i].Area == operations[j].Area {
					return operations[i].Name < operations[j].Name
				}
				return operations[i].Area < operations[j].Area
			})
			items := make([]map[string]any, 0, len(operations))
			for _, operation := range operations {
				if area != "" && operation.Area != area {
					continue
				}
				items = append(items, map[string]any{
					"area": operation.Area, "operation": operation.Name,
					"command":     "bitwave inventory api " + operation.Area + " " + operation.Use,
					"description": operation.Short, "protocol": operation.Protocol,
					"method": operation.Method, "path": operation.Path,
				})
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "operations": items, "total": len(items)})
			}
			for _, item := range items {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-15s %-38s %s\n", item["area"], item["operation"], item["description"])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&area, "area", "", "Limit results to one inventory area")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit machine-readable JSON")
	return cmd
}

const gqlInventoriesList = `query InventoryCLIInventories($orgId: ID!) { inventories(orgId: $orgId) { id orgId inventoryGroupId name description } }`
const gqlInventoryCreate = `mutation InventoryCLICreate($orgId: ID!, $inventory: CreateInventoryInput, $wallets: [String]) { createInventory(orgId: $orgId, inventory: $inventory, wallets: $wallets) { id orgId inventoryGroupId name description } }`
const gqlInventoryGroupsList = `query InventoryCLIGroups($orgId: ID!) { inventoryGroups(orgId: $orgId) { id orgId name description accountingConnectionIds inventoryIdsToGlAccountIds { inventoryId glAccountId } walletIdsToInventoryIds { walletId inventoryId } } }`
const gqlInventoryGroupCreate = `mutation InventoryCLIGroupCreate($orgId: ID!, $inventoryGroup: CreateInventoryGroupInput) { createInventoryGroup(orgId: $orgId, inventoryGroup: $inventoryGroup) { id orgId name description } }`
const gqlInventoryGroupUpdate = `mutation InventoryCLIGroupUpdate($orgId: ID!, $inventoryGroupId: ID!, $update: UpdateInventoryGroupInput) { updateInventoryGroup(orgId: $orgId, inventoryGroupId: $inventoryGroupId, update: $update) { id orgId name description accountingConnectionIds inventoryIdsToGlAccountIds { inventoryId glAccountId } walletIdsToInventoryIds { walletId inventoryId } } }`
const gqlInventoryViewConfigUpdate = `mutation InventoryCLIConfigUpdate($orgId: ID!, $inventoryId: ID!, $inventoryConfig: InventoryViewConfigInput!) { updateInventoryViewConfig(orgId: $orgId, inventoryId: $inventoryId, inventoryConfig: $inventoryConfig) { success errors } }`
