package operations

import (
	"encoding/json"
	"fmt"
	"net/http"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

// NewRoot creates fresh operation definitions and flag bindings for one call.
// No command line is executed here. Interactive auth, global CLI configuration,
// telemetry and self-update are deliberately owned by the terminal adapter.
func NewRoot() *op.Definition {
	root := &op.Definition{Use: "bitwave", Short: "Bitwave accounting and platform operations"}
	org := &op.Definition{Use: "org", Short: "Organization operations"}
	org.AddCommand(newOrgCurrentOperation(), newOrgListOperation(), newOrgWalletsCmd(), newOrgAccountingCmd(), newOrgAdminCmd())
	root.AddCommand(org, newWorkspaceCmd(), newJournalCmd(), newInitCmd(), newJECmd(), newAcctCmd(), newPriceCmd(), newWalletsCmd(), newExpenseCmd(), newBalCmd(), newRegCmd(), newPrintCmd(), newAccountsCmd(), newContactsCmd(), newCommoditiesCmd(), newEquityCmd(), newClearedCmd(), newCSVCmd(), newStatsCmd(), newOrgReportCmd(), newMigrateCmd(), newOrgTransactionsCmd(), newOrgInvoicesCmd(), newOrgRulesCmd(), newOrgInventoryCmd(), newOrgPricingCmd(), newOrgImportsCmd(), newAPICmd(), newCloseCmd(), newShareCmd(), newSharesCmd(), newStatusOperation(), newVersionOperation())
	return root
}
func newOrgCurrentOperation() *op.Definition {
	return &op.Definition{Use: "current", Short: "Print the request-scoped organization", Args: op.NoArgs, RunE: func(c *op.Call, _ []string) error {
		org, e := requireActiveOrg(c.Context())
		if e != nil {
			return e
		}
		_, e = fmt.Fprintln(c.OutOrStdout(), org.OrgID)
		return e
	}}
}
func newOrgListOperation() *op.Definition {
	return &op.Definition{Use: "list", Short: "List organizations available to the supplied credential", Args: op.NoArgs, RunE: func(c *op.Call, _ []string) error {
		rt := op.RuntimeFrom(c.Context())
		data, e := newReportsClient(c.Context(), "").RawRequest(c.Context(), orgreports.APIServiceCore, http.MethodGet, "/v3/orgs", nil)
		if e != nil {
			return e
		}
		type org struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		var list []org
		var wrapped struct {
			Orgs []org `json:"orgs"`
		}
		if e = json.Unmarshal(data, &list); e != nil {
			if e = json.Unmarshal(data, &wrapped); e != nil {
				return e
			}
			list = wrapped.Orgs
		}
		if len(list) == 0 {
			_, e = fmt.Fprintln(c.OutOrStdout(), "(no orgs)")
			return e
		}
		for _, o := range list {
			marker := "  "
			if o.ID == rt.Options.OrganizationID {
				marker = "* "
			}
			fmt.Fprintf(c.OutOrStdout(), "%s%-32s  %s\n", marker, o.ID, o.Name)
		}
		return nil
	}}
}
func newStatusOperation() *op.Definition {
	return &op.Definition{Use: "status", Short: "Show explicit SDK workspace and organization context", Args: op.NoArgs, Run: func(c *op.Call, _ []string) {
		o := op.RuntimeFrom(c.Context()).Options
		identity := "anonymous"
		if o.Token != "" || o.AgentToken != "" || o.TokenResolver != nil {
			identity = "authenticated"
		}
		fmt.Fprintf(c.OutOrStdout(), "bitwave: workspace=%s | org=%s | identity=%s\n", o.WorkingDirectory, o.OrganizationID, identity)
	}}
}
func newVersionOperation() *op.Definition {
	return &op.Definition{Use: "version", Short: "Show the shared Bitwave SDK version", Args: op.NoArgs, Run: func(c *op.Call, _ []string) { fmt.Fprintln(c.OutOrStdout(), "bitwave shared SDK") }}
}
