package operations

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-accounting-sdk/expense"
	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

func newExpenseCmd() *operation.Definition {
	cmd := &operation.Definition{
		Use:   "expense",
		Short: "Log and report expenses (plain-text accounting tagged entries)",
		Long: `Expenses are stored as normal journal entries tagged with
expense-report:<id>. A "report" is emergent — it's a filter over the
workspace by tag, so you can attribute the same entry to a report any time.

Each ` + "`bitwave expense new`" + ` writes one balanced double-entry transaction:
  - DEBIT  <--account> (e.g. Expenses:Meals) for the amount
  - CREDIT <--credit-account>, default Assets:Cash
    (or Liabilities:Reimbursements when --reimbursable is set)

Use --amount "1 ETH" for non-USD commodities; the credit leg keeps the same
commodity, so pick a --credit-account that matches (e.g. Assets:Crypto:ETH).

Examples:
  # Cash receipt — defaults credit to Assets:Cash
  bitwave expense new --report 2026-05 --date 2026-05-29 \
      --amount 10 --account Expenses:Meals --merchant Cafe

  # Reimbursable — credits Liabilities:Reimbursements
  bitwave expense new --report Q1-travel --date 2026-05-16 \
      --amount 120 --account Expenses:Travel --merchant Acme --reimbursable

  # Crypto / non-USD commodity
  bitwave expense new --report 2026-05 --date 2026-05-29 \
      --amount "1 ETH" --account Expenses:Crypto \
      --credit-account Assets:Crypto:ETH \
      --note "tx 0x1234..."

  bitwave expense report 2026-05              # human-readable table on stdout
  bitwave expense report 2026-05 --format csv # csv|qif|html|json for export

To email a recipient a tokenized link to the workspace, use the root share:
  bitwave share --to me@example.com`,
	}
	cmd.AddCommand(newExpenseNewCmd())
	cmd.AddCommand(newExpenseReportCmd())
	return cmd
}

func newExpenseNewCmd() *operation.Definition {
	var (
		reportId, dateS, account, merchant, payee, note, journalFlag string
		amount                                                       string
		reimbursable                                                 bool
		creditAccount                                                string
	)
	cmd := &operation.Definition{
		Use:   "new",
		Short: "Add a tagged expense entry to a journal",
		Long: `Append one balanced double-entry transaction to the active journal,
tagged with expense-report:<--report> so it shows up in ` + "`bitwave expense report`" + `.

Required: --report, --amount, --account. Date defaults to today.

Credit-side default:
  - Assets:Cash                 (default)
  - Liabilities:Reimbursements  (when --reimbursable is set)
  - Override with --credit-account (e.g. Assets:Crypto:ETH for an ETH spend)

Amount syntax:
  --amount 120          # bare number -> workspace base currency
  --amount 120.45       # same
  --amount "1 ETH"      # commodity -> credit leg uses the same commodity
  --amount "0.5 BTC"
  --amount "$10"        # $-prefix forces USD

Examples:
  bitwave expense new --report 2026-05 --amount 10 --account Expenses:Meals
  bitwave expense new --report Q1 --amount 120 --account Expenses:Travel \
      --merchant Acme --reimbursable
  bitwave expense new --report 2026-05 --amount "1 ETH" \
      --account Expenses:Crypto --credit-account Assets:Crypto:ETH \
      --note "tx 0xabc..."`,
		RunE: func(cmd *operation.Call, _ []string) error {
			if strings.TrimSpace(reportId) == "" {
				return fmt.Errorf("--report is required")
			}
			if amount == "" {
				return fmt.Errorf("--amount is required")
			}
			if account == "" {
				return fmt.Errorf("--account is required")
			}
			d, err := time.Parse("2006-01-02", dateS)
			if err != nil {
				return fmt.Errorf("--date: %w", err)
			}
			s, cfg, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			journalId, err := resolveJournal(cmd.Context(), s, cfg, journalFlag)
			if err != nil {
				return err
			}
			if payee == "" {
				payee = merchant
			}
			if payee == "" {
				payee = "Expense"
			}
			credit := creditAccount
			if credit == "" {
				if reimbursable {
					credit = "Liabilities:Reimbursements"
				} else {
					credit = "Assets:Cash"
				}
			}

			tags := expense.ComposeTags(reportId, merchant, reimbursable)
			memo := strings.Join(tags, " ")
			if note != "" {
				memo = memo + " " + note
			}

			// If the user already supplied a commodity (e.g. "1 ETH") use it
			// as-is; otherwise fall back to the workspace base currency. Also
			// normalise the negation for the credit leg so we don't emit
			// "--1 ETH" when the amount itself is signed.
			qty, commodity := splitAmountCommodity(amount)
			if commodity == "" {
				commodity = cfg.BaseCurrency
			}
			var negQty string
			if strings.HasPrefix(qty, "-") {
				negQty = qty[1:]
			} else {
				negQty = "-" + qty
			}

			var buf bytes.Buffer
			_, _ = fmt.Fprintf(&buf, "%s %s    ; %s\n", d.Format("2006-01-02"), payee, memo)
			_, _ = fmt.Fprintf(&buf, "    %s    %s %s\n", account, qty, commodity)
			_, _ = fmt.Fprintf(&buf, "    %s    %s %s\n", credit, negQty, commodity)

			parsed, err := format.Parse(&buf)
			if err != nil {
				return fmt.Errorf("parse entry: %w", err)
			}
			if len(parsed.Entries) != 1 {
				return fmt.Errorf("expected one entry, parsed %d", len(parsed.Entries))
			}
			e := parsed.Entries[0]
			if !e.IsBalanced(cfg.BaseCurrency) {
				return fmt.Errorf("entry does not balance in %s", cfg.BaseCurrency)
			}
			id, err := s.AddEntry(cmd.Context(), journalId, e)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added expense %s to report %s\n", id, reportId)
			return nil
		},
	}
	cmd.Flags().StringVar(&reportId, "report", "", "Expense-report id (required; e.g. Q1-travel)")
	cmd.Flags().StringVar(&dateS, "date", time.Now().Format("2006-01-02"), "Date YYYY-MM-DD")
	cmd.Flags().StringVar(&amount, "amount", "", "Amount (required, e.g. 120, 120.45, or '1 ETH'). Bare numbers use the workspace base currency.")
	cmd.Flags().StringVar(&account, "account", "", "Expense account (required; e.g. Expenses:Travel)")
	cmd.Flags().StringVar(&merchant, "merchant", "", "Merchant / vendor (recorded as merchant: tag)")
	cmd.Flags().BoolVar(&reimbursable, "reimbursable", false, "Mark as reimbursable (credits Liabilities:Reimbursements)")
	cmd.Flags().StringVar(&creditAccount, "credit-account", "", "Override the credit-side account (default: Assets:Cash or Liabilities:Reimbursements)")
	cmd.Flags().StringVar(&payee, "payee", "", "Override the entry payee (defaults to --merchant or 'Expense')")
	cmd.Flags().StringVar(&note, "note", "", "Free-form note appended after tags")
	cmd.Flags().StringVar(&journalFlag, "journal", "", "Journal id (defaults to the workspace's default)")
	return cmd
}

func newExpenseReportCmd() *operation.Definition {
	var (
		fromS, toS, formatFlag, outFile, journalFlag string
	)
	cmd := &operation.Definition{
		Use:   "report <reportId>",
		Short: "Render an expense report",
		Long: `Render the entries tagged expense-report:<id> in the chosen format.
Defaults to a fixed-width text table on stdout; use --format csv|qif|html|json
--out file for export.

To share with a recipient, use the workspace-level share at the root:
  bitwave share --to me@example.com`,
		Args: operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			return runExpenseReport(cmd, args[0], fromS, toS, formatFlag, outFile, journalFlag)
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "", "Earliest date YYYY-MM-DD")
	cmd.Flags().StringVar(&toS, "to", "", "Latest date YYYY-MM-DD")
	cmd.Flags().StringVar(&formatFlag, "format", "text", "Output format: text|csv|qif|html|json")
	cmd.Flags().StringVar(&outFile, "out", "", "Write to file instead of stdout")
	cmd.Flags().StringVar(&journalFlag, "journal", "", "Journal id (defaults to the workspace's default)")
	return cmd
}

// runExpenseReport is shared by the explicit subcommand and the parent's
// fallback handler so users can omit the `run` keyword.
func runExpenseReport(cmd *operation.Call, reportId, fromS, toS, formatFlag, outFile, journalFlag string) error {
	s, _, _, err := resolveStore(cmd.Context())
	if err != nil {
		return err
	}
	p, err := s.Project(cmd.Context())
	if err != nil {
		return err
	}
	from, to, err := parseRange(fromS, toS)
	if err != nil {
		return err
	}
	entries := filterEntriesByJournal(p.Entries, journalFlag)
	r, err := expense.Build(entries, expense.Filter{ReportId: reportId, From: from, To: to})
	if err != nil {
		return err
	}

	out := io.Writer(cmd.OutOrStdout())
	if outFile != "" {
		f, err := operation.RuntimeFrom(cmd.Context()).Files.Create(outFile)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		out = f
	}

	switch strings.ToLower(formatFlag) {
	case "", "text", "txt":
		return expense.RenderText(out, r)
	case "csv":
		return expense.RenderCSV(out, r)
	case "qif":
		return expense.RenderQIF(out, r)
	case "html":
		return expense.RenderHTML(out, r)
	case "json":
		return expense.RenderJSON(out, r)
	default:
		return fmt.Errorf("unknown --format %q (want text|csv|qif|html|json)", formatFlag)
	}
}

// filterEntriesByJournal is a no-op for the in-memory model — the Store
// already collapses workspace journals into one Project. The flag is wired
// for forward-compat: a future store can scope to a journal.
func filterEntriesByJournal(entries []model.Entry, _ string) []model.Entry {
	return entries
}

// splitAmountCommodity separates a raw --amount flag into its numeric
// portion and an optional commodity suffix. Supports:
//
//	"120"        -> ("120", "")
//	"1 ETH"      -> ("1", "ETH")
//	"1.5BTC"     -> ("1.5", "BTC")
//	"$10"        -> ("10", "USD")
//	"-1 ETH"     -> ("-1", "ETH")
//
// The numeric portion preserves the user's sign. Commodity is uppercased.
func splitAmountCommodity(raw string) (qty, commodity string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ""
	}
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign = "-"
		s = strings.TrimSpace(s[1:])
	} else if strings.HasPrefix(s, "+") {
		s = strings.TrimSpace(s[1:])
	}
	if strings.HasPrefix(s, "$") {
		return sign + strings.TrimSpace(s[1:]), "USD"
	}
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' || c == ',' {
			i++
			continue
		}
		break
	}
	num := strings.TrimSpace(s[:i])
	rest := strings.TrimSpace(s[i:])
	return sign + num, strings.ToUpper(rest)
}
