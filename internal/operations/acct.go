package operations

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

func newAcctCmd() *operation.Definition {
	cmd := &operation.Definition{
		Use:     "acct",
		Aliases: []string{"account"},
		Short:   "Manage account declarations",
	}
	cmd.AddCommand(newAcctAddCmd())
	cmd.AddCommand(newAcctListCmd())
	return cmd
}

func newAcctAddCmd() *operation.Definition {
	var typeFlag, note string
	cmd := &operation.Definition{
		Use:   "add <Name>",
		Short: "Declare an account",
		Args:  operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			s, _, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			a := model.Account{
				Name: args[0],
				Type: model.AccountType(strings.ToUpper(typeFlag)),
				Note: note,
			}
			if a.Type == "" {
				a.Type = model.InferAccountType(a.Name)
			}
			return s.AddAccount(cmd.Context(), a)
		},
	}
	cmd.Flags().StringVar(&typeFlag, "type", "", "asset|liability|equity|income|expense (inferred from name if omitted)")
	cmd.Flags().StringVar(&note, "note", "", "Optional note")
	return cmd
}

func newAcctListCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "list",
		Short: "List declared and observed accounts",
		RunE: func(cmd *operation.Call, _ []string) error {
			s, _, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			p, err := s.Project(cmd.Context())
			if err != nil {
				return err
			}
			seen := make(map[string]bool)
			for _, a := range p.Accounts {
				seen[a.Name] = true
			}
			for _, e := range p.Entries {
				for _, post := range e.Postings {
					seen[post.Account] = true
				}
			}
			names := make([]string, 0, len(seen))
			for n := range seen {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
			return nil
		},
	}
}

func newPriceCmd() *operation.Definition {
	cmd := &operation.Definition{
		Use:   "price",
		Short: "Manage price observations",
	}
	cmd.AddCommand(newPriceAddCmd())
	cmd.AddCommand(newPriceListCmd())
	return cmd
}

func newPriceAddCmd() *operation.Definition {
	cmd := &operation.Definition{
		Use:   "add <date> <commodity> <price>",
		Short: "Add a P-directive (e.g. price add 2024-01-15 BTC $50000)",
		Args:  operation.ExactArgs(3),
		RunE: func(cmd *operation.Call, args []string) error {
			s, cfg, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			d, err := time.Parse("2006-01-02", args[0])
			if err != nil {
				return fmt.Errorf("date: %w", err)
			}
			line := fmt.Sprintf("P %s %s %s\n", args[0], args[1], args[2])
			parsed, err := format.Parse(strings.NewReader(line))
			if err != nil {
				return fmt.Errorf("parse price: %w", err)
			}
			if len(parsed.Prices) != 1 {
				return fmt.Errorf("expected one price, parsed %d", len(parsed.Prices))
			}
			p := parsed.Prices[0]
			p.Date = d
			if p.QuoteCurrency == "" {
				p.QuoteCurrency = cfg.BaseCurrency
			}
			return s.AddPrice(cmd.Context(), p)
		},
	}
	return cmd
}

func newPriceListCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "list",
		Short: "List price observations",
		RunE: func(cmd *operation.Call, _ []string) error {
			s, _, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			p, err := s.Project(cmd.Context())
			if err != nil {
				return err
			}
			for _, pr := range p.Prices {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s %s\n",
					pr.Date.Format("2006-01-02"),
					pr.Commodity,
					pr.Price.FloatString(8),
					pr.QuoteCurrency,
				)
			}
			return nil
		},
	}
}
