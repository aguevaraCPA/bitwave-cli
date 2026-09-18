package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bitwave-io/bitwave-cli/internal/orgctx"
	"github.com/bitwave-io/bitwave-cli/internal/orgs"
)

func newOrgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Manage the active organization context",
		Long: `Cloud bitwave commands inherit an active org from ~/.bitwave/config.json. Use
` + "`bitwave org use`" + ` / ` + "`bitwave org create`" + ` to manage it instead of passing
--org-id on every call.`,
	}
	cmd.AddCommand(newOrgCurrentCmd())
	cmd.AddCommand(newOrgUseCmd())
	cmd.AddCommand(newOrgCreateCmd())
	cmd.AddCommand(newOrgClearCmd())
	for _, definition := range sdkDefinition("org").Commands() {
		if definition.Name() != "current" {
			cmd.AddCommand(sdkCommand(definition))
		}
	}
	return cmd
}

func newOrgCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Print the active org id and name",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := orgctx.Load()
			if err != nil {
				if errors.Is(err, orgctx.ErrNoActiveOrg) {
					fmt.Fprintln(cmd.ErrOrStderr(), "No active org. Run `bitwave org use` to pick one.")
				}
				return err
			}
			if a.OrgName != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  (%s)\n", a.OrgID, a.OrgName)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), a.OrgID)
			}
			return nil
		},
	}
}

func newOrgUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use [orgId]",
		Short: "Set the active org. Bare invocation shows a picker.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				orgID := args[0]
				orgName := lookupOrgName(orgID)
				if err := orgctx.Save(&orgctx.Active{OrgID: orgID, OrgName: orgName}); err != nil {
					return err
				}
				if orgName == "" {
					fmt.Printf("Active org: %s\n", orgID)
				} else {
					fmt.Printf("Active org: %s (%s)\n", orgName, orgID)
				}
				return nil
			}
			c := orgs.New(resolveCoreBaseURL(), makeTokenResolver())
			list, err := c.List()
			if err != nil {
				return fmt.Errorf("list orgs: %w", err)
			}
			if len(list) == 0 {
				fmt.Println("You have no orgs. Run: bitwave org create --name <name>")
				return nil
			}
			fmt.Println("Pick an org:")
			for i, o := range list {
				fmt.Printf("  [%d] %s  (%s)\n", i+1, o.Name, o.ID)
			}
			fmt.Print("> ")
			rdr := bufio.NewReader(os.Stdin)
			line, _ := rdr.ReadString('\n')
			n, err := strconv.Atoi(strings.TrimSpace(line))
			if err != nil || n < 1 || n > len(list) {
				return fmt.Errorf("invalid selection")
			}
			pick := list[n-1]
			if err := orgctx.Save(&orgctx.Active{OrgID: pick.ID, OrgName: pick.Name}); err != nil {
				return err
			}
			fmt.Printf("Active org: %s (%s)\n", pick.Name, pick.ID)
			return nil
		},
	}
}

func lookupOrgName(id string) string {
	c := orgs.New(resolveCoreBaseURL(), makeTokenResolver())
	list, err := c.List()
	if err != nil {
		return ""
	}
	for _, o := range list {
		if o.ID == id {
			return o.Name
		}
	}
	return ""
}

func newOrgCreateCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new org and use it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				fmt.Print("Org name: ")
				rdr := bufio.NewReader(os.Stdin)
				line, _ := rdr.ReadString('\n')
				name = strings.TrimSpace(line)
				if name == "" {
					return fmt.Errorf("org name is required")
				}
			}
			c := orgs.New(resolveCoreBaseURL(), makeTokenResolver())
			o, err := c.Create(orgs.CreateRequest{Name: name})
			if err != nil {
				return fmt.Errorf("create org: %w", err)
			}
			if err := orgctx.Save(&orgctx.Active{OrgID: o.ID, OrgName: o.Name}); err != nil {
				return fmt.Errorf("save active org: %w", err)
			}
			fmt.Printf("Created and switched to: %s (%s)\n", o.Name, o.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Org name")
	return cmd
}

func newOrgClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove the active-org pointer",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return orgctx.Save(&orgctx.Active{})
		},
	}
}
