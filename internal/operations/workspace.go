package operations

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/bitwave/config"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/workspaces"
)

func newWorkspaceCmd() *operation.Definition {
	cmd := &operation.Definition{
		Use:   "workspace",
		Short: "Manage cloud workspaces in the active org",
		Long: `bitwave workspace lists, creates, and switches between cloud workspaces in
the active org. The selection is recorded in the cwd's .bitwave.toml so
later commands (bitwave je, bitwave bal, ...) target the right workspace.`,
	}
	cmd.AddCommand(newWorkspaceListCmd())
	cmd.AddCommand(newWorkspaceUseCmd())
	cmd.AddCommand(newWorkspaceCurrentCmd())
	cmd.AddCommand(newWorkspaceCreateCmd())
	cmd.AddCommand(newWorkspaceAdoptCmd())
	cmd.AddCommand(newWorkspaceURLCmd())
	return cmd
}

// newWorkspaceURLCmd prints the browser URL for the cwd's bound cloud
// workspace, fetched fresh from GET /v1/workspaces/{id}.
func newWorkspaceURLCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "url",
		Short: "Print the browser URL for the cwd's bound cloud workspace",
		Long: `bitwave workspace url reads the workspace bound in the cwd's .bitwave.toml
and prints its online URL. Requires a cloud-mode workspace; local-mode
workspaces have no online URL.`,
		RunE: func(cmd *operation.Call, _ []string) error {
			cfg, _, err := loadCwdConfig(cmd.Context())
			if err != nil {
				return err
			}
			if cfg.Mode != config.ModeCloud {
				return fmt.Errorf("workspace at cwd is in %s mode, not cloud — local workspaces have no online URL", cfg.Mode)
			}
			if cfg.WorkspaceId == "" {
				return fmt.Errorf("no workspace_id in .bitwave.toml — run `bitwave workspace use`")
			}
			active, err := requireActiveOrg(cmd.Context())
			if err != nil {
				return err
			}
			c := newWorkspaceClient(cmd.Context(), active.OrgID)
			w, err := c.GetWorkspace(cfg.WorkspaceId)
			if err != nil {
				return fmt.Errorf("get workspace: %w", err)
			}
			if w.URL == "" {
				return fmt.Errorf("workspace %s has no URL (the server may predate this feature)", cfg.WorkspaceId)
			}
			fmt.Fprintln(cmd.OutOrStdout(), w.URL)
			return nil
		},
	}
}

// newWorkspaceAdoptCmd accepts a pending shared workspace from the cloud ledger. The
// recipient must be logged in (PKCE or BITWAVE_TOKEN); auth is forwarded as
// the bearer token. The cloud ledger starts an async server-side workflow
// that drains the stashed zip into real ledger rows under the recipient's
// default org.
//
// The command returns as soon as the workflow is scheduled — it doesn't wait
// for hydration to finish. Hydration is idempotent and the row's Status
// flips back to active when it completes; `bitwave workspace list` will then
// show the newly-adopted workspace.
func newWorkspaceAdoptCmd() *operation.Definition {
	var name string
	cmd := &operation.Definition{
		Use:   "adopt <workspaceId>",
		Short: "Accept a shared workspace into the active org",
		Args:  operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			workspaceId := args[0]
			active, err := requireActiveOrg(cmd.Context())
			if err != nil {
				return err
			}
			c := newWorkspaceShareClient(cmd.Context(), active.OrgID)
			resp, err := c.Adopt(cmd.Context(), workspaceId, name)
			if err != nil {
				return fmt.Errorf("adopt workspace: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Adopted workspace %s\n", resp.WorkspaceId)
			fmt.Fprintf(cmd.OutOrStdout(), "  Workflow:    %s\n", resp.WorkflowId)
			fmt.Fprintf(cmd.OutOrStdout(), "  WorkflowRun: %s\n", resp.WorkflowRunId)
			fmt.Fprintf(cmd.OutOrStdout(), "Hydration runs asynchronously; use `bitwave workspace list` to see when it appears.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Rename the adopted workspace (defaults to the shared name)")
	return cmd
}

func newWorkspaceListCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "list",
		Short: "List cloud workspaces in the active org",
		RunE: func(cmd *operation.Call, _ []string) error {
			active, err := requireActiveOrg(cmd.Context())
			if err != nil {
				return err
			}
			c := newWorkspaceClient(cmd.Context(), active.OrgID)
			ws, err := c.ListWorkspaces()
			if err != nil {
				return fmt.Errorf("list workspaces: %w", err)
			}
			if len(ws) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no workspaces) — run `bitwave init --cloud --name <n>` to create one")
				return nil
			}
			currentId := loadCurrentWorkspaceId(cmd.Context())
			for _, w := range ws {
				marker := "  "
				if w.Id == currentId {
					marker = "* "
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s%-32s  %s  (%s)\n", marker, w.Id, w.Name, w.BaseCurrency)
			}
			return nil
		},
	}
}

func newWorkspaceCurrentCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "current",
		Short: "Print the workspace recorded in the cwd's .bitwave.toml",
		RunE: func(cmd *operation.Call, _ []string) error {
			cfg, _, err := loadCwdConfig(cmd.Context())
			if err != nil {
				return err
			}
			if cfg.Mode != config.ModeCloud {
				return fmt.Errorf("workspace at cwd is in %s mode, not cloud", cfg.Mode)
			}
			if cfg.WorkspaceId == "" {
				return fmt.Errorf("no workspace_id in .bitwave.toml — run `bitwave workspace use`")
			}
			fmt.Fprintln(cmd.OutOrStdout(), cfg.WorkspaceId)
			return nil
		},
	}
}

func newWorkspaceUseCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "use [workspaceId]",
		Short: "Point the cwd .bitwave.toml at a cloud workspace. Bare invocation shows a picker.",
		Args:  operation.MaximumNArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			active, err := requireActiveOrg(cmd.Context())
			if err != nil {
				return err
			}
			cwd := operation.RuntimeFrom(cmd.Context()).Options.WorkingDirectory
			c := newWorkspaceClient(cmd.Context(), active.OrgID)

			var picked workspaces.Workspace
			if len(args) == 1 {
				ws, err := c.ListWorkspaces()
				if err != nil {
					return fmt.Errorf("list workspaces: %w", err)
				}
				for _, w := range ws {
					if w.Id == args[0] {
						picked = w
						break
					}
				}
				if picked.Id == "" {
					return fmt.Errorf("workspace %s not found in org %s", args[0], active.OrgID)
				}
			} else {
				ws, err := c.ListWorkspaces()
				if err != nil {
					return fmt.Errorf("list workspaces: %w", err)
				}
				if len(ws) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No workspaces. Run: bitwave init --cloud --name <name>")
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Pick a workspace:")
				for i, w := range ws {
					fmt.Fprintf(cmd.OutOrStdout(), "  [%d] %s  (%s)\n", i+1, w.Name, w.Id)
				}
				fmt.Fprint(cmd.OutOrStdout(), "> ")
				rdr := bufio.NewReader(cmd.InOrStdin())
				line, _ := rdr.ReadString('\n')
				n, err := strconv.Atoi(strings.TrimSpace(line))
				if err != nil || n < 1 || n > len(ws) {
					return fmt.Errorf("invalid selection")
				}
				picked = ws[n-1]
			}

			// Rebinding reads only the old local marker for display defaults.
			// It must not authorize/read the old org's workspace data. The target
			// workspace was resolved above using the explicit request org/token.
			cfg, dir, err := readCwdConfig(cmd.Context())
			if err != nil && !errors.Is(err, config.ErrNotAWorkspace) {
				return err
			}
			if cfg == nil {
				cfg = &config.Config{}
				dir = cwd
			}
			cfg.Mode = config.ModeCloud
			cfg.OrgId = active.OrgID
			cfg.WorkspaceId = picked.Id
			if cfg.Name == "" {
				cfg.Name = picked.Name
			}
			if cfg.BaseCurrency == "" {
				cfg.BaseCurrency = picked.BaseCurrency
			}
			if err := config.SaveFS(operation.RuntimeFrom(cmd.Context()).Files, dir, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Active workspace: %s (%s)\n", picked.Name, picked.Id)
			return nil
		},
	}
}

func newWorkspaceCreateCmd() *operation.Definition {
	var name, baseCurrency string
	cmd := &operation.Definition{
		Use:   "create --name <n> [--base-currency USD]",
		Short: "Create a workspace in the active org without binding cwd",
		Long: `bitwave workspace create makes an empty cloud workspace under the active
org. It does NOT touch cwd's .bitwave.toml — for that use ` + "`bitwave init --cloud`" + `
which creates the workspace and binds the directory in one step.`,
		RunE: func(cmd *operation.Call, _ []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if baseCurrency == "" {
				baseCurrency = "USD"
			}
			active, err := requireActiveOrg(cmd.Context())
			if err != nil {
				return err
			}
			c := newWorkspaceClient(cmd.Context(), active.OrgID)
			res, err := c.CreateWorkspace(workspaces.CreateWorkspaceRequest{
				Name:         name,
				BaseCurrency: baseCurrency,
			})
			if err != nil {
				return fmt.Errorf("create workspace: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created workspace: %s (%s)\n", name, res.Id)
			if res.URL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "View online: %s\n", res.URL)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Workspace name")
	cmd.Flags().StringVar(&baseCurrency, "base-currency", "USD", "Workspace base currency")
	return cmd
}

// loadCwdConfig finds the nearest .bitwave.toml from cwd and returns it.
func loadCwdConfig(ctx context.Context) (*config.Config, string, error) {
	cfg, dir, err := readCwdConfig(ctx)
	if err != nil {
		return nil, dir, err
	}
	if cfg.Mode == config.ModeCloud && cfg.OrgId != operation.RuntimeFrom(ctx).Options.OrganizationID {
		return nil, dir, fmt.Errorf("cloud workspace organization %q does not match request organization %q", cfg.OrgId, operation.RuntimeFrom(ctx).Options.OrganizationID)
	}
	return cfg, dir, nil
}

// readCwdConfig reads only the local marker through the request filesystem.
// Workspace data access must use loadCwdConfig's org-bound validation instead.
func readCwdConfig(ctx context.Context) (*config.Config, string, error) {
	runtime := operation.RuntimeFrom(ctx)
	cwd := runtime.Options.WorkingDirectory
	dir, err := config.FindFS(runtime.Files, cwd)
	if err != nil {
		if errors.Is(err, config.ErrNotAWorkspace) {
			return nil, "", fmt.Errorf("%w — run `bitwave init` here (or `cd` to an existing workspace) before this command", err)
		}
		return nil, "", err
	}
	cfg, err := config.LoadFS(runtime.Files, dir)
	if err != nil {
		return nil, dir, err
	}
	return cfg, dir, nil
}

// loadCurrentWorkspaceId returns "" when cwd is not bound to a cloud workspace.
func loadCurrentWorkspaceId(ctx context.Context) string {
	cfg, _, err := loadCwdConfig(ctx)
	if err != nil {
		return ""
	}
	if cfg.Mode != config.ModeCloud {
		return ""
	}
	return cfg.WorkspaceId
}
