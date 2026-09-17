package cmd

// This is the terminal frontend for the shared business SDK. Cobra only owns
// command selection, flags, help and shell completion. Business handlers run
// through the same typed SDK entry point as hosted adapters.
import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/bitwave-io/bitwave-cli/internal/operations"
	"github.com/bitwave-io/bitwave-cli/internal/orgctx"
	"github.com/bitwave-io/bitwave-cli/sdk"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Retain the historical linker override, now consumed exclusively by the
// terminal adapter rather than by request-scoped business code.
var defaultBlockchainQueryBaseURL = "https://api4.bitwave.io"

func sdkCommand(d *operation.Definition) *cobra.Command {
	c := &cobra.Command{
		Use: d.Use, Short: d.Short, Long: d.Long, Example: d.Example,
		Aliases: d.Aliases, Hidden: d.Hidden,
	}
	c.Flags().AddFlagSet(d.Flags())
	d.Flags().VisitAll(func(flag *pflag.Flag) {
		if d.Required(flag.Name) {
			_ = c.MarkFlagRequired(flag.Name)
		}
	})
	if d.Args != nil {
		c.Args = func(_ *cobra.Command, args []string) error { return d.Args.Validate(args) }
	}
	if d.Runnable() {
		c.RunE = func(cmd *cobra.Command, args []string) error {
			input, err := sdkArguments(d, args)
			if err != nil {
				return err
			}
			opts, err := terminalSDKOptions(cmd)
			if err != nil {
				return err
			}
			_, invokeErr := sdk.NewClient(opts).Invoke(cmd.Context(), sdk.Request{
				Operation: operation.ToolName(d.Path()), Arguments: input, Input: cmd.InOrStdin(),
				Output: cmd.OutOrStdout(), Diagnostics: cmd.ErrOrStderr(),
			})
			return invokeErr
		}
	}
	for _, child := range d.Commands() {
		c.AddCommand(sdkCommand(child))
	}
	return c
}

func sdkArguments(d *operation.Definition, args []string) (json.RawMessage, error) {
	input := map[string]any{"arguments": append([]string{}, args...)}
	var err error
	// Cobra owns a different FlagSet containing these shared Flag pointers.
	// Its parse marks Flag.Changed but does not populate this set's Visit map.
	d.Flags().VisitAll(func(flag *pflag.Flag) {
		if err != nil || !flag.Changed {
			return
		}
		input[flag.Name], err = terminalFlagJSON(flag)
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(input)
}

func terminalFlagJSON(flag *pflag.Flag) (any, error) {
	kind := strings.ToLower(flag.Value.Type())
	if slice, ok := flag.Value.(pflag.SliceValue); ok {
		values := slice.GetSlice()
		if values == nil {
			values = []string{}
		}
		switch kind {
		case "intslice", "int32slice", "int64slice", "uintslice", "float32slice", "float64slice":
			out := make([]json.Number, len(values))
			for i, value := range values {
				out[i] = json.Number(value)
			}
			return out, nil
		case "boolslice":
			out := make([]bool, len(values))
			for i, value := range values {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return nil, err
				}
				out[i] = parsed
			}
			return out, nil
		default:
			return values, nil
		}
	}
	switch kind {
	case "bool":
		return strconv.ParseBool(flag.Value.String())
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "count", "float32", "float64":
		return json.Number(flag.Value.String()), nil
	default:
		return flag.Value.String(), nil
	}
}

// terminalSDKOptions is the only bridge from ambient terminal settings into
// shared operations. Hosted adapters construct sdk.Options themselves and
// never call this function or consult credential files/environment variables.
func terminalSDKOptions(cmd *cobra.Command) (sdk.Options, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return sdk.Options{}, err
	}
	org := ""
	if active, err := orgctx.Load(); err == nil {
		org = active.OrgID
	}
	if flag := cmd.Flags().Lookup("org"); flag != nil && flag.Changed {
		org = flag.Value.String()
	}
	baseQuery := os.Getenv("BITWAVE_BASE_URL_BLOCKCHAIN_QUERY")
	if baseQuery == "" {
		baseQuery = defaultBlockchainQueryBaseURL
	}
	var mu sync.Mutex
	resolvers := map[string]func() (string, error){}
	resolve := func(ctx context.Context, orgID string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		mu.Lock()
		defer mu.Unlock()
		resolver := resolvers[orgID]
		if resolver == nil {
			if orgID == "" {
				resolver = makeTokenResolver()
			} else {
				resolver = makeOrgTokenResolver(orgID)
			}
			resolvers[orgID] = resolver
		}
		return resolver()
	}
	return sdk.Options{
		WorkingDirectory: cwd, OrganizationID: org, TokenResolver: resolve,
		CoreBaseURL: resolveCoreBaseURL(), GLBaseURL: resolveGLBaseURL(),
		BlockchainQueryBaseURL: baseQuery, IdentityEmail: resolveIdentityEmail(),
		UnrestrictedFiles: true, AllowEndpointOverrides: true,
	}, nil
}

func sdkDefinition(path ...string) *operation.Definition {
	d := operations.NewRoot()
	for _, part := range path {
		var found *operation.Definition
		for _, child := range d.Commands() {
			if child.Name() == part {
				found = child
				break
			}
		}
		if found == nil {
			panic("unknown shared operation path: " + strings.Join(path, " "))
		}
		d = found
	}
	return d
}
