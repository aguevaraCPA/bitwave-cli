package operations

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

func newJECmd() *operation.Definition {
	cmd := &operation.Definition{
		Use:     "je",
		Short:   "Journal entries (add, clear, list, show, import, export)",
		Aliases: []string{"entry"},
	}
	cmd.AddCommand(newJENewCmd())
	cmd.AddCommand(newJEClearCmd())
	cmd.AddCommand(newJEUnclearCmd())
	cmd.AddCommand(newJEListCmd())
	cmd.AddCommand(newJEShowCmd())
	cmd.AddCommand(newJEImportCmd())
	cmd.AddCommand(newJEExportCmd())
	return cmd
}

// --- new ---

func newJENewCmd() *operation.Definition {
	var date, payee, note, statusFlag, journalFlag string
	var check, network, txn string
	var postings []string
	cmd := &operation.Definition{
		Use:   "new",
		Short: "Add a balanced entry to a journal (validated before write)",
		Long: `Add a transaction. Pass --posting twice or more, in the form
  --posting "<Account> <amount> [@ <unitPrice>]"

The entry is parsed, balance-checked against the workspace base currency,
and rejected if it doesn't sum to zero. Defaults to uncleared status.

Journal selection:
  --journal <id>       explicit
  default_journal in .bitwave.toml
  the only journal in the workspace
  fallback: auto-create "default"`,
		RunE: func(cmd *operation.Call, _ []string) error {
			if len(postings) < 2 {
				return fmt.Errorf("need at least two --posting flags")
			}
			if check != "" && len(postings) > 2 {
				return fmt.Errorf(`--check is entry-level and ambiguous with %d postings. Embed it on a specific posting instead, e.g.:
  --posting "Assets:Cash -$1000.00 ; check:%s"`, len(postings), check)
			}
			st, err := model.ParseStatus(statusFlag)
			if err != nil {
				return err
			}
			d, err := time.Parse("2006-01-02", date)
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

			var buf bytes.Buffer
			fmt.Fprintf(&buf, "%s %s %s", d.Format("2006-01-02"), st.Flag(), payee)
			if memo := composeMemo(check, network, txn, note); memo != "" {
				fmt.Fprintf(&buf, "    ; %s", memo)
			}
			fmt.Fprintln(&buf)
			for _, p := range postings {
				rendered, err := renderPostingLine(p)
				if err != nil {
					return fmt.Errorf("posting %q: %w", p, err)
				}
				fmt.Fprintf(&buf, "    %s\n", rendered)
			}
			parsed, err := format.Parse(&buf)
			if err != nil {
				return fmt.Errorf("parse entry: %w", err)
			}
			if len(parsed.Entries) != 1 {
				return fmt.Errorf("expected one entry, parsed %d", len(parsed.Entries))
			}
			e := parsed.Entries[0]
			if !e.IsBalanced(cfg.BaseCurrency) {
				return fmt.Errorf("entry does not balance in %s (sum=%s)", cfg.BaseCurrency, e.Balance(cfg.BaseCurrency).FloatString(8))
			}
			id, err := s.AddEntry(cmd.Context(), journalId, e)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added entry %s\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&date, "date", time.Now().Format("2006-01-02"), "Entry date YYYY-MM-DD")
	cmd.Flags().StringVar(&payee, "payee", "", "Entry payee")
	cmd.Flags().StringVar(&note, "note", "", "Free-form entry note")
	cmd.Flags().StringVar(&check, "check", "", "Check number (recorded as `check:<n>` tag)")
	cmd.Flags().StringVar(&network, "network", "", "Blockchain network (recorded as `network:<n>` tag)")
	cmd.Flags().StringVar(&txn, "txn", "", "On-chain txn id (recorded as `txn:<id>` tag)")
	cmd.Flags().StringVar(&statusFlag, "status", "uncleared", "uncleared|pending|cleared")
	cmd.Flags().StringArrayVar(&postings, "posting", nil, `Posting line, e.g. "Expenses:Food $5.00" (repeat)`)
	cmd.Flags().StringVar(&journalFlag, "journal", "", "Journal id (defaults to the workspace's default)")
	return cmd
}

// --- clear / unclear ---

func newJEClearCmd() *operation.Definition {
	var posting string
	cmd := &operation.Definition{
		Use:   "clear <entry-id>",
		Short: "Mark an entry (or one posting) cleared (*)",
		Args:  operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			s, _, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			return s.SetEntryStatus(cmd.Context(), args[0], model.StatusCleared, posting)
		},
	}
	cmd.Flags().StringVar(&posting, "posting", "", "Limit to one posting account")
	return cmd
}

func newJEUnclearCmd() *operation.Definition {
	var posting string
	cmd := &operation.Definition{
		Use:   "unclear <entry-id>",
		Short: "Revert an entry (or one posting) to uncleared",
		Args:  operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			s, _, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			return s.SetEntryStatus(cmd.Context(), args[0], model.StatusUncleared, posting)
		},
	}
	cmd.Flags().StringVar(&posting, "posting", "", "Limit to one posting account")
	return cmd
}

// --- list / show ---

func newJEListCmd() *operation.Definition {
	var fromS, toS, account string
	cmd := &operation.Definition{
		Use:   "list",
		Short: "List entries (one line per entry)",
		RunE: func(cmd *operation.Call, _ []string) error {
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
			for _, e := range p.Entries {
				if !inRange(e.Date, from, to) {
					continue
				}
				if account != "" && !entryTouchesAccount(e, account) {
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %-2s  %s\n", e.Date.Format("2006-01-02"), e.ID, e.Status.Flag(), e.Payee)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "", "Earliest date YYYY-MM-DD")
	cmd.Flags().StringVar(&toS, "to", "", "Latest date YYYY-MM-DD")
	cmd.Flags().StringVar(&account, "account", "", "Filter to entries touching this account")
	return cmd
}

func newJEShowCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "show <entry-id>",
		Short: "Print one entry as canonical ledger text",
		Args:  operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			s, _, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			p, err := s.Project(cmd.Context())
			if err != nil {
				return err
			}
			for _, e := range p.Entries {
				if e.ID == args[0] {
					sub := &model.Project{Entries: []model.Entry{e}}
					return format.Print(cmd.OutOrStdout(), sub)
				}
			}
			return fmt.Errorf("entry not found: %s", args[0])
		},
	}
}

// --- import / export ---

func newJEImportCmd() *operation.Definition {
	var journalFlag string
	cmd := &operation.Definition{
		Use:   "import <file>",
		Short: "Parse + balance-check a foreign .ledger / .journal / .beancount file, then append",
		Long: `Import a foreign plain-text accounting file into a journal. The parser
auto-detects the format from the extension:

  .ledger / .dat / .test  -> ledger-cli grammar
  .journal                -> hledger journal grammar (same parser, slash dates etc.)
  .beancount / .bean      -> beancount syntax shim (open/close/price/* "Payee")

Relative ` + "`include`" + ` directives are resolved against the source file's
directory. The whole file must parse cleanly and every entry must balance in
the workspace base currency before any rows are written.`,
		Args: operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			parsed, err := parseLedgerFile(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			s, cfg, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			for i, e := range parsed.Entries {
				if !e.IsBalanced(cfg.BaseCurrency) {
					return fmt.Errorf("entry %d does not balance in %s", i, cfg.BaseCurrency)
				}
			}
			journalId, err := resolveJournal(cmd.Context(), s, cfg, journalFlag)
			if err != nil {
				return err
			}
			return s.Import(cmd.Context(), journalId, parsed)
		},
	}
	cmd.Flags().StringVar(&journalFlag, "journal", "", "Journal id to receive the entries")
	return cmd
}

func newJEExportCmd() *operation.Definition {
	var fromS, toS, account, outFile string
	cmd := &operation.Definition{
		Use:   "export",
		Short: "Dump the workspace as canonical ledger text",
		RunE: func(cmd *operation.Call, _ []string) error {
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
			filtered := filterProject(p, from, to, account)
			out := io.Writer(cmd.OutOrStdout())
			if outFile != "" {
				f, err := operation.RuntimeFrom(cmd.Context()).Files.Create(outFile)
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				out = f
			}
			return format.Print(out, filtered)
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "", "Earliest date YYYY-MM-DD")
	cmd.Flags().StringVar(&toS, "to", "", "Latest date YYYY-MM-DD")
	cmd.Flags().StringVar(&account, "account", "", "Restrict to entries touching this account")
	cmd.Flags().StringVar(&outFile, "out", "", "Write to file instead of stdout")
	return cmd
}

// --- helpers ---

func composeMemo(check, network, txn, note string) string {
	var parts []string
	if check != "" {
		parts = append(parts, "check:"+check)
	}
	if network != "" {
		parts = append(parts, "network:"+network)
	}
	if txn != "" {
		parts = append(parts, "txn:"+txn)
	}
	if note != "" {
		parts = append(parts, note)
	}
	return strings.Join(parts, " ")
}

func renderPostingLine(p string) (string, error) {
	s := strings.TrimSpace(p)
	if s == "" {
		return "", fmt.Errorf("empty posting")
	}
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, nil
	}
	account := s[:i]
	rest := strings.TrimSpace(s[i:])
	if rest == "" {
		return account, nil
	}
	return account + "    " + rest, nil
}

func parseRange(fromS, toS string) (time.Time, time.Time, error) {
	var from, to time.Time
	if fromS != "" {
		t, err := time.Parse("2006-01-02", fromS)
		if err != nil {
			return from, to, fmt.Errorf("--from: %w", err)
		}
		from = t
	}
	if toS != "" {
		t, err := time.Parse("2006-01-02", toS)
		if err != nil {
			return from, to, fmt.Errorf("--to: %w", err)
		}
		to = t
	}
	return from, to, nil
}

func inRange(d, from, to time.Time) bool {
	if !from.IsZero() && d.Before(from) {
		return false
	}
	if !to.IsZero() && d.After(to) {
		return false
	}
	return true
}

func entryTouchesAccount(e model.Entry, account string) bool {
	for _, p := range e.Postings {
		if p.Account == account || strings.HasPrefix(p.Account, account+":") {
			return true
		}
	}
	return false
}

func filterProject(p *model.Project, from, to time.Time, account string) *model.Project {
	out := &model.Project{
		Name:         p.Name,
		BaseCurrency: p.BaseCurrency,
		Accounts:     p.Accounts,
		Prices:       p.Prices,
	}
	for _, e := range p.Entries {
		if !inRange(e.Date, from, to) {
			continue
		}
		if account != "" && !entryTouchesAccount(e, account) {
			continue
		}
		out.Entries = append(out.Entries, e)
	}
	return out
}
