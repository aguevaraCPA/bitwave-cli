package operations

import (
	"context"
	"time"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-accounting-sdk/report"
)

// loadProject is the report-side loader, parallel to bw's loadProject.
func loadProject(ctx context.Context) (*model.Project, error) {
	s, _, _, err := resolveStore(ctx)
	if err != nil {
		return nil, err
	}
	return s.Project(ctx)
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

func addReportFilters(c *operation.Definition, from, to, account *string, clearedOnly *bool) {
	c.Flags().StringVar(from, "from", "", "Earliest date (YYYY-MM-DD)")
	c.Flags().StringVar(to, "to", "", "Latest date (YYYY-MM-DD)")
	c.Flags().StringVar(account, "account", "", "Account name substring filter")
	if clearedOnly != nil {
		c.Flags().BoolVar(clearedOnly, "cleared", false, "Cleared entries only")
	}
}

func newPrintCmd() *operation.Definition {
	var from, to, account string
	var cleared bool
	cmd := &operation.Definition{
		Use:   "print",
		Short: "Re-emit canonical ledger format",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context())
			if err != nil {
				return err
			}
			return report.Print(cmd.OutOrStdout(), p, buildFilter(from, to, account, cleared))
		},
	}
	addReportFilters(cmd, &from, &to, &account, &cleared)
	return cmd
}

func newBalCmd() *operation.Definition {
	var from, to, account string
	var cleared bool
	cmd := &operation.Definition{
		Use:     "bal [account-substring]",
		Aliases: []string{"balance"},
		Short:   "Account balances tree",
		Args:    operation.MaximumNArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			p, err := loadProject(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 && account == "" {
				account = args[0]
			}
			return report.Balance(cmd.OutOrStdout(), p, buildFilter(from, to, account, cleared))
		},
	}
	addReportFilters(cmd, &from, &to, &account, &cleared)
	return cmd
}

func newRegCmd() *operation.Definition {
	var from, to, account string
	var cleared bool
	cmd := &operation.Definition{
		Use:     "reg [account-substring]",
		Aliases: []string{"register"},
		Short:   "Posting register with running balance",
		Args:    operation.MaximumNArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			p, err := loadProject(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 && account == "" {
				account = args[0]
			}
			return report.Register(cmd.OutOrStdout(), p, buildFilter(from, to, account, cleared))
		},
	}
	addReportFilters(cmd, &from, &to, &account, &cleared)
	return cmd
}

func newAccountsCmd() *operation.Definition {
	var account string
	cmd := &operation.Definition{
		Use:   "accounts",
		Short: "List declared and observed accounts",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context())
			if err != nil {
				return err
			}
			return report.Accounts(cmd.OutOrStdout(), p, report.Filter{AccountMatch: account})
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "Account name substring filter")
	return cmd
}

// newContactsCmd: ledger-cli's "payees" report — renamed because the cloud ledger uses
// the directionally-neutral "contacts" terminology (matching Xero/QuickBooks).
func newContactsCmd() *operation.Definition {
	cmd := &operation.Definition{
		Use:     "contacts",
		Aliases: []string{"payees"},
		Short:   "Distinct contacts (payees + payors) referenced by entries",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context())
			if err != nil {
				return err
			}
			return report.Payees(cmd.OutOrStdout(), p)
		},
	}
	return cmd
}

func newCommoditiesCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "commodities",
		Short: "Distinct commodities (asset symbols)",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context())
			if err != nil {
				return err
			}
			return report.Commodities(cmd.OutOrStdout(), p)
		},
	}
}

func newEquityCmd() *operation.Definition {
	var from, to, account string
	cmd := &operation.Definition{
		Use:   "equity",
		Short: "Equity-style snapshot entry",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context())
			if err != nil {
				return err
			}
			return report.Equity(cmd.OutOrStdout(), p, buildFilter(from, to, account, false))
		},
	}
	addReportFilters(cmd, &from, &to, &account, nil)
	return cmd
}

func newClearedCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "cleared",
		Short: "Print only cleared entries",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context())
			if err != nil {
				return err
			}
			return report.Cleared(cmd.OutOrStdout(), p)
		},
	}
}

func newCSVCmd() *operation.Definition {
	var from, to, account string
	var cleared bool
	cmd := &operation.Definition{
		Use:   "csv",
		Short: "CSV dump of postings",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context())
			if err != nil {
				return err
			}
			return report.CSVPrint(cmd.OutOrStdout(), p, buildFilter(from, to, account, cleared))
		},
	}
	addReportFilters(cmd, &from, &to, &account, &cleared)
	return cmd
}

func newStatsCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "stats",
		Short: "Workspace summary counts",
		RunE: func(cmd *operation.Call, _ []string) error {
			p, err := loadProject(cmd.Context())
			if err != nil {
				return err
			}
			return report.Stats(cmd.OutOrStdout(), p)
		},
	}
}
