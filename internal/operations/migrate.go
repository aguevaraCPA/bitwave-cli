package operations

import (
	"fmt"
	"path/filepath"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/bitwave/config"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/workspaces"
)

func newMigrateCmd() *operation.Definition {
	var name, invite string
	cmd := &operation.Definition{
		Use:   "migrate",
		Short: "Push a local workspace up to a new cloud workspace under the active org",
		Long: `Migrate the cwd's local workspace to Bitwave's workspace ledger
service (the cloud-hosted journal/entry model — not the Bitwave platform API):
  1. Create a new LedgerWorkspace in the active org
  2. Push every journal's entries (and the accounts/prices) into it
  3. Rewrite .bitwave.toml to mode=cloud + the new ids
  4. Move local *.ledger / *.journal files to *.bak for rollback

--invite <email> (NOT YET IMPLEMENTED) would target an existing user's org
via the bitwave delegation flow. For now this errors out — pass --invite once
the server-side delegation endpoint lands.`,
		RunE: func(cmd *operation.Call, _ []string) error {
			if invite != "" {
				return fmt.Errorf("--invite requires the bitwave delegation flow; not yet implemented")
			}
			cfg, dir, err := loadCwdConfig(cmd.Context())
			if err != nil {
				return err
			}
			if cfg.Mode != config.ModeLocal {
				return fmt.Errorf("only local workspaces can be migrated (current mode: %s)", cfg.Mode)
			}

			active, err := requireActiveOrg(cmd.Context())
			if err != nil {
				return err
			}

			ls, err := store.OpenLocalFS(operation.RuntimeFrom(cmd.Context()).Files, dir)
			if err != nil {
				return err
			}
			proj, err := ls.Project(cmd.Context())
			if err != nil {
				return err
			}
			for i, e := range proj.Entries {
				if !e.IsBalanced(cfg.BaseCurrency) {
					return fmt.Errorf("entry %d (%s) does not balance — fix locally before migrating", i, e.Date.Format("2006-01-02"))
				}
			}

			workspaceName := name
			if workspaceName == "" {
				workspaceName = cfg.Name
			}
			if workspaceName == "" {
				workspaceName = filepath.Base(dir)
			}

			wc := newWorkspaceClient(cmd.Context(), active.OrgID)
			ws, err := wc.CreateWorkspace(workspaces.CreateWorkspaceRequest{
				Name:         workspaceName,
				BaseCurrency: cfg.BaseCurrency,
			})
			if err != nil {
				return fmt.Errorf("create cloud workspace: %w", err)
			}

			// Push each local journal as its own cloud journal so the multi-
			// journal layout survives the migration.
			cs := newCloudStore(cmd.Context(), active.OrgID, ws.Id)
			journalIds, err := ls.JournalIds()
			if err != nil {
				return err
			}
			if len(journalIds) == 0 {
				journalIds = []string{store.DefaultJournal}
			}
			for _, jid := range journalIds {
				if err := cs.EnsureJournal(cmd.Context(), jid); err != nil {
					return fmt.Errorf("ensure cloud journal %s: %w", jid, err)
				}
			}

			// Accounts/prices live at the workspace level — push the whole
			// project once but routed through the first journal for entries.
			// Then the remaining journals get only their own entries.
			if len(journalIds) == 1 {
				if err := cs.Import(cmd.Context(), journalIds[0], proj); err != nil {
					return fmt.Errorf("import to cloud: %w", err)
				}
			} else {
				wsLevel := *proj
				wsLevel.Entries = nil
				if err := cs.Import(cmd.Context(), journalIds[0], &wsLevel); err != nil {
					return fmt.Errorf("import accounts/prices: %w", err)
				}
				for _, jid := range journalIds {
					ents, err := ls.ParseJournalEntries(jid)
					if err != nil {
						return fmt.Errorf("parse %s: %w", jid, err)
					}
					if len(ents) == 0 {
						continue
					}
					sub := *proj
					sub.Accounts = nil
					sub.Prices = nil
					sub.Entries = ents
					if err := cs.Import(cmd.Context(), jid, &sub); err != nil {
						return fmt.Errorf("import journal %s: %w", jid, err)
					}
				}
			}

			cfg.Mode = config.ModeCloud
			cfg.OrgId = active.OrgID
			cfg.WorkspaceId = ws.Id
			cfg.Name = workspaceName
			if err := config.SaveFS(operation.RuntimeFrom(cmd.Context()).Files, dir, cfg); err != nil {
				return fmt.Errorf("update %s: %w", config.FileName, err)
			}

			// Move local files aside.
			renameToBak(cmd, filepath.Join(dir, store.AccountsFile))
			renameToBak(cmd, filepath.Join(dir, store.PricesFile))
			for _, jid := range journalIds {
				renameToBak(cmd, filepath.Join(dir, jid+store.JournalExt))
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Migrated to cloud workspace %s (org %s)\n", ws.Id, active.OrgID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Cloud workspace name (defaults to local workspace name)")
	cmd.Flags().StringVar(&invite, "invite", "", "Delegate ownership to this email (delegation flow — not yet implemented)")
	return cmd
}

func renameToBak(cmd *operation.Call, p string) {
	files := operation.RuntimeFrom(cmd.Context()).Files
	if _, err := files.Stat(p); err == nil {
		_ = files.Rename(p, p+".bak")
	}
}
