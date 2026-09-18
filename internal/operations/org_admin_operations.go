package operations

import (
	"net/http"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func adminRESTOperation(area, name, use, short, method, path string) adminOperation {
	return adminOperation{Area: area, Name: name, Use: use, Short: short, Protocol: adminREST, Service: orgreports.APIServiceCore, Method: method, Path: path}
}

func adminGraphQLOperation(area, name, use, short, document string, args ...string) adminOperation {
	return adminOperation{Area: area, Name: name, Use: use, Short: short, Protocol: adminGraphQL, Service: orgreports.APIServiceApp, Method: http.MethodPost, Document: document, ArgumentNames: args}
}

func withInput(operation adminOperation) adminOperation {
	operation.InputRequired = true
	return operation
}
func withETag(operation adminOperation) adminOperation { operation.AutoETag = true; return operation }
func withFeature(operation adminOperation, flag string) adminOperation {
	operation.FeatureFlag = flag
	return operation
}
func withService(operation adminOperation, service string) adminOperation {
	operation.Service = service
	return operation
}
func withDefaults(operation adminOperation, defaults map[string]any) adminOperation {
	operation.Defaults = defaults
	return operation
}
func withQuery(operation adminOperation, query ...string) adminOperation {
	operation.DefaultQuery = query
	return operation
}
func withNotes(operation adminOperation, notes string) adminOperation {
	operation.Notes = notes
	return operation
}

func adminOperations() []adminOperation {
	operations := []adminOperation{
		// Organization
		adminRESTOperation("organization", "organization-get", "get", "Get organization settings and the current ETag", http.MethodGet, "/v3/orgs/{org}"),
		withETag(withInput(adminRESTOperation("organization", "organization-update", "update", "Update name, fee handling, display timezone, or engine version", http.MethodPatch, "/v3/orgs/{org}/settings/admin"))),

		// Subsidiaries / organization structure
		adminRESTOperation("subsidiaries", "subsidiaries-list", "list", "List the complete organization hierarchy, including deleted entities", http.MethodGet, "/v3/orgs/{org}/structure"),
		withInput(adminRESTOperation("subsidiaries", "subsidiary-create", "create", "Create a subsidiary or other organization-structure entity", http.MethodPost, "/v3/orgs/{org}/structure")),
		adminRESTOperation("subsidiaries", "subsidiary-get", "get ENTITY_ID", "Get one organization-structure entity", http.MethodGet, "/v3/orgs/{org}/structure/{entityId}"),
		withInput(adminRESTOperation("subsidiaries", "subsidiary-update", "update ENTITY_ID", "Update a subsidiary, mappings, currency, accounts, or metadata", http.MethodPatch, "/v3/orgs/{org}/structure/{entityId}")),
		withInput(adminRESTOperation("subsidiaries", "subsidiary-move", "move ENTITY_ID", "Move an entity within the organization hierarchy", http.MethodPut, "/v3/orgs/{org}/structure/{entityId}:move")),
		withInput(adminRESTOperation("subsidiaries", "subsidiary-delete", "delete ENTITY_ID", "Soft-delete an organization-structure entity", http.MethodPatch, "/v3/orgs/{org}/structure/{entityId}")),
		withInput(adminRESTOperation("subsidiaries", "subsidiary-restore", "restore ENTITY_ID", "Restore a soft-deleted organization-structure entity", http.MethodPatch, "/v3/orgs/{org}/structure/{entityId}")),

		// Accounting setup
		adminGraphQLOperation("accounting-setup", "accounting-setup-get", "get", "Read organization accounting defaults", gqlAccountingSetupGet),
		withInput(adminGraphQLOperation("accounting-setup", "accounting-setup-update", "update", "Update inference, collapsing, reconciliation, network, AR, AP, and fee defaults", gqlAccountingSetupUpdate)),

		// Billing and credits
		withService(adminRESTOperation("billing", "billing-transaction-usage", "transaction-usage", "Get daily transaction usage for a year range", http.MethodGet, "/v3/orgs/{org}/usage/transaction-counts"), orgreports.APIServicePlatform),
		withFeature(withService(adminRESTOperation("billing", "billing-credits-usage", "credits-usage", "Get credit balance, grants, top-ups, and dimension spend", http.MethodGet, "/v3/orgs/{org}/usage/credits"), orgreports.APIServicePlatform), "billing-credits"),
		withFeature(withService(withInput(adminRESTOperation("billing", "billing-credits-checkout", "credits-checkout", "Create a checkout session to purchase credits", http.MethodPost, "/v3/orgs/{org}/credits/checkout")), orgreports.APIServicePlatform), "billing-credits"),
		withFeature(withService(withInput(adminRESTOperation("billing", "billing-payment-method", "payment-method", "Create a billing-card setup session", http.MethodPost, "/v3/orgs/{org}/credits/payment-method")), orgreports.APIServicePlatform), "billing-credits"),
		withFeature(withService(withInput(adminRESTOperation("billing", "billing-auto-top-up", "auto-top-up", "Update automatic credit top-up settings", http.MethodPut, "/v3/orgs/{org}/credits/auto-top-up")), orgreports.APIServicePlatform), "billing-credits"),

		// Accounting connections, including provider-specific settings.
		adminGraphQLOperation("connections", "connections-list", "list", "List accounting connections with complete provider settings and sync status", gqlConnectionsList),
		withInput(adminGraphQLOperation("connections", "connection-create", "create", "Create any UI-supported accounting connection", gqlConnectionCreate)),
		withInput(adminGraphQLOperation("connections", "connection-setup", "setup CONNECTION_ID", "Finish or edit default holding and fee account setup", gqlConnectionSetup, "connectionId")),
		adminGraphQLOperation("connections", "connection-sync", "sync CONNECTION_ID", "Start an accounting connection sync", gqlConnectionSync, "connectionId"),
		adminGraphQLOperation("connections", "connection-disconnect", "disconnect CONNECTION_ID", "Disconnect QuickBooks or Xero", gqlConnectionDisconnect, "connectionId"),
		adminGraphQLOperation("connections", "connection-reconnect", "reconnect CONNECTION_ID", "Reconnect a disabled OAuth accounting connection", gqlConnectionReconnect, "connectionId"),
		withInput(adminGraphQLOperation("connections", "connection-update", "update CONNECTION_ID", "Update default state, name, setup, disabled state, skip steps, or provider settings", gqlConnectionUpdate, "connectionId")),
		withInput(adminGraphQLOperation("connections", "netsuite-create", "netsuite-create", "Create a NetSuite connection with credentials and account mappings", gqlConnectionCreate)),
		withInput(adminGraphQLOperation("connections", "netsuite-update-settings", "netsuite-update-settings CONNECTION_ID", "Update every nested NetSuite setting", gqlConnectionUpdate, "connectionId")),
		withInput(adminGraphQLOperation("connections", "netsuite-update-credentials", "netsuite-update-credentials CONNECTION_ID", "Replace NetSuite consumer and token credentials", gqlUpdateConnectionCredentials, "connectionId")),
		withInput(withNotes(adminGraphQLOperation("connections", "netsuite-custom-segments", "netsuite-custom-segments CONNECTION_ID", "Create, replace, or remove NetSuite custom segment definitions", gqlConnectionUpdate, "connectionId"), "Supply the complete connectionSpecificFields object; use connections list first so unrelated NetSuite settings are preserved.")),
		withInput(adminGraphQLOperation("connections", "netsuite-custom-fields", "netsuite-custom-fields CONNECTION_ID", "Manage NetSuite custom body and line fields", gqlConnectionUpdate, "connectionId")),
		withInput(adminGraphQLOperation("connections", "netsuite-saved-searches", "netsuite-saved-searches CONNECTION_ID", "Manage NetSuite bill, invoice, vendor, and customer saved searches", gqlConnectionUpdate, "connectionId")),
		withInput(adminGraphQLOperation("connections", "netsuite-custom-records", "netsuite-custom-records CONNECTION_ID", "Manage NetSuite custom records and reference mappings", gqlConnectionUpdate, "connectionId")),
		withInput(adminGraphQLOperation("connections", "netsuite-metadata-mappers", "netsuite-metadata-mappers CONNECTION_ID", "Manage NetSuite metadata mappers and accessors", gqlConnectionUpdate, "connectionId")),
		withInput(adminGraphQLOperation("connections", "netsuite-subsidiary-routing", "netsuite-subsidiary-routing CONNECTION_ID", "Manage invoice and bill subsidiary routing", gqlConnectionUpdate, "connectionId")),

		// System jobs
		adminGraphQLOperation("system-jobs", "system-jobs-list", "list", "List and paginate system jobs with status, parameters, timing, and errors", gqlSystemJobsList),
		withInput(adminGraphQLOperation("system-jobs", "system-job-run", "run", "Run any Admin System Jobs action with its full filter contract", gqlSystemJobRun)),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-delete-transactions", "delete-transactions", "Delete matching transactions", gqlSystemJobRun), bulkTransactionDefaults("delete")),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-ignore", "ignore", "Ignore matching transactions", gqlSystemJobRun), bulkTransactionDefaults("ignore")),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-unignore", "unignore", "Unignore matching transactions", gqlSystemJobRun), bulkTransactionDefaults("unignore")),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-reconcile", "reconcile", "Reconcile matching transactions", gqlSystemJobRun), bulkTransactionDefaults("reconcile")),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-mark-reconciled", "mark-reconciled", "Mark matching transactions reconciled", gqlSystemJobRun), bulkTransactionDefaults("markReconciled")),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-mark-unreconciled", "mark-unreconciled", "Mark matching transactions unreconciled", gqlSystemJobRun), bulkTransactionDefaults("markUnreconciled")),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-uncategorize", "uncategorize", "Uncategorize matching transactions", gqlSystemJobRun), bulkTransactionDefaults("uncategorize")),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-reprice-failed", "reprice-failed", "Reprice failed-to-price transactions", gqlSystemJobRun), map[string]any{"systemJobId": "reprice-failed-to-price-transactions", "action": "repriceFailedToPriceTransactions"}),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-reprice-ready", "reprice-ready", "Reprice priced and ready-to-price transactions", gqlSystemJobRun), map[string]any{"systemJobId": "reprice-priced-and-ready-to-price-transactions", "action": "repricePricedAndReadyToPriceTransactions"}),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-process-needs-review", "process-needs-review", "Process needs-review transactions", gqlSystemJobRun), map[string]any{"systemJobId": "process-needs-review-transactions", "action": "processNeedsReviewTransactions", "resolution": "accept", "transactionState": "open-needs-review"}),
		withDefaults(adminGraphQLOperation("system-jobs", "system-job-reprocess-rollups", "reprocess-rollups", "Reprocess rollup-parent transactions", gqlSystemJobRun), map[string]any{"systemJobId": "reprocess-rollup-parent-txns", "action": "reprocessRollupParentTxns", "processNewTxns": true, "updateChildrenForExistingParent": false}),
		withInput(adminGraphQLOperation("system-jobs", "system-job-csv-categorize", "csv-categorize", "Start a previously uploaded categorization import", gqlRunImport)),

		// Wallet administration
		adminGraphQLOperation("wallets", "admin-wallets-list", "list", "List wallets and their administrative settings", gqlWalletsList),
		adminGraphQLOperation("wallets", "wallet-balance-checks", "balance-checks", "List reconciliation state for every wallet", gqlWalletBalanceChecks),
		withService(adminRESTOperation("wallets", "wallet-balance-check-history", "balance-check-history WALLET_ID", "Get one wallet's balance-check history", http.MethodGet, "/orgs/{org}/wallets/{walletId}/balance-check-history"), orgreports.APIServicePlatform),
		withInput(adminGraphQLOperation("wallets", "admin-wallet-update", "update WALLET_ID", "Update wallet name, defaults, mappings, roles, flags, or sync settings", gqlWalletUpdate, "walletId")),
		withService(withInput(adminRESTOperation("wallets", "admin-wallet-sync-settings", "sync-settings WALLET_ID", "Enable or disable user-controlled wallet syncing", http.MethodPatch, "/orgs/{org}/wallets/{walletId}")), orgreports.APIServicePlatform),
		withDefaults(adminGraphQLOperation("wallets", "admin-wallet-delete", "delete WALLET_ID", "Delete a wallet and its transactions through the Admin workflow", gqlSystemJobRun, "walletId"), map[string]any{"systemJobId": "delete-wallet"}),

		// Users and invitations
		adminRESTOperation("users", "users-list", "list", "List organization users with aggregated roles and identities", http.MethodGet, "/v3/orgs/{org}/principals/aggregated"),
		adminRESTOperation("users", "user-get", "get USER_ID", "Get one organization principal", http.MethodGet, "/v3/orgs/{org}/principals/{userId}"),
		withInput(adminGraphQLOperation("users", "user-invite", "invite", "Invite a standard organization user", gqlInviteUser)),
		withInput(adminRESTOperation("users", "user-provision-saml", "provision-saml", "Provision a SAML user without an invitation", http.MethodPost, "/v2/orgs/{org}/users/provision-saml")),
		withInput(adminGraphQLOperation("users", "user-update", "update USER_ID", "Update a user's organization role and asserted identity", gqlUpdateOrgUser, "userId")),
		adminGraphQLOperation("users", "user-remove", "remove USER_ID", "Revoke a user's organization access", gqlRemoveOrgUser, "userId"),
		adminGraphQLOperation("users", "invitations-list", "invitations", "List outstanding organization invitations", gqlInvitationsList),
		adminGraphQLOperation("users", "invitation-cancel", "cancel-invitation INVITATION_ID", "Cancel an outstanding invitation", gqlCancelInvitation, "inviteId"),

		// Roles
		withFeature(adminRESTOperation("roles", "roles-list", "list", "List organization roles and permissions", http.MethodGet, "/v3/orgs/{org}/roles"), "advanced-rbac"),
		withFeature(adminRESTOperation("roles", "role-get", "get ROLE_ID", "Get one organization role", http.MethodGet, "/v3/orgs/{org}/roles/{roleId}"), "advanced-rbac"),
		withFeature(withInput(adminRESTOperation("roles", "role-create", "create", "Create an organization role", http.MethodPost, "/v3/orgs/{org}/roles")), "advanced-rbac"),
		withFeature(withInput(adminRESTOperation("roles", "role-update", "update ROLE_ID", "Update an organization role and scopes", http.MethodPatch, "/v3/orgs/{org}/roles/{roleId}")), "advanced-rbac"),

		// SSO and SCIM
		adminRESTOperation("sso", "sso-get", "get", "Get SAML SSO settings and service-provider mode", http.MethodGet, "/v3/orgs/{org}"),
		withETag(withInput(adminRESTOperation("sso", "sso-update", "update", "Enable or update SAML IdP settings, endpoint mode, and default role", http.MethodPatch, "/v3/orgs/{org}/settings/sso"))),
		withFeature(adminRESTOperation("scim", "scim-status", "status", "Show SCIM provisioning URL and token status", http.MethodGet, "/v3/orgs/{org}"), "scim"),
		withFeature(withETag(adminRESTOperation("scim", "scim-generate", "generate", "Generate or regenerate the one-time SCIM token", http.MethodPost, "/v3/orgs/{org}/tokens/scim")), "scim"),
		withFeature(withETag(adminRESTOperation("scim", "scim-disable", "disable", "Disable the organization's SCIM token", http.MethodDelete, "/v3/orgs/{org}/tokens/scim")), "scim"),

		// API credentials
		adminGraphQLOperation("api-keys", "api-keys-list", "list", "List organization API client IDs and permissions", gqlAPIKeysList),
		withInput(adminGraphQLOperation("api-keys", "api-key-create", "create", "Create an API key and return its secret once", gqlAPIKeyCreate)),
		adminGraphQLOperation("api-keys", "api-key-delete", "delete CLIENT_ID", "Delete an organization API key", gqlAPIKeyDelete, "clientId"),

		// Audit configuration
		withFeature(adminRESTOperation("audit-config", "audit-config-get", "get", "Get audit categories and ignored actors", http.MethodGet, "/v3/orgs/{org}/audit-config"), "audit-log"),
		withFeature(adminRESTOperation("audit-config", "audit-config-initialize", "initialize", "Initialize the organization's audit configuration", http.MethodPost, "/v3/orgs/{org}/audit-config"), "audit-log"),
		withFeature(withInput(adminRESTOperation("audit-config", "audit-category-update", "category-update CATEGORY_PATH", "Enable or disable an audit category", http.MethodPut, "/v3/orgs/{org}/audit-config/categories/{categoryPath}")), "audit-log"),
		withFeature(adminRESTOperation("audit-config", "audit-deliveries-list", "delivery-list", "List audit delivery mechanisms", http.MethodGet, "/v3/orgs/{org}/audit-config/delivery-mechanisms"), "audit-log"),
		withFeature(withInput(adminRESTOperation("audit-config", "audit-delivery-create", "delivery-create", "Create a Pub/Sub pull or push delivery mechanism", http.MethodPost, "/v3/orgs/{org}/audit-config/delivery-mechanisms")), "audit-log"),
		withFeature(withInput(adminRESTOperation("audit-config", "audit-pull-update", "pull-update MECHANISM_ID", "Update or retry a Pub/Sub pull delivery mechanism", http.MethodPut, "/v3/orgs/{org}/audit-config/delivery-mechanisms/pubsub-pull/{mechanismId}")), "audit-log"),
		withFeature(withInput(adminRESTOperation("audit-config", "audit-push-update", "push-update MECHANISM_ID", "Update or retry a Pub/Sub push delivery mechanism", http.MethodPut, "/v3/orgs/{org}/audit-config/delivery-mechanisms/pubsub-push/{mechanismId}")), "audit-log"),
		withFeature(adminRESTOperation("audit-config", "audit-delivery-delete", "delivery-delete MECHANISM_ID", "Delete an audit delivery mechanism", http.MethodDelete, "/v3/orgs/{org}/audit-config/delivery-mechanisms/{mechanismId}"), "audit-log"),
		withFeature(adminRESTOperation("audit-config", "audit-delivery-status", "delivery-status MECHANISM_ID", "Get delivery provisioning or teardown status", http.MethodGet, "/v3/orgs/{org}/audit-config/delivery-mechanisms/{mechanismId}/provisioning-status"), "audit-log"),
		withFeature(withInput(adminRESTOperation("audit-config", "audit-ignore-actor", "ignore-actor", "Exclude an actor from organization audit logging", http.MethodPost, "/v3/orgs/{org}/audit-config/ignored-actors")), "audit-log"),
		withFeature(adminRESTOperation("audit-config", "audit-unignore-actor", "unignore-actor PRINCIPAL_ID", "Remove an actor from the audit ignore list", http.MethodDelete, "/v3/orgs/{org}/audit-config/ignored-actors/{principalId}"), "audit-log"),

		// Custom labels
		withFeature(adminRESTOperation("custom-labels", "custom-labels-list", "list", "List and filter custom label definitions", http.MethodGet, "/orgs/{org}/labels"), "custom-labels"),
		withFeature(adminRESTOperation("custom-labels", "custom-label-slug-templates", "slug-templates", "List slug-enabled label templates", http.MethodGet, "/orgs/{org}/labels/slug-templates"), "custom-labels"),
		withFeature(withInput(adminRESTOperation("custom-labels", "custom-label-create", "create", "Create a custom label definition", http.MethodPost, "/orgs/{org}/labels")), "custom-labels"),
		withFeature(withInput(adminRESTOperation("custom-labels", "custom-label-update", "update LABEL_ID", "Update a custom label description or enabled state", http.MethodPatch, "/orgs/{org}/labels/{labelId}")), "custom-labels"),
		withFeature(adminRESTOperation("custom-labels", "custom-label-delete", "delete LABEL_ID", "Delete a non-reserved custom label", http.MethodDelete, "/orgs/{org}/labels/{labelId}"), "custom-labels"),

		// SFTP
		withFeature(withQuery(adminRESTOperation("sftp", "sftp-connections-list", "connections", "List SFTP connections", http.MethodGet, "/orgs/{org}/connections"), "type=sftp", "overrideCache=true"), "sftp-connections"),
		withFeature(adminRESTOperation("sftp", "sftp-connection-get", "connection CONNECTION_ID", "Get one SFTP connection", http.MethodGet, "/orgs/{org}/connections/{connectionId}"), "sftp-connections"),
		withFeature(withInput(adminRESTOperation("sftp", "sftp-connection-test", "connection-test", "Test SFTP credentials before saving", http.MethodPost, "/orgs/{org}/connections/validate")), "sftp-connections"),
		withFeature(withInput(adminRESTOperation("sftp", "sftp-connection-create", "connection-create", "Create an SFTP connection", http.MethodPost, "/orgs/{org}/connections")), "sftp-connections"),
		withFeature(withInput(adminRESTOperation("sftp", "sftp-credentials-rotate", "credentials-rotate CONNECTION_ID", "Rotate SFTP credentials", http.MethodPost, "/orgs/{org}/connections/{connectionId}/credentials")), "sftp-connections"),
		withFeature(adminRESTOperation("sftp", "sftp-paths-list", "paths", "List SFTP report paths", http.MethodGet, "/orgs/{org}/sftp-paths"), "sftp-connections"),
		withFeature(adminRESTOperation("sftp", "sftp-path-get", "path PATH_ID", "Get one SFTP path", http.MethodGet, "/orgs/{org}/sftp-paths/{pathId}"), "sftp-connections"),
		withFeature(withInput(adminRESTOperation("sftp", "sftp-path-test", "path-test", "Test SFTP path reachability and push permissions", http.MethodPost, "/orgs/{org}/sftp-paths/test")), "sftp-connections"),
		withFeature(withInput(adminRESTOperation("sftp", "sftp-path-create", "path-create", "Create an SFTP report path", http.MethodPost, "/orgs/{org}/sftp-paths")), "sftp-connections"),
		withFeature(withInput(adminRESTOperation("sftp", "sftp-path-update", "path-update PATH_ID", "Update an SFTP report path", http.MethodPatch, "/orgs/{org}/sftp-paths/{pathId}")), "sftp-connections"),
		withFeature(adminRESTOperation("sftp", "sftp-path-delete", "path-delete PATH_ID", "Delete an SFTP report path", http.MethodDelete, "/orgs/{org}/sftp-paths/{pathId}"), "sftp-connections"),

		// Rolled-up journal entry configuration
		withFeature(adminRESTOperation("rolled-up-je", "rolled-up-je-list", "list", "List rolled-up journal-entry configurations", http.MethodGet, "/orgs/{org}/rolled-up-je-configurations"), "rolled-up-actions-je-sync"),
		withFeature(adminRESTOperation("rolled-up-je", "rolled-up-je-get", "get CONFIG_ID", "Get a complete rolled-up JE configuration", http.MethodGet, "/orgs/{org}/rolled-up-je-configurations/{configId}"), "rolled-up-actions-je-sync"),
		withFeature(withInput(adminRESTOperation("rolled-up-je", "rolled-up-je-create", "create", "Create a rolled-up JE configuration with advanced overrides", http.MethodPost, "/orgs/{org}/rolled-up-je-configurations")), "rolled-up-actions-je-sync"),
		withFeature(withInput(adminRESTOperation("rolled-up-je", "rolled-up-je-update", "update CONFIG_ID", "Update a rolled-up JE configuration", http.MethodPatch, "/orgs/{org}/rolled-up-je-configurations/{configId}")), "rolled-up-actions-je-sync"),
	}

	// Bind REST path arguments after construction. Keeping names beside paths
	// makes path escaping deterministic and prevents accidental arbitrary URLs.
	for index := range operations {
		if operations[index].Protocol != adminREST {
			continue
		}
		operations[index].ArgumentNames = pathArgumentNames(operations[index].Path)
	}
	return operations
}

func bulkTransactionDefaults(action string) map[string]any {
	return map[string]any{
		"systemJobId":                  "bulk-transaction",
		"action":                       action,
		"transactionType":              "all",
		"allowReconciledModification":  false,
		"allowCategorizedModification": false,
	}
}

func pathArgumentNames(path string) []string {
	known := []string{"entityId", "walletId", "userId", "roleId", "categoryPath", "mechanismId", "principalId", "labelId", "connectionId", "pathId", "configId"}
	result := make([]string, 0)
	for _, name := range known {
		if containsPathPlaceholder(path, name) {
			result = append(result, name)
		}
	}
	return result
}

func containsPathPlaceholder(path, name string) bool {
	return len(path) > 0 && stringContains(path, "{"+name+"}")
}
func stringContains(value, part string) bool {
	for index := 0; index+len(part) <= len(value); index++ {
		if value[index:index+len(part)] == part {
			return true
		}
	}
	return false
}

const gqlAccountingSetupGet = `query AdminAccountingSetup($orgId: ID!) { org(id: $orgId) { id accountingConfig { networkContactIds { blockchain contactId } defaultFeeCategoryId defaultFeeCategoryIds { blockchain categoryId } defaultAccountsPayableCategoryId defaultAccountsReceivableCategoryId allowTxnInference collapseAcrossWallets useTxnMemoForReconciliation } } }`
const gqlAccountingSetupUpdate = `mutation AdminAccountingSetupUpdate($orgId: ID!, $networkContact: NetworkContactInput, $defaultFeeCategoryId: String, $defaultAccountsPayableCategoryId: String, $defaultAccountsReceivableCategoryId: String, $allowTxnInference: Boolean, $collapseAcrossWallets: Boolean, $networkContactIds: [NetworkContactInput], $defaultFeeCategoryIds: [NetworkFeeCategoryInput], $useTxnMemoForReconciliation: Boolean) { updateOrgAccountingConfig(orgId: $orgId, networkContact: $networkContact, defaultFeeCategoryId: $defaultFeeCategoryId, defaultAccountsPayableCategoryId: $defaultAccountsPayableCategoryId, defaultAccountsReceivableCategoryId: $defaultAccountsReceivableCategoryId, allowTxnInference: $allowTxnInference, collapseAcrossWallets: $collapseAcrossWallets, networkContactIds: $networkContactIds, defaultFeeCategoryIds: $defaultFeeCategoryIds, useTxnMemoForReconciliation: $useTxnMemoForReconciliation) { id accountingConfig { networkContactIds { blockchain contactId } defaultFeeCategoryId defaultFeeCategoryIds { blockchain categoryId } defaultAccountsPayableCategoryId defaultAccountsReceivableCategoryId allowTxnInference collapseAcrossWallets useTxnMemoForReconciliation } } }`
const gqlConnectionsList = `query AdminConnections($orgId: ID!) { connections(orgId: $orgId, overrideCache: true) { id provider name accountCode feeAccountCode lastSyncSEC isSetupComplete isDisabled isDeleted isDefault status connectionSpecificFields syncStatus { status lastSyncCompletedSEC errors warnings isRunning } } }`
const gqlConnectionCreate = `mutation AdminConnectionCreate($orgId: ID!, $create: CreateConnectionInput!) { createConnection(orgId: $orgId, create: $create) { success errors message } }`
const gqlConnectionSetup = `mutation AdminConnectionSetup($orgId: ID!, $connectionId: ID!, $accountCode: String!, $feeAccountCode: String!, $newAccountCode: String, $newFeeAccountCode: String) { setupConnection(orgId: $orgId, connectionId: $connectionId, accountCode: $accountCode, feeAccountCode: $feeAccountCode, newAccountCode: $newAccountCode, newFeeAccountCode: $newFeeAccountCode) { success errors } }`
const gqlConnectionSync = `mutation AdminConnectionSync($orgId: ID!, $connectionId: ID!) { syncConnection(orgId: $orgId, connectionId: $connectionId) { success errors } }`
const gqlConnectionDisconnect = `mutation AdminConnectionDisconnect($orgId: ID!, $connectionId: ID!) { revokeToken(orgId: $orgId, connectionId: $connectionId) { success error } }`
const gqlConnectionReconnect = `mutation AdminConnectionReconnect($orgId: ID!, $connectionId: ID!) { reconnectToken(orgId: $orgId, connectionId: $connectionId) { success error } }`
const gqlConnectionUpdate = `mutation AdminConnectionUpdate($orgId: ID!, $connectionId: ID!, $accountCode: String, $feeAccountCode: String, $connectionSpecificFields: AccountingConnectionSpecificFields, $isDefault: Boolean, $isDisabled: Boolean, $name: String, $skipSyncStep: SkipSyncStep) { updateAccountingConnection(orgId: $orgId, connectionId: $connectionId, accountCode: $accountCode, feeAccountCode: $feeAccountCode, connectionSpecificFields: $connectionSpecificFields, isDefault: $isDefault, isDisabled: $isDisabled, name: $name, skipSyncStep: $skipSyncStep) { success error connectionId } }`
const gqlUpdateConnectionCredentials = `mutation AdminConnectionCredentials($orgId: ID!, $connectionId: ID!, $credentials: String!) { updateConnectionTokenCredentials(orgId: $orgId, connectionId: $connectionId, credentials: $credentials) { success errors } }`
const gqlSystemJobsList = `query AdminSystemJobs($orgId: ID!, $limit: Int, $paginationOption: String) { getSystemJobs(orgId: $orgId, limit: $limit, paginationOption: $paginationOption) { nextPageToken items { type id createdByUser { userId name email } cancelledByUser { userId name email } createdSEC statusUpdatedSEC startedSEC completedSEC status steps { id order status description startedSEC completedSEC successMessage errors } progress { numerator denominator units } name params { name value } errors } } }`
const gqlSystemJobRun = `mutation AdminSystemJobRun($orgId: ID!, $systemJobId: String!, $action: String, $walletId: String, $startSEC: String, $endSEC: String, $dataSourceId: String, $transactionType: String, $allowReconciledModification: Boolean, $allowCategorizedModification: Boolean, $resolution: String, $transactionState: String, $updateChildrenForExistingParent: Boolean, $processNewTxns: Boolean, $matchCriteria: TransactionMatchCriteriaInput) { runSystemJob(orgId: $orgId, systemJobId: $systemJobId, action: $action, walletId: $walletId, startSEC: $startSEC, endSEC: $endSEC, dataSourceId: $dataSourceId, transactionType: $transactionType, allowReconciledModification: $allowReconciledModification, allowCategorizedModification: $allowCategorizedModification, resolution: $resolution, transactionState: $transactionState, updateChildrenForExistingParent: $updateChildrenForExistingParent, processNewTxns: $processNewTxns, matchCriteria: $matchCriteria) }`
const gqlRunImport = `mutation AdminRunImport($orgId: ID!, $id: ID!) { runImport(orgId: $orgId, id: $id) }`
const gqlWalletsList = `query AdminWallets($orgId: ID!) { wallets(orgId: $orgId, loadFairValue: false) { id connectionId exchangeConnectionId name description type deviceType address addresses path networkId enabledCoins groupId subsidiaryId accountingConnection defaultInflowContact defaultInflowCategory defaultOutflowContact defaultOutflowCategory defaultFeeContact defaultFeeCategory isBalanceMonitoringOnly isSyncEnabledSystem isSyncEnabledUser lastSuccessfulSyncSEC lastSuccessfulBalanceCheckSEC disabled metadata walletRoleId } }`
const gqlWalletBalanceChecks = `query AdminWalletBalanceChecks($orgId: ID!) { wallets(orgId: $orgId, loadFairValue: false) { id name address addresses networkId lastSuccessfulBalanceCheckSEC latestBalanceCheck { metaSuccess results { totalFiatDelta totalFiatValue lineDeltas { ticker localValue localFiatValue remoteValue } } } } }`
const gqlWalletUpdate = `mutation AdminWalletUpdate($orgId: ID!, $walletId: ID!, $name: String, $description: String, $addWallets: [String], $removeWallets: [String], $flags: WalletFlagsInput, $subsidiaryId: String, $accountingConnection: String, $defaultInflowContact: String, $defaultInflowCategory: String, $defaultOutflowContact: String, $defaultOutflowCategory: String, $defaultFeeContact: String, $defaultFeeCategory: String, $metadata: JSON, $walletRoleId: String, $disabled: Boolean, $remoteId: String) { updateWallet(orgId: $orgId, walletId: $walletId, name: $name, description: $description, addWallets: $addWallets, removeWallets: $removeWallets, flags: $flags, subsidiaryId: $subsidiaryId, accountingConnection: $accountingConnection, defaultInflowContact: $defaultInflowContact, defaultInflowCategory: $defaultInflowCategory, defaultOutflowContact: $defaultOutflowContact, defaultOutflowCategory: $defaultOutflowCategory, defaultFeeContact: $defaultFeeContact, defaultFeeCategory: $defaultFeeCategory, metadata: $metadata, walletRoleId: $walletRoleId, disabled: $disabled, remoteId: $remoteId) { id } }`
const gqlInviteUser = `mutation AdminInviteUser($orgId: ID!, $invite: InviteInput!) { inviteUser(orgId: $orgId, invite: $invite) { success errors } }`
const gqlUpdateOrgUser = `mutation AdminUpdateOrgUser($orgId: ID!, $userId: ID!, $orgUser: OrgUserInput!) { updateOrgUser(orgId: $orgId, userId: $userId, orgUser: $orgUser) { id } }`
const gqlRemoveOrgUser = `mutation AdminRemoveOrgUser($orgId: ID!, $userId: ID!) { removeOrgUser(orgId: $orgId, userId: $userId) { success errors } }`
const gqlInvitationsList = `query AdminInvitations($orgId: ID!) { org(id: $orgId) { id invites { id userName email byUserId } } }`
const gqlCancelInvitation = `mutation AdminCancelInvitation($orgId: ID!, $inviteId: ID!) { declineInvitation(inviteId: $inviteId) }`
const gqlAPIKeysList = `query AdminAPIKeys($orgId: ID!) { authTokens(orgId: $orgId) { clientId description permissions } }`
const gqlAPIKeyCreate = `mutation AdminAPIKeyCreate($orgId: ID!, $description: String!, $permissions: [AuthTokenPermissions]!) { createAuthToken(orgId: $orgId, description: $description, permissions: $permissions) { clientId clientSecret } }`
const gqlAPIKeyDelete = `mutation AdminAPIKeyDelete($orgId: ID!, $clientId: ID!) { deleteAuthToken(orgId: $orgId, clientId: $clientId) }`
