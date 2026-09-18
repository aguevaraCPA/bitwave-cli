package operations

import (
	"fmt"
	"net/http"
	"sort"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func pricingOperations() []adminOperation {
	return []adminOperation{
		// Pricing history and token discovery.
		pricingGraphQL("history", "available-tokens", "tokens", "List tokens available in pricing history", gqlPricingAvailableTokens),
		withInput(pricingGraphQL("history", "historic-prices", "history", "Query paginated historic prices and pricing steps", gqlHistoricPrices)),
		withService(withQuery(pricingREST("history", "transaction-token-lookups", "token-lookups", "List or search transaction token tickers", http.MethodGet, "/orgs/{org}/lookups"), "fieldName=amountCurrencyName"), orgreports.APIServiceTransactions),
		withInput(pricingREST("history", "pricing-report-export", "export", "Export pricing history", http.MethodPost, "/orgs/{org}/pricing-report")),
		withInput(pricingREST("history", "price-cache-clear", "cache-clear", "Clear failed or selected pricing history", http.MethodDelete, "/orgs/{org}/cache-clear")),
		withInput(pricingGraphQL("history", "price-cache-clear-graphql", "cache-clear-graphql", "Clear pricing history through the GraphQL contract", gqlClearPriceCache)),
		pricingREST("history", "price-get", "price PRICE_ID", "Get a stored price by ID", http.MethodGet, "/orgs/{org}/prices/{priceId}", "priceId"),
		pricingREST("history", "asset-price", "price-asset", "Price an asset conversion using query parameters", http.MethodPost, "/orgs/{org}/prices"),
		pricingREST("history", "transaction-price", "price-transaction TRANSACTION_ID", "Price or reprice one transaction", http.MethodPost, "/orgs/{org}/transactions/{transactionId}/price", "transactionId"),

		// Pricing rules, including the default rule and audit history.
		pricingGraphQL("rules", "pricing-rules-list", "list", "List asset-specific pricing rules", gqlPricingRulesList),
		pricingGraphQL("rules", "default-pricing-rule-get", "default", "Get the organization default pricing rule", gqlDefaultPricingRule),
		withInput(pricingGraphQL("rules", "pricing-rule-create", "create", "Create an asset-specific pricing rule", gqlPricingRuleCreate)),
		withInput(pricingGraphQL("rules", "pricing-rule-update", "update", "Update, archive, or restore a pricing rule", gqlPricingRuleUpdate)),
		withInput(pricingGraphQL("rules", "default-pricing-rule-create", "default-create", "Create or replace the default pricing rule", gqlDefaultPricingRuleCreate)),
		pricingREST("rules", "pricing-rule-audit-log", "audit RULE_ID", "Get a pricing rule's audit history", http.MethodGet, "/orgs/{org}/pricing-directives/{ruleId}/audit-log", "ruleId"),

		// Pricing contexts and routes.
		pricingREST("contexts", "pricing-contexts-list", "list", "List pricing contexts", http.MethodGet, "/orgs/{org}/pricing-contexts"),
		withInput(pricingREST("contexts", "pricing-context-create", "create", "Create a pricing context", http.MethodPost, "/orgs/{org}/pricing-contexts")),
		withInput(pricingREST("contexts", "pricing-context-update", "update CONTEXT_ID", "Replace pricing context fields", http.MethodPut, "/orgs/{org}/pricing-contexts/{contextId}", "contextId")),
		withInput(pricingREST("contexts", "pricing-context-status-update", "status CONTEXT_ID", "Enable or disable a pricing context", http.MethodPatch, "/orgs/{org}/pricing-contexts/{contextId}", "contextId")),
		pricingREST("contexts", "pricing-context-routes-list", "routes", "List pricing context routes", http.MethodGet, "/orgs/{org}/pricing-context-routes"),
		withInput(pricingREST("contexts", "pricing-context-route-create", "route-create", "Create a pricing context route", http.MethodPost, "/orgs/{org}/pricing-context-routes")),
		withInput(pricingREST("contexts", "pricing-context-route-update", "route-update ROUTE_ID", "Replace pricing context route fields", http.MethodPut, "/orgs/{org}/pricing-context-routes/{routeId}", "routeId")),
		withInput(pricingREST("contexts", "pricing-context-route-status", "route-status ROUTE_ID", "Enable or disable a pricing context route", http.MethodPatch, "/orgs/{org}/pricing-context-routes/{routeId}", "routeId")),

		// Rate tables live on API2.
		withService(pricingREST("rate-tables", "rate-tables-list", "list", "List rate tables", http.MethodGet, "/v2/orgs/{org}/rate-tables"), orgreports.APIServiceAPI2),
		withService(withInput(pricingREST("rate-tables", "rate-table-create", "create", "Create a rate table", http.MethodPost, "/v2/orgs/{org}/rate-tables")), orgreports.APIServiceAPI2),
		withService(pricingREST("rate-tables", "rate-table-rows-list", "rows RATE_TABLE_ID", "List paginated rate-table rows", http.MethodGet, "/v2/orgs/{org}/rate-tables/{rateTableId}/paginated", "rateTableId"), orgreports.APIServiceAPI2),
		withService(withInput(pricingREST("rate-tables", "rate-table-rows-create", "rows-create RATE_TABLE_ID", "Add rates to a rate table", http.MethodPost, "/v2/orgs/{org}/rate-tables/{rateTableId}/rows", "rateTableId")), orgreports.APIServiceAPI2),
		withService(withInput(pricingREST("rate-tables", "rate-table-rows-delete", "rows-delete RATE_TABLE_ID", "Delete rate-table rows", http.MethodDelete, "/v2/orgs/{org}/rate-tables/{rateTableId}/rows", "rateTableId")), orgreports.APIServiceAPI2),
		withService(pricingREST("rate-tables", "rate-table-export", "export RATE_TABLE_ID", "Export a rate table as CSV", http.MethodGet, "/v2/orgs/{org}/rate-tables/{rateTableId}/export", "rateTableId"), orgreports.APIServiceAPI2),
		withService(withInput(pricingREST("rate-tables", "rate-table-load", "load RATE_TABLE_ID", "Load rates from BigQuery", http.MethodPost, "/v2/orgs/{org}/rate-tables/{rateTableId}/load", "rateTableId")), orgreports.APIServiceAPI2),

		// BigQuery source discovery used by rate-table import.
		withService(pricingREST("rate-tables", "rate-table-data-sources", "data-sources", "List data sources available for rate-table import", http.MethodGet, "/v3/orgs/{org}/data-sources"), orgreports.APIServicePlatform),
		withService(pricingREST("rate-tables", "rate-table-data-source-get", "data-source DATA_SOURCE_ID", "Get a rate-table import data source", http.MethodGet, "/v3/orgs/{org}/data-sources/{dataSourceId}", "dataSourceId"), orgreports.APIServicePlatform),
		pricingREST("rate-tables", "rate-table-data-sources-legacy", "data-sources-legacy", "List import data sources for organizations not using data-core-v2", http.MethodGet, "/v3/orgs/{org}/data-sources"),
		pricingREST("rate-tables", "rate-table-data-source-get-legacy", "data-source-legacy DATA_SOURCE_ID", "Get a legacy rate-table import data source", http.MethodGet, "/v3/orgs/{org}/data-sources/{dataSourceId}", "dataSourceId"),
	}
}

func pricingREST(area, name, use, short, method, path string, args ...string) adminOperation {
	operation := adminRESTOperation(area, name, use, short, method, path)
	operation.ArgumentNames = args
	return operation
}

func pricingGraphQL(area, name, use, short, document string) adminOperation {
	return withService(adminGraphQLOperation(area, name, use, short, document), orgreports.APIServicePlatform)
}

func newOrgPricingCmd() *op.Definition {
	cmd := &op.Definition{
		Use:   "pricing",
		Short: "Access Bitwave pricing history, rules, contexts, and rate tables",
		Long: `Access the complete backend surface used by Bitwave Pricing pages.

Reads accept repeatable --query key=value filters. Mutations accept JSON through
--input or --data, support --dry-run, and require --yes.`,
	}
	cmd.AddCommand(newOrgPricingCapabilitiesCmd())
	areas := make(map[string]*op.Definition)
	for _, operation := range pricingOperations() {
		area := areas[operation.Area]
		if area == nil {
			area = &op.Definition{Use: operation.Area, Short: "Pricing " + operation.Area + " endpoints"}
			areas[operation.Area] = area
			cmd.AddCommand(area)
		}
		area.AddCommand(newAdminOperationCmd(operation))
	}
	return cmd
}

func newOrgPricingCapabilitiesCmd() *op.Definition {
	var area string
	var jsonOutput bool
	cmd := &op.Definition{
		Use: "capabilities", Short: "List every pricing operation exposed by the CLI",
		RunE: func(cmd *op.Call, _ []string) error {
			operations := pricingOperations()
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
					"command":     "bitwave pricing " + operation.Area + " " + operation.Use,
					"description": operation.Short, "protocol": operation.Protocol,
					"service": operation.Service, "method": operation.Method, "path": operation.Path,
				})
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "operations": items, "total": len(items)})
			}
			for _, item := range items {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-36s %s\n", item["area"], item["operation"], item["description"])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&area, "area", "", "Limit results to one pricing area")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit machine-readable JSON")
	return cmd
}

const gqlPricingAvailableTokens = `query PricingCLIAvailableTokens($orgId: ID!) { availableTokens(orgId: $orgId) }`
const gqlHistoricPrices = `query PricingCLIHistoricPrices($orgId: ID!, $coin: String!, $fromTimestampSec: Int, $toTimestampSec: Int, $pageSize: Int, $pageToken: String) { historicPrices(orgId: $orgId, coin: $coin, fromTimestampSec: $fromTimestampSec, toTimestampSec: $toTimestampSec, pageSize: $pageSize, pageToken: $pageToken) { hasMore nextPageToken prices { ... on HistoricPriceSuccess { timestampSEC status price { type ... on PriceDetailCandlestick { price open close high low volume } ... on PriceDetailOverride { price } ... on PriceDetailCoarseGrain { price } } steps { detail description source type } } ... on HistoricPriceFailure { timestampSEC status steps { detail description source type } } } } }`
const gqlClearPriceCache = `mutation PricingCLIClearCache($orgId: ID!, $coin: String, $dateFrom: String, $dateTo: String, $clearOnlyFailedPrices: Boolean, $clearAllCache: Boolean) { clearPriceCache(orgId: $orgId, coin: $coin, dateFrom: $dateFrom, dateTo: $dateTo, clearOnlyFailedPrices: $clearOnlyFailedPrices, clearAllCache: $clearAllCache) }`
const gqlPricingRuleFields = `id assetId ticker isValid invalidReason priority application action { type price sourceOrdering { type exchange fiatToCoinTickerMapping conversionAssetTicker conversionPricingDirective { type price source { type exchange fiatToCoinTickerMapping } } rateTableId sourceLocatorId sourceLocator } overrideTicker percentage } fromSEC toSEC`
const gqlDefaultPricingRuleFields = `methodology resolution action { type price sourceOrdering { type exchange fiatToCoinTickerMapping rateTableId sourceLocatorId sourceLocator } }`
const gqlPricingRulesList = `query PricingCLIRules($orgId: ID!) { pricingRules(orgId: $orgId) { ` + gqlPricingRuleFields + ` } }`
const gqlDefaultPricingRule = `query PricingCLIDefaultRule($orgId: ID!) { defaultPricingRule(orgId: $orgId) { ` + gqlDefaultPricingRuleFields + ` } }`
const gqlPricingRuleCreate = `mutation PricingCLIRuleCreate($orgId: ID!, $rule: PricingRuleInput!) { createPricingRule(orgId: $orgId, rule: $rule) { success errors } }`
const gqlPricingRuleUpdate = `mutation PricingCLIRuleUpdate($orgId: ID!, $rule: PricingRuleInput!) { updatePricingRule(orgId: $orgId, rule: $rule) { success errors } }`
const gqlDefaultPricingRuleCreate = `mutation PricingCLIDefaultRuleCreate($orgId: ID!, $rule: DefaultPricingRuleInput!) { createDefaultPricingRule(orgId: $orgId, rule: $rule) { success errors } }`
