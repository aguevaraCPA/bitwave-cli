// Package cmd defines the bitwave CLI command tree.
//
// bitwave is an agent-first accounting platform: workflows an agent (or a
// human) can drive end-to-end, with delegation-friendly auth modalities.
// Ledger verbs are top-level (bal, reg, print, ...).
package cmd

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bitwave-io/bitwave-cli/internal/auth"
	"github.com/bitwave-io/bitwave-cli/internal/telemetry"
	"github.com/bitwave-io/bitwave-cli/internal/update"
)

const developmentVersion = "0.3.0-dev"

// Version is populated by GoReleaser or Make via -ldflags. Empty is the
// unambiguous linker sentinel: versioned `go install` builds have no ldflags,
// so init falls back to the module version embedded in Go build information.
var Version string

func init() {
	info, ok := debug.ReadBuildInfo()
	Version = resolveRuntimeVersion(Version, info, ok)
}

func resolveRuntimeVersion(linkerVersion string, info *debug.BuildInfo, ok bool) string {
	// An ldflags value is authoritative for release and local packaged builds.
	if strings.TrimSpace(linkerVersion) != "" {
		return linkerVersion
	}
	return resolveBuildVersion(developmentVersion, info, ok)
}

func resolveBuildVersion(fallback string, info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return fallback
	}
	version := strings.TrimSpace(info.Main.Version)
	if version == "" || version == "(devel)" {
		return fallback
	}
	return strings.TrimPrefix(version, "v")
}

// NewRootCmd builds the bitwave root command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "bitwave",
		Short: "Agent-first accounting platform — run locally, share in the cloud",
		Long: `bitwave is an agent-first accounting platform: AI agents and humans keep
complete, auditable, double-entry books through one CLI. Everything runs
locally against plain-text journal files (compatible with hledger, ledger,
and beancount), and any workspace can be shared or persisted in the
Bitwave cloud when more than one person — or agent — needs it.

Ledger commands operate on a workspace — a directory containing a
.bitwave.toml marker plus one or more .journal files. Run ` + "`bitwave init`" + ` in the
directory where you want to use the plain-text ledger. Organization-product
commands such as ` + "`report`" + `, ` + "`transaction`" + `, ` + "`inventory`" + `, ` + "`pricing`" + `,
` + "`import`" + `, ` + "`close`" + `, and ` + "`org admin`" + ` use the selected Bitwave organization and do
not require a ledger workspace.

Modes:
  - Local (default): files live on disk. No auth needed. ` + "`bitwave share`" + ` also
    works anonymously in local mode (the recipient is the one who signs in
    to adopt the workspace).
  - Cloud (` + "`bitwave init --cloud`" + `): backed by Bitwave's workspace ledger
    service under your org. Requires ` + "`bitwave auth login`" + ` and
    ` + "`bitwave org use`" + `. The workspace ledger remains distinct from the
    Bitwave organization-product commands listed above.

Sign in and select a Bitwave organization:
  bitwave auth login

This is the normal setup path. It opens browser login and then presents an org
picker. Agents can skip the picker with ` + "`bitwave auth login --orgId ORG_ID`" + `.
Organization-product commands do not require a ledger workspace.

Operating context is printed to stderr before every command as a one-line
banner: ` + "`bitwave: workspace=... | org=... | identity=...`" + `. Suppress with
--quiet or BITWAVE_QUIET=1. Run ` + "`bitwave status`" + ` to print it on demand.

Quickstart — log expenses and share a report:
  cd ~/my-expenses
  bitwave init                                                 # workspace in cwd
  bitwave expense new --report 2026-05 --date 2026-05-29 \
      --amount 10 --account Expenses:Meals --merchant Cafe
  bitwave expense new --report 2026-05 --date 2026-05-29 \
      --amount "1 ETH" --account Expenses:Crypto \
      --credit-account Assets:Crypto:ETH
  bitwave expense report 2026-05                               # render to stdout
  bitwave share --to me@example.com                            # email a link

Quickstart — raw double-entry journal:
  bitwave init
  bitwave acct add Assets:Cash
  bitwave acct add Income:Salary
  bitwave je new --date 2026-01-01 --payee "Initial" \
      --posting "Assets:Cash 1000 USD" \
      --posting "Income:Salary -1000 USD"
  bitwave bal

Tip: run ` + "`bitwave <command> --help`" + ` on any subcommand to see flags + examples.`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(c *cobra.Command, _ []string) {
			quiet := quietFlag || os.Getenv("BITWAVE_QUIET") == "1"
			// One-time telemetry disclosure, before any other output. Printed,
			// never prompted — sessions are commonly non-interactive.
			telemetry.NoticeIfNeeded(Version, quiet)
			printUpdateNotice(c, quiet)
			printStatusBanner(c)
		},
		Run: func(c *cobra.Command, _ []string) { _ = c.Help() },
	}

	const (
		groupAuth      = "auth"
		groupAccount   = "account"
		groupLedger    = "ledger"
		groupReports   = "reports"
		groupWorkflows = "workflows"
		groupCLI       = "cli"
	)
	root.AddGroup(
		&cobra.Group{ID: groupAuth, Title: "Authentication:"},
		&cobra.Group{ID: groupAccount, Title: "Org & workspace:"},
		&cobra.Group{ID: groupLedger, Title: "Ledger:"},
		&cobra.Group{ID: groupReports, Title: "Reports:"},
		&cobra.Group{ID: groupWorkflows, Title: "Workflows:"},
		&cobra.Group{ID: groupCLI, Title: "CLI:"},
	)

	addInGroup := func(group string, c *cobra.Command) {
		c.GroupID = group
		root.AddCommand(c)
	}

	root.SetVersionTemplate("bitwave v{{.Version}}\n")

	root.PersistentFlags().StringVar(&authURLFlag, "auth-url", "", "")
	root.PersistentFlags().StringVar(&tokenFlag, "token", "", "Bitwave API token (env: BITWAVE_TOKEN)")
	root.PersistentFlags().BoolVar(&quietFlag, "quiet", false, "Suppress the bitwave status banner (also: BITWAVE_QUIET=1)")
	_ = root.PersistentFlags().MarkHidden("auth-url")

	addInGroup(groupAuth, newAuthCmd())
	addInGroup(groupAccount, newOrgCmd())
	addInGroup(groupAccount, newWorkspaceCmd())
	addInGroup(groupAccount, newJournalCmd())
	addInGroup(groupAccount, newInitCmd())

	addInGroup(groupLedger, newJECmd())
	addInGroup(groupLedger, newAcctCmd())
	addInGroup(groupLedger, newPriceCmd())
	addInGroup(groupLedger, newWalletsCmd())
	addInGroup(groupLedger, newExpenseCmd())

	addInGroup(groupReports, newBalCmd())
	addInGroup(groupReports, newRegCmd())
	addInGroup(groupReports, newPrintCmd())
	addInGroup(groupReports, newAccountsCmd())
	addInGroup(groupReports, newContactsCmd())
	addInGroup(groupReports, newCommoditiesCmd())
	addInGroup(groupReports, newEquityCmd())
	addInGroup(groupReports, newClearedCmd())
	addInGroup(groupReports, newCSVCmd())
	addInGroup(groupReports, newStatsCmd())
	addInGroup(groupReports, newOrgReportCmd())

	addInGroup(groupWorkflows, newMigrateCmd())
	addInGroup(groupWorkflows, newOrgTransactionsCmd())
	addInGroup(groupWorkflows, newOrgInvoicesCmd())
	addInGroup(groupWorkflows, newOrgRulesCmd())
	addInGroup(groupWorkflows, newOrgInventoryCmd())
	addInGroup(groupWorkflows, newOrgPricingCmd())
	addInGroup(groupWorkflows, newOrgImportsCmd())
	addInGroup(groupWorkflows, newAPICmd())
	addInGroup(groupWorkflows, newCloseCmd())
	addInGroup(groupWorkflows, newShareCmd())
	addInGroup(groupWorkflows, newSharesCmd())

	addInGroup(groupCLI, newStatusCmd())
	addInGroup(groupCLI, newVersionCmd())
	addInGroup(groupCLI, newUpgradeCmd())
	addInGroup(groupCLI, newTelemetryCmd())

	return root
}

// PostRunMaintenance runs from main after every invocation — success or
// failure. It cannot live in PersistentPostRun: cobra skips post-hooks when
// RunE errors, which would leave the update cache stale exactly on the runs
// where users are struggling. Refreshes the update-check cache at most once
// per 24h; silent on failure; skipped for quiet runs, dev builds, and
// BITWAVE_NO_UPDATE_CHECK=1.
func PostRunMaintenance() {
	if !quietFlag && os.Getenv("BITWAVE_QUIET") != "1" {
		update.BackgroundRefresh(Version)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the bitwave CLI version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print(auth.Banner)
			fmt.Printf("  bitwave v%s\n\n", Version)
		},
	}
}
