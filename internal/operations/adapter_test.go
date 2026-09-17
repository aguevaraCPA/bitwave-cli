package operations

// Legacy command-contract fixtures exercise the extracted business handlers
// through a TEST-ONLY terminal adapter. Production SDK code has no Cobra or
// ambient environment/cwd dependency.
import (
	"context"
	op "github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/bitwave-io/bitwave-cli/internal/orgctx"
	"github.com/spf13/cobra"
	"os"
)

func NewRootCmd() *cobra.Command { return testDefinition(NewRoot()) }
func testDefinition(d *op.Definition) *cobra.Command {
	c := &cobra.Command{Use: d.Use, Short: d.Short, Long: d.Long, Example: d.Example, Aliases: d.Aliases, Hidden: d.Hidden, SilenceUsage: true, SilenceErrors: true}
	c.Flags().AddFlagSet(d.Flags())
	if d.Runnable() {
		c.RunE = func(cmd *cobra.Command, args []string) error {
			wd, _ := os.Getwd()
			org := os.Getenv("BITWAVE_ORG_ID")
			if org == "" {
				if active, e := orgctx.Load(); e == nil {
					org = active.OrgID
				}
			}
			if flag := cmd.Flags().Lookup("org"); flag != nil && flag.Changed {
				org = flag.Value.String()
			}
			rt, e := op.NewRuntime(op.Options{WorkingDirectory: wd, OrganizationID: org, Token: os.Getenv("BITWAVE_TOKEN"), AgentToken: os.Getenv("BITWAVE_AGENT_TOKEN"), CoreBaseURL: os.Getenv("BITWAVE_BASE_URL_CORE"), GLBaseURL: os.Getenv("BITWAVE_BASE_URL_GL"), BlockchainQueryBaseURL: os.Getenv("BITWAVE_BASE_URL_BLOCKCHAIN_QUERY"), UnrestrictedFiles: true})
			if e != nil {
				return e
			}
			defer rt.Close()
			ctx := op.WithRuntime(cmd.Context(), rt)
			return d.Invoke(op.NewCall(ctx, d, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()), args)
		}
	}
	for _, child := range d.Commands() {
		c.AddCommand(testDefinition(child))
	}
	c.SetContext(context.Background())
	return c
}
