package operations

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-accounting-sdk/report"

	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
)

// loadProject is the report-side loader, parallel to bw's loadProject.
//
// Stores expose the workspace as a single merged Project — the journal id is a
// write-time concept that Store.Project flattens away. A non-empty journal
// re-applies that scope here, recovering each entry's journal from its
// journal-prefixed id. This is what lets a workspace hold wallet activity and
// accounting-only activity side by side and still report on them separately.
func loadProject(ctx context.Context, journal string) (*model.Project, error) {
	st, _, _, err := resolveStore(ctx)
	if err != nil {
		return nil, err
	}
	p, err := st.Project(ctx)
	if err != nil {
		return nil, err
	}
	if journal == "" {
		return p, nil
	}

	ids, err := st.Journals(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(ids, journal) {
		return nil, fmt.Errorf("no journal %q in this workspace (have: %s)", journal, strings.Join(ids, ", "))
	}

	scoped := *p
	scoped.Entries = nil
	prefixed := 0
	for _, e := range p.Entries {
		j, ok := store.JournalOfEntryID(e.ID)
		if !ok {
			continue
		}
		prefixed++
		if j == journal {
			scoped.Entries = append(scoped.Entries, e)
		}
	}
	// Guard against silently reporting an empty ledger on a store whose entry
	// ids aren't journal-prefixed, which would read as "this journal is empty".
	if prefixed == 0 && len(p.Entries) > 0 {
		return nil, fmt.Errorf("--journal is not supported for this workspace: entry ids carry no journal prefix")
	}

	// Account declarations are workspace-scoped, so a journal-scoped view would
	// otherwise still carry every account in the workspace — including
	// wallet-tagged ones, whose wallet:/address:/network: metadata survives an
	// export/import round trip and registers a wallet in the importing
	// workspace. Narrow declarations to the accounts this journal posts to.
	scoped.Accounts = accountsUsedBy(p.Accounts, scoped.Entries)
	return &scoped, nil
}

// accountsUsedBy returns the declarations referenced by the given entries,
// preserving the original declaration order.
func accountsUsedBy(declared []model.Account, entries []model.Entry) []model.Account {
	if len(declared) == 0 {
		return nil
	}
	used := make(map[string]bool, len(entries))
	for _, e := range entries {
		for _, post := range e.Postings {
			used[post.Account] = true
		}
	}
	kept := make([]model.Account, 0, len(used))
	for _, a := range declared {
		if used[a.Name] {
			kept = append(kept, a)
		}
	}
	return kept
}

func buildFilter(from, to, account string, clearedOnly bool) report.Filter {
	f := report.Filter{AccountMatch: account, ClearedOnly: clearedOnly}
	if from != "" {
		if t, err := time.Parse("2006-01-02", from); err == nil {
			f.From = t
		}
	}
	if to != "" {
		if t, err := time.Parse("2006-01-02", to); err == nil {
			f.To = t
		}
	}
	return f
}

func addReportFilters(c *operation.Definition, from, to, account, journal *string, clearedOnly *bool) {
	addJournalFilter(c, journal)
	c.Flags().StringVar(from, "from", "", "Earliest date (YYYY-MM-DD)")
	c.Flags().StringVar(to, "to", "", "Latest date (YYYY-MM-DD)")
	c.Flags().StringVar(account, "account", "", "Account name substring filter")
	if clearedOnly != nil {
		c.Flags().BoolVar(clearedOnly, "cleared", false, "Cleared entries only")
	}
}

// addJournalFilter registers --journal, which narrows a report to one journal
// inside the workspace. Omitted, every report stays workspace-wide as before.
func addJournalFilter(c *operation.Definition, journal *string) {
	c.Flags().StringVar(journal, "journal", "", "Restrict the report to one journal id")
}

func newPrintCmd() *operation.Definition {
	var from, to, account, journal string
	var cleared bool
	cmd := &operation.Definition{
		Use:   "print",
		Short: "Re-emit canonical ledger format",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context(), journal)
			if err != nil {
				return err
			}
			return report.Print(cmd.OutOrStdout(), p, buildFilter(from, to, account, cleared))
		},
	}
	addReportFilters(cmd, &from, &to, &account, &journal, &cleared)
	return cmd
}

func newBalCmd() *operation.Definition {
	var from, to, account, journal string
	var cleared bool
	cmd := &operation.Definition{
		Use:     "bal [account-substring]",
		Aliases: []string{"balance"},
		Short:   "Account balances tree",
		Args:    operation.MaximumNArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			p, err := loadProject(cmd.Context(), journal)
			if err != nil {
				return err
			}
			if len(args) == 1 && account == "" {
				account = args[0]
			}
			return report.Balance(cmd.OutOrStdout(), p, buildFilter(from, to, account, cleared))
		},
	}
	addReportFilters(cmd, &from, &to, &account, &journal, &cleared)
	return cmd
}

func newRegCmd() *operation.Definition {
	var from, to, account, journal string
	var cleared bool
	cmd := &operation.Definition{
		Use:     "reg [account-substring]",
		Aliases: []string{"register"},
		Short:   "Posting register with running balance",
		Args:    operation.MaximumNArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			p, err := loadProject(cmd.Context(), journal)
			if err != nil {
				return err
			}
			if len(args) == 1 && account == "" {
				account = args[0]
			}
			return report.Register(cmd.OutOrStdout(), p, buildFilter(from, to, account, cleared))
		},
	}
	addReportFilters(cmd, &from, &to, &account, &journal, &cleared)
	return cmd
}

func newAccountsCmd() *operation.Definition {
	var account, journal string
	cmd := &operation.Definition{
		Use:   "accounts",
		Short: "List declared and observed accounts",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context(), journal)
			if err != nil {
				return err
			}
			return report.Accounts(cmd.OutOrStdout(), p, report.Filter{AccountMatch: account})
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "Account name substring filter")
	addJournalFilter(cmd, &journal)
	return cmd
}

// newContactsCmd: ledger-cli's "payees" report — renamed because the cloud ledger uses
// the directionally-neutral "contacts" terminology (matching Xero/QuickBooks).
func newContactsCmd() *operation.Definition {
	var journal string
	cmd := &operation.Definition{
		Use:     "contacts",
		Aliases: []string{"payees"},
		Short:   "Distinct contacts (payees + payors) referenced by entries",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context(), journal)
			if err != nil {
				return err
			}
			return report.Payees(cmd.OutOrStdout(), p)
		},
	}
	return cmd
}

func newCommoditiesCmd() *operation.Definition {
	var journal string
	cmd := &operation.Definition{
		Use:   "commodities",
		Short: "Distinct commodities (asset symbols)",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context(), journal)
			if err != nil {
				return err
			}
			return report.Commodities(cmd.OutOrStdout(), p)
		},
	}
	addJournalFilter(cmd, &journal)
	return cmd
}

func newEquityCmd() *operation.Definition {
	var from, to, account, journal string
	cmd := &operation.Definition{
		Use:   "equity",
		Short: "Equity-style snapshot entry",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context(), journal)
			if err != nil {
				return err
			}
			return report.Equity(cmd.OutOrStdout(), p, buildFilter(from, to, account, false))
		},
	}
	addReportFilters(cmd, &from, &to, &account, &journal, nil)
	return cmd
}

func newClearedCmd() *operation.Definition {
	var journal string
	cmd := &operation.Definition{
		Use:   "cleared",
		Short: "Print only cleared entries",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context(), journal)
			if err != nil {
				return err
			}
			return report.Cleared(cmd.OutOrStdout(), p)
		},
	}
	addJournalFilter(cmd, &journal)
	return cmd
}

func newCSVCmd() *operation.Definition {
	var from, to, account, journal string
	var cleared bool
	cmd := &operation.Definition{
		Use:   "csv",
		Short: "CSV dump of postings",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context(), journal)
			if err != nil {
				return err
			}
			return report.CSVPrint(cmd.OutOrStdout(), p, buildFilter(from, to, account, cleared))
		},
	}
	addReportFilters(cmd, &from, &to, &account, &journal, &cleared)
	return cmd
}

func newStatsCmd() *operation.Definition {
	var journal string
	cmd := &operation.Definition{
		Use:   "stats",
		Short: "Workspace summary counts",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context(), journal)
			if err != nil {
				return err
			}
			return report.Stats(cmd.OutOrStdout(), p)
		},
	}
	addJournalFilter(cmd, &journal)
	return cmd
}
