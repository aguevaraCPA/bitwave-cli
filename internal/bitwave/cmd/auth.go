package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bitwave-io/bitwave-cli/internal/auth"
	"github.com/bitwave-io/bitwave-cli/internal/orgctx"
	"github.com/bitwave-io/bitwave-cli/internal/orgs"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with Bitwave",
	}
	cmd.AddCommand(newAuthLoginCmd())
	cmd.AddCommand(newAuthLogoutCmd())
	cmd.AddCommand(newAuthStatusCmd())
	cmd.AddCommand(newAuthDelegateCmd())
	cmd.AddCommand(newAuthResumeCmd())
	cmd.AddCommand(newAuthAgentCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var clientID, clientSecret, orgID string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in and select a Bitwave organization",
		Long: `Sign in to Bitwave.

The default flow opens Bitwave login in the browser and then prompts you to
select an organization. Agents should pass ` + "`--orgId ORG_ID`" + ` to select and
verify the requested organization without an interactive picker.

For headless or managed environments, an already-provisioned
BITWAVE_AGENT_TOKEN can be used. OAuth client credentials remain available as
an advanced compatibility path through --client-id / --client-secret, but
normal users should not be asked to choose or create them.

Tokens land in ~/.bitwave/credentials.json and are auto-refreshed.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if clientID == "" {
				clientID = os.Getenv("BITWAVE_CLIENT_ID")
			}
			if clientSecret == "" {
				clientSecret = os.Getenv("BITWAVE_CLIENT_SECRET")
			}
			if clientID != "" || clientSecret != "" {
				if clientID == "" || clientSecret == "" {
					return fmt.Errorf("both --client-id and --client-secret are required for client credentials login")
				}
				creds, err := auth.ClientCredentialsLogin(resolveAuthURL(), clientID, clientSecret)
				if err != nil {
					return err
				}
				if err := auth.SaveCredentials(creds); err != nil {
					return err
				}
				auth.PrintLoginSuccess("", creds.ExpiresAt)
				return selectAuthenticatedOrg(cmd, orgID)
			}
			if err := auth.Login(resolveAuthURL()); err != nil {
				return err
			}
			return selectAuthenticatedOrg(cmd, orgID)
		},
	}
	cmd.Flags().StringVar(&orgID, "orgId", "", "Organization ID to verify and select after login (recommended for agents)")
	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth client ID (env: BITWAVE_CLIENT_ID)")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "OAuth client secret (env: BITWAVE_CLIENT_SECRET)")
	return cmd
}

func selectAuthenticatedOrg(cmd *cobra.Command, requestedOrgID string) error {
	requestedOrgID = strings.TrimSpace(requestedOrgID)
	if requestedOrgID != "" {
		client := orgs.New(resolveCoreBaseURL(), makeOrgTokenResolver(requestedOrgID))
		org, err := client.Get(requestedOrgID)
		if err != nil {
			return fmt.Errorf("verify access to organization %s: %w", requestedOrgID, err)
		}
		return saveAuthenticatedOrg(cmd, org)
	}

	client := orgs.New(resolveCoreBaseURL(), makeTokenResolver())
	available, err := client.List()
	if err != nil {
		return fmt.Errorf("list organizations after login: %w", err)
	}
	if len(available) == 0 {
		return fmt.Errorf("login succeeded, but this identity has no available organizations")
	}
	if len(available) == 1 {
		return saveAuthenticatedOrg(cmd, &available[0])
	}

	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Select an organization:")
	for index, org := range available {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  [%d] %s (%s)\n", index+1, org.Name, org.ID)
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), "> ")
	line, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if readErr != nil && strings.TrimSpace(line) == "" {
		return fmt.Errorf("organization selection requires input; agents should rerun with --orgId ORG_ID")
	}
	selection, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || selection < 1 || selection > len(available) {
		return fmt.Errorf("invalid organization selection; choose 1-%d or rerun with --orgId ORG_ID", len(available))
	}
	return saveAuthenticatedOrg(cmd, &available[selection-1])
}

func saveAuthenticatedOrg(cmd *cobra.Command, org *orgs.Org) error {
	if err := orgctx.Save(&orgctx.Active{OrgID: org.ID, OrgName: org.Name}); err != nil {
		return fmt.Errorf("save active organization: %w", err)
	}
	if org.Name == "" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Active org: %s\n", org.ID)
	} else {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Active org: %s (%s)\n", org.Name, org.ID)
	}
	return nil
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear stored credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return auth.Logout(resolveAuthURL())
		},
	}
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current identity, token source, and scopes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if v := os.Getenv("BITWAVE_AGENT_TOKEN"); v != "" {
				fmt.Println("Identity: agent (BITWAVE_AGENT_TOKEN env)")
				fmt.Println("Source:   environment")
				return nil
			}
			if v := os.Getenv("BITWAVE_TOKEN"); v != "" {
				fmt.Println("Identity: bearer token (BITWAVE_TOKEN env)")
				fmt.Println("Source:   environment")
				_ = v
				return nil
			}
			return auth.Status()
		},
	}
}

// newAuthDelegateCmd: server endpoint not yet built. Stub with a clear error
// pointing at what would happen.
func newAuthDelegateCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "delegate <email>",
		Short:  "Request delegated access from a user (spin-waits on email approval)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("auth delegate is not yet implemented (server-side delegation flow is pending). Email that would be contacted: %s", args[0])
		},
	}
}

func newAuthResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "resume",
		Short:  "Resume a pending login or delegation flow",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("auth resume is not yet implemented (no pending-flow store on this client)")
		},
	}
}

func newAuthAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "agent",
		Short:  "Manage well-known agent identities (issued tokens)",
		Hidden: true,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "create --name <n> [--workspace <id>]",
		Short: "Issue an agent token (org admin)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("auth agent create is not yet implemented (server-side issuance pending)")
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List agent tokens issued in the active org",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("auth agent list is not yet implemented")
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an agent token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("auth agent revoke is not yet implemented")
		},
	})
	return cmd
}
