package operations

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/bitwave/config"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/workspaces"
)

func newInitCmd() *operation.Definition {
	var (
		cloud        bool
		name         string
		baseCurrency string
		dirFlag      string
	)
	cmd := &operation.Definition{
		Use:   "init",
		Short: "Scaffold a bitwave workspace in the current (or --dir) directory",
		Long: `bitwave init creates a .bitwave.toml marker in the current directory (or --dir),
plus empty accounts.ledger / prices.ledger files for local mode. This must
run BEFORE any other bitwave command — most commands fail with "not a bitwave
workspace" until a .bitwave.toml exists at or above the cwd.

The workspace lives where you run init. Run it from inside the directory
you want to use (e.g. ` + "`cd ~/my-expenses && bitwave init`" + `), or pass --dir to
scaffold somewhere else. Workspace name defaults to the directory basename.

Local mode (default):
  bitwave init [--name N] [--base-currency USD]
    Writes .bitwave.toml plus empty accounts.ledger / prices.ledger.

Cloud mode (--cloud):
  bitwave init --cloud --name N [--base-currency USD]
    Creates a LedgerWorkspace under the active org and binds the cwd to it.
    Requires ` + "`bitwave auth login`" + ` and an active org (` + "`bitwave org use`" + `).
    Cloud mode syncs to Bitwave's workspace ledger service — the hosted
    journal/entry model. It is NOT the Bitwave platform API (transactions,
    categorization, inventory), which is a separate surface.

Examples:
  cd ~/my-expenses && bitwave init
  bitwave init --dir ./jan-2026 --name jan-expenses
  bitwave init --base-currency EUR
  bitwave init --cloud --name acme-fy26`,
		RunE: func(cmd *operation.Call, _ []string) error {
			dir := dirFlag
			if dir == "" {
				dir = operation.RuntimeFrom(cmd.Context()).Options.WorkingDirectory
			}
			abs, err := operation.RuntimeFrom(cmd.Context()).Files.Path(dir)
			if err != nil {
				return err
			}
			if baseCurrency == "" {
				baseCurrency = "USD"
			}
			if cloud {
				if name == "" {
					return fmt.Errorf("--name is required for --cloud")
				}
				return runInitCloud(cmd, abs, name, baseCurrency)
			}
			if name == "" {
				name = filepath.Base(abs)
			}
			return runInitLocal(cmd, abs, name, baseCurrency)
		},
	}
	cmd.Flags().BoolVar(&cloud, "cloud", false, "Create a cloud-backed workspace under the active org")
	cmd.Flags().StringVar(&name, "name", "", "Workspace name (required for --cloud; defaults to dir name otherwise)")
	cmd.Flags().StringVar(&baseCurrency, "base-currency", "USD", "Workspace base currency")
	cmd.Flags().StringVar(&dirFlag, "dir", "", "Workspace directory (defaults to cwd)")
	return cmd
}

func runInitLocal(cmd *operation.Call, dir, name, baseCurrency string) error {
	if _, err := store.InitLocalFS(operation.RuntimeFrom(cmd.Context()).Files, dir, name, baseCurrency); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Initialized local workspace at %s\n", dir)
	fmt.Fprintln(cmd.OutOrStdout(), "Next:")
	fmt.Fprintln(cmd.OutOrStdout(), "  bitwave expense new --report <id> --amount 10 --account Expenses:Meals")
	fmt.Fprintln(cmd.OutOrStdout(), "  bitwave je new   (raw double-entry)   |   bitwave --help   (all commands)")
	return nil
}

func runInitCloud(cmd *operation.Call, dir, name, baseCurrency string) error {
	if _, err := operation.RuntimeFrom(cmd.Context()).Files.Stat(filepath.Join(dir, config.FileName)); err == nil {
		return fmt.Errorf("workspace already initialized at %s", dir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	active, err := requireActiveOrg(cmd.Context())
	if err != nil {
		return err
	}
	notifyEmail := resolveIdentityEmail(cmd.Context())
	c := newWorkspaceClient(cmd.Context(), active.OrgID)
	res, err := c.CreateWorkspace(workspaces.CreateWorkspaceRequest{
		Name:         name,
		BaseCurrency: baseCurrency,
		NotifyEmail:  notifyEmail,
	})
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	cfg := &config.Config{
		Mode:         config.ModeCloud,
		Name:         name,
		BaseCurrency: baseCurrency,
		OrgId:        active.OrgID,
		WorkspaceId:  res.Id,
	}
	if err := config.SaveFS(operation.RuntimeFrom(cmd.Context()).Files, dir, cfg); err != nil {
		return fmt.Errorf("save .bitwave.toml: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Created cloud workspace: %s (%s)\n", name, res.Id)
	fmt.Fprintf(cmd.OutOrStdout(), "Bound %s to it.\n", dir)
	if res.URL != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "View online: %s\n", res.URL)
	}
	if notifyEmail != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "We emailed %s a link.\n", notifyEmail)
	}
	return nil
}
