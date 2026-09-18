package operations

import (
	"context"
	"fmt"
	"strings"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/bitwave/config"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/workspaces"
)

func newJournalCmd() *operation.Definition {
	cmd := &operation.Definition{
		Use:   "journal",
		Short: "Manage journals within the current workspace",
		Long: `Workspaces hold one or more journals. In local mode each journal is a
<id>.journal file in the workspace dir; in cloud mode it's a row in
the cloud ledger.

` + "`bitwave journal use`" + ` records a default journal id in .bitwave.toml so
` + "`bitwave je new`" + ` doesn't have to keep passing --journal.`,
	}
	cmd.AddCommand(newJournalListCmd())
	cmd.AddCommand(newJournalNewCmd())
	cmd.AddCommand(newJournalUseCmd())
	return cmd
}

func newJournalListCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "list",
		Short: "List journals in the current workspace",
		RunE: func(cmd *operation.Call, _ []string) error {
			cfg, dir, err := loadCwdConfig(cmd.Context())
			if err != nil {
				return err
			}
			ids, names, err := listJournals(cmd.Context(), cfg, dir)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no journals)")
				return nil
			}
			for i, id := range ids {
				marker := "  "
				if id == cfg.DefaultJournal {
					marker = "* "
				}
				if names[i] != "" && names[i] != id {
					fmt.Fprintf(cmd.OutOrStdout(), "%s%-24s  %s\n", marker, id, names[i])
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n", marker, id)
				}
			}
			return nil
		},
	}
}

func newJournalNewCmd() *operation.Definition {
	var name, description string
	cmd := &operation.Definition{
		Use:   "new <id>",
		Short: "Create a journal in the current workspace",
		Args:  operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return fmt.Errorf("journal id is required")
			}
			cfg, dir, err := loadCwdConfig(cmd.Context())
			if err != nil {
				return err
			}
			switch cfg.Mode {
			case config.ModeLocal:
				ws, err := store.OpenLocalFS(operation.RuntimeFrom(cmd.Context()).Files, dir)
				if err != nil {
					return err
				}
				if err := ws.EnsureJournal(cmd.Context(), id); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Created journal: %s\n", id)
				return nil
			case config.ModeCloud:
				if cfg.OrgId == "" || cfg.WorkspaceId == "" {
					return fmt.Errorf(".bitwave.toml is missing org_id or workspace_id")
				}
				c := newWorkspaceClient(cmd.Context(), cfg.OrgId)
				if name == "" {
					name = titleCase(strings.ReplaceAll(id, "-", " "))
				}
				newId, err := c.CreateJournal(cfg.WorkspaceId, workspaces.CreateJournalRequest{
					Id:          id,
					Name:        name,
					Description: description,
				})
				if err != nil {
					return fmt.Errorf("create journal: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Created journal: %s\n", newId)
				return nil
			default:
				return fmt.Errorf("unknown workspace mode: %s", cfg.Mode)
			}
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Journal display name (cloud mode; defaults to title-cased id)")
	cmd.Flags().StringVar(&description, "description", "", "Journal description (cloud mode)")
	return cmd
}

func newJournalUseCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "use <id>",
		Short: "Set the default journal for `bitwave je new` and other writes",
		Args:  operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			id := strings.TrimSpace(args[0])
			cfg, dir, err := loadCwdConfig(cmd.Context())
			if err != nil {
				return err
			}
			ids, _, err := listJournals(cmd.Context(), cfg, dir)
			if err != nil {
				return err
			}
			found := false
			for _, j := range ids {
				if j == id {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("journal %s does not exist (run `bitwave journal new %s`)", id, id)
			}
			cfg.DefaultJournal = id
			if err := config.SaveFS(operation.RuntimeFrom(cmd.Context()).Files, dir, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Default journal: %s\n", id)
			return nil
		},
	}
}

// titleCase upper-cases the first rune of every space-separated word. Avoids
// strings.Title (deprecated) without dragging in x/text/cases for one helper.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// listJournals returns journal ids and parallel display names for the active
// workspace. Cloud mode hits the cloud ledger; local mode reads files.
func listJournals(ctx context.Context, cfg *config.Config, dir string) ([]string, []string, error) {
	switch cfg.Mode {
	case config.ModeLocal:
		ws, err := store.OpenLocalFS(operation.RuntimeFrom(ctx).Files, dir)
		if err != nil {
			return nil, nil, err
		}
		ids, err := ws.JournalIds()
		if err != nil {
			return nil, nil, err
		}
		names := make([]string, len(ids))
		return ids, names, nil
	case config.ModeCloud:
		if cfg.OrgId == "" || cfg.WorkspaceId == "" {
			return nil, nil, fmt.Errorf(".bitwave.toml is missing org_id or workspace_id")
		}
		c := newWorkspaceClient(ctx, cfg.OrgId)
		js, err := c.ListJournals(cfg.WorkspaceId)
		if err != nil {
			return nil, nil, fmt.Errorf("list journals: %w", err)
		}
		ids := make([]string, len(js))
		names := make([]string, len(js))
		for i, j := range js {
			ids[i] = j.Id
			names[i] = j.Name
		}
		return ids, names, nil
	default:
		return nil, nil, fmt.Errorf("unknown workspace mode: %s", cfg.Mode)
	}
}
