package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"
)

type ruleSummary struct {
	ID                     string          `json:"id"`
	Name                   string          `json:"name"`
	Disabled               bool            `json:"disabled"`
	Type                   string          `json:"type,omitempty"`
	Priority               int             `json:"priority,omitempty"`
	Coin                   string          `json:"coin,omitempty"`
	WalletID               string          `json:"walletId,omitempty"`
	Direction              string          `json:"direction,omitempty"`
	FromAddress            string          `json:"fromAddress,omitempty"`
	ToAddress              string          `json:"toAddress,omitempty"`
	AccountingConnectionID string          `json:"accountingConnectionId,omitempty"`
	AfterDateSEC           json.RawMessage `json:"afterDateSEC,omitempty"`
	BeforeDateSEC          json.RawMessage `json:"beforeDateSEC,omitempty"`
	Action                 json.RawMessage `json:"action,omitempty"`
}

func newOrgRulesCmd() *op.Definition {
	cmd := &op.Definition{
		Use:     "rule",
		Aliases: []string{"rules"},
		Short:   "Discover, create, and validate Bitwave categorization rules",
		Long: `Manage organization categorization rules through Bitwave's rule API.

Rule creation changes future organization behavior. Every create requires
--yes, supports --dry-run, and should be validated against a known transaction
before an enabled rule is run across historical data.`,
	}
	cmd.AddCommand(
		newListRulesCmd(), newGetRuleCmd(), newCreateRawRuleCmd(), newUpdateRawRuleCmd(), newValidateRuleCmd(),
		newToggleRuleCmd("enable", false), newToggleRuleCmd("disable", true), newDeleteRuleCmd(),
		newRunRulesCmd(), newBulkRunRulesCmd(),
	)
	return cmd
}

func newRunRulesCmd() *op.Definition {
	var f transactionMutationFlags
	cmd := &op.Definition{
		Use:   "run",
		Short: "Trigger an asynchronous organization rules run",
		RunE: func(cmd *op.Call, _ []string) error {
			operation := "run-rules"
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			preview := map[string]any{"operation": "RunRulesForOrg", "variables": map[string]any{"orgId": orgID}}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
			}
			if !f.yes {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to trigger an organization-wide rules run without --yes"))
			}
			client := newReportsClient(cmd.Context(), orgID)
			if err := client.RunRules(cmd.Context(), orgID); err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: map[string]any{"triggered": true, "asynchronous": true}})
		},
	}
	addMutationFlags(cmd, &f)
	return cmd
}

func newBulkRunRulesCmd() *op.Definition {
	var f transactionMutationFlags
	var fromDate, toDate string
	cmd := &op.Definition{
		Use:   "bulk-run",
		Short: "Run enabled rules over an inclusive date range",
		RunE: func(cmd *op.Call, _ []string) error {
			operation := "bulk-run-rules"
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			client := newReportsClient(cmd.Context(), orgID)
			org, err := client.Org(cmd.Context(), orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("load organization timezone: %w", err))
			}
			tz := strings.TrimSpace(org.Timezone)
			if tz == "" {
				tz = "UTC"
			}
			location, err := time.LoadLocation(tz)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("load organization timezone %q: %w", tz, err))
			}
			if strings.TrimSpace(fromDate) == "" {
				fromDate = "2000-01-01"
			}
			if strings.TrimSpace(toDate) == "" {
				toDate = time.Now().In(location).Format("2006-01-02")
			}
			after, before, err := resolveRuleDates(fromDate, toDate, tz)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			request := map[string]any{"method": "POST", "path": "/org/" + orgID + "/rules/execute", "body": map[string]any{"executeUpdates": "true", "after": after, "before": before}, "timezone": tz}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: request})
			}
			if !f.yes {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to trigger Bulk Run without --yes"))
			}
			if err := client.ExecuteBulkRules(cmd.Context(), orgID, after, before); err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: map[string]any{"triggered": true, "asynchronous": true, "fromDate": fromDate, "toDate": toDate, "after": after, "before": before, "timezone": tz}})
		},
	}
	addMutationFlags(cmd, &f)
	cmd.Flags().StringVar(&fromDate, "from-date", "", "Inclusive start date in the organization timezone (default 2000-01-01)")
	cmd.Flags().StringVar(&toDate, "to-date", "", "Inclusive end date in the organization timezone (default current date)")
	return cmd
}

func resolveRuleDates(fromDate, toDate, timezone string) (int64, int64, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return 0, 0, fmt.Errorf("load timezone %q: %w", timezone, err)
	}
	from, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(fromDate), location)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid --from-date: %w", err)
	}
	to, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(toDate), location)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid --to-date: %w", err)
	}
	if from.After(to) {
		return 0, 0, errors.New("--from-date must not be after --to-date")
	}
	return from.Unix(), to.AddDate(0, 0, 1).Add(-time.Second).Unix(), nil
}

func newGetRuleCmd() *op.Definition {
	var orgID string
	cmd := &op.Definition{
		Use:   "get RULE_ID",
		Short: "Get one complete organization rule without listing every rule",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			rule, err := client.Rule(cmd.Context(), resolvedOrg, args[0])
			if err != nil {
				return fmt.Errorf("get rule %s: %w", args[0], err)
			}
			_, err = cmd.OutOrStdout().Write(append(rule, '\n'))
			return err
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func newToggleRuleCmd(name string, disabled bool) *op.Definition {
	var f transactionMutationFlags
	cmd := &op.Definition{
		Use:   name + " RULE_ID",
		Short: strings.ToUpper(name[:1]) + name[1:] + " an organization categorization rule",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, name+"-rule", f.jsonOutput, err)
			}
			preview := map[string]any{"operation": "ToggleRuleStatus", "variables": map[string]any{"orgId": orgID, "ruleId": args[0], "disabled": disabled}}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: name + "-rule", Organization: orgID, DryRun: true, Request: preview})
			}
			if !f.yes {
				return mutationError(cmd, name+"-rule", f.jsonOutput, errors.New("refusing to change a rule without --yes (use --dry-run to preview)"))
			}
			client := newReportsClient(cmd.Context(), orgID)
			if err := client.ToggleRule(cmd.Context(), orgID, args[0], disabled); err != nil {
				return mutationError(cmd, name+"-rule", f.jsonOutput, err)
			}
			return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: name + "-rule", Organization: orgID, Result: map[string]any{"ruleId": args[0], "disabled": disabled}})
		},
	}
	addMutationFlags(cmd, &f)
	return cmd
}

func newDeleteRuleCmd() *op.Definition {
	var f transactionMutationFlags
	cmd := &op.Definition{
		Use:   "delete RULE_ID",
		Short: "Permanently delete an organization categorization rule",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, "delete-rule", f.jsonOutput, err)
			}
			preview := map[string]any{"operation": "DeleteRule", "variables": map[string]any{"orgId": orgID, "ruleId": args[0]}}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: "delete-rule", Organization: orgID, DryRun: true, Request: preview})
			}
			if !f.yes {
				return mutationError(cmd, "delete-rule", f.jsonOutput, errors.New("refusing to permanently delete a rule without --yes (use --dry-run to preview)"))
			}
			client := newReportsClient(cmd.Context(), orgID)
			if err := client.DeleteRule(cmd.Context(), orgID, args[0]); err != nil {
				return mutationError(cmd, "delete-rule", f.jsonOutput, err)
			}
			return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: "delete-rule", Organization: orgID, Result: map[string]any{"ruleId": args[0], "deleted": true}})
		},
	}
	addMutationFlags(cmd, &f)
	return cmd
}

func newListRulesCmd() *op.Definition {
	var orgID, query string
	var limit int
	var includeDisabled, full bool
	cmd := &op.Definition{
		Use:   "list",
		Short: "List a bounded set of organization categorization rules",
		RunE: func(cmd *op.Call, _ []string) error {
			if limit < 1 || limit > 500 {
				return errors.New("--limit must be between 1 and 500")
			}
			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			originalQuery := strings.TrimSpace(query)
			rawRules := []json.RawMessage{}
			totalKnown := true
			moreAvailable := false
			if originalQuery == "" {
				totalKnown = false
				token := ""
				for {
					page, pageErr := client.RulesPage(cmd.Context(), resolvedOrg, min(limit*2, 500), token)
					if pageErr != nil {
						return fmt.Errorf("list rules: %w", pageErr)
					}
					rawRules = append(rawRules, page.Items...)
					matching := 0
					for _, raw := range rawRules {
						var summary ruleSummary
						if json.Unmarshal(raw, &summary) == nil && (includeDisabled || !summary.Disabled) {
							matching++
						}
					}
					if matching >= limit || page.NextPageToken == "" || page.NextPageToken == token {
						moreAvailable = page.NextPageToken != ""
						break
					}
					token = page.NextPageToken
				}
			} else {
				var loadErr error
				rawRules, loadErr = client.Rules(cmd.Context(), resolvedOrg)
				if loadErr != nil {
					return fmt.Errorf("list rules: %w", loadErr)
				}
			}
			query = strings.ToLower(strings.TrimSpace(query))
			matchingRaw := make([]json.RawMessage, 0)
			matchingSummary := make([]ruleSummary, 0)
			for _, raw := range rawRules {
				var summary ruleSummary
				if json.Unmarshal(raw, &summary) != nil {
					continue
				}
				if !includeDisabled && summary.Disabled {
					continue
				}
				haystack := strings.ToLower(strings.Join([]string{summary.ID, summary.Name, summary.Type, summary.Coin, summary.WalletID, summary.Direction, summary.FromAddress, summary.ToAddress, summary.AccountingConnectionID, string(summary.Action)}, " "))
				if query != "" && !strings.Contains(haystack, query) {
					continue
				}
				matchingRaw = append(matchingRaw, raw)
				matchingSummary = append(matchingSummary, summary)
				if len(matchingSummary) == limit {
					break
				}
			}
			var rules any = matchingSummary
			if full {
				rules = matchingRaw
			}
			warnings := []string{}
			if len(matchingSummary) == limit && (len(rawRules) > limit || moreAvailable) {
				warnings = append(warnings, "Rule results may be truncated; narrow --query or increase --limit.")
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"schemaVersion": "1", "organization": resolvedOrg, "totalRules": map[bool]any{true: len(rawRules), false: nil}[totalKnown], "totalRulesKnown": totalKnown,
				"count": len(matchingSummary), "rules": rules, "resultShape": map[bool]string{true: "full", false: "compact"}[full],
				"filters": map[string]any{"query": query, "includeDisabled": includeDisabled, "limit": limit}, "warnings": warnings,
			})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(&query, "query", "", "Case-insensitive rule name, ID, asset, wallet, address, direction, or action substring")
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum rules to return (1-500)")
	cmd.Flags().BoolVar(&includeDisabled, "include-disabled", false, "Include disabled rules")
	cmd.Flags().BoolVar(&full, "full", false, "Return complete rule objects")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func newCreateRawRuleCmd() *op.Definition {
	var f transactionMutationFlags
	var input string
	var enabled bool
	cmd := &op.Definition{
		Use:   "create",
		Short: "Create a rule from the complete Bitwave Rule input contract",
		RunE: func(cmd *op.Call, _ []string) error {
			operation := "create-rule"
			rule, err := readRuleObject(cmd.Context(), input, cmd.InOrStdin())
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			rule, err = prepareRuleObject(rule, enabled)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			preview := map[string]any{"method": "POST", "service": "rules GraphQL", "operation": "CreateRule", "variables": map[string]any{"orgId": orgID, "rule": rule}}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
			}
			if !f.yes {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to create an organization rule without --yes (use --dry-run to preview)"))
			}
			client := newReportsClient(cmd.Context(), orgID)
			result, err := client.CreateRule(cmd.Context(), orgID, rule)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}, "created organization rule\n")
		},
	}
	addMutationFlags(cmd, &f)
	cmd.Flags().StringVarP(&input, "input", "i", "", "Complete Rule JSON file, or - for stdin (required)")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "Permit creation of an enabled recurring rule (rules are forced disabled by default)")
	return cmd
}

func newUpdateRawRuleCmd() *op.Definition {
	var f transactionMutationFlags
	var input string
	cmd := &op.Definition{
		Use:   "update RULE_ID",
		Short: "Replace a rule from the complete Bitwave Rule input contract",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			operation := "update-rule"
			rule, err := readRuleObject(cmd.Context(), input, cmd.InOrStdin())
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			preview := map[string]any{"operation": "UpdateRule", "variables": map[string]any{"orgId": orgID, "ruleId": args[0], "rule": rule}}
			if f.dryRun {
				return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
			}
			if !f.yes {
				return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to update an organization rule without --yes"))
			}
			client := newReportsClient(cmd.Context(), orgID)
			result, err := client.UpdateRule(cmd.Context(), orgID, args[0], rule)
			if err != nil {
				return mutationError(cmd, operation, f.jsonOutput, err)
			}
			return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}, "updated organization rule\n")
		},
	}
	addMutationFlags(cmd, &f)
	cmd.Flags().StringVarP(&input, "input", "i", "", "Complete Rule JSON file, or - for stdin (required)")
	return cmd
}

func newValidateRuleCmd() *op.Definition {
	var orgID string
	cmd := &op.Definition{
		Use:   "validate RULE_ID TRANSACTION_ID",
		Short: "Validate whether one rule applies to one transaction",
		Args:  op.ExactArgs(2),
		RunE: func(cmd *op.Call, args []string) error {
			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			result, err := client.ValidateRule(cmd.Context(), resolvedOrg, args[1], args[0])
			if err != nil {
				return fmt.Errorf("validate rule %s on transaction %s: %w", args[0], args[1], err)
			}
			_, err = cmd.OutOrStdout().Write(append(result, '\n'))
			return err
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func readRuleObject(ctx context.Context, path string, stdin io.Reader) (json.RawMessage, error) {
	if path == "" {
		return nil, errors.New("--input is required")
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 4<<20))
	} else {
		data, err = op.RuntimeFrom(ctx).Files.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read rule input: %w", err)
	}
	var object map[string]any
	if len(data) == 0 || json.Unmarshal(data, &object) != nil || object == nil {
		return nil, errors.New("rule input must be a JSON object")
	}
	if len(object) != 1 {
		return nil, errors.New("Rule input must contain exactly one top-level rule variant, such as `transfer`")
	}
	return json.RawMessage(data), nil
}

func prepareRuleObject(rule json.RawMessage, allowEnabled bool) (json.RawMessage, error) {
	var root map[string]any
	if err := json.Unmarshal(rule, &root); err != nil {
		return nil, errors.New("rule input must be a JSON object")
	}
	for variant, raw := range root {
		object, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rule variant %q must be a JSON object", variant)
		}
		if name, _ := object["name"].(string); strings.TrimSpace(name) == "" {
			return nil, errors.New("rule input requires a non-empty `name`")
		}
		if object["action"] == nil {
			return nil, errors.New("rule input requires `action`")
		}
		if connection, _ := object["accountingConnectionId"].(string); strings.TrimSpace(connection) == "" {
			return nil, errors.New("rule input requires `accountingConnectionId`")
		}
		if _, ok := object["priority"].(float64); !ok {
			return nil, errors.New("rule input requires numeric `priority`")
		}
		disabled, present := object["disabled"].(bool)
		if !allowEnabled {
			if present && !disabled {
				return nil, errors.New("input contains an enabled rule; pass --enabled explicitly or set `disabled` to true")
			}
			object["disabled"] = true
		}
		root[variant] = object
	}
	prepared, err := json.Marshal(root)
	return json.RawMessage(prepared), err
}
