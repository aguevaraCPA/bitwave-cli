package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/bitwave-io/bitwave-cli/internal/operations"
	"github.com/spf13/pflag"
)

const ToolName = "run_bitwave_cli"
const ToolProvider = "bitwave-cli"
const ToolDescription = "Call the shared Bitwave SDK using familiar CLI-style arguments. No terminal, shell, executable, or subprocess is invoked. Pass args without bitwave; empty args returns operation help. Prefer --json for structured results. For organization balances use report balance; bal reads a separate local plain-text ledger. Use stdin for payloads consumed by --input -."

var ToolInputSchema = json.RawMessage(`{"type":"object","properties":{"args":{"type":"array","items":{"type":"string"},"default":[]},"stdin":{"type":"string","description":"Request-local stdin payload for --input - or --body-file -."}},"additionalProperties":false}`)

// ExecuteOptions preserves the legacy argv adapter contract. New typed callers
// should use Client.Invoke. Both adapters call the same business handlers.
type ExecuteOptions struct {
	Args                                                []string
	WorkingDirectory, OrganizationID, Token, AgentToken string
	Input                                               io.Reader
	ClientOptions                                       Options
}
type CommandResult struct {
	Command         []string `json:"command"`
	Operation       string   `json:"operation,omitempty"`
	Directory       string   `json:"directory"`
	ExitCode        int      `json:"exitCode"`
	Stdout          string   `json:"stdout,omitempty"`
	Stderr          string   `json:"stderr,omitempty"`
	Truncated       bool     `json:"truncated,omitempty"`
	StdoutTruncated bool     `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool     `json:"stderrTruncated,omitempty"`
	// Err preserves the original typed/wrapped failure for in-process callers.
	// It is deliberately not serialized. Terminal Stderr behavior is unchanged.
	Err error `json:"-"`
}

func ValidateArgs(args []string) error {
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return errors.New("refusing a Bitwave argument containing a NUL byte")
		}
	}
	return nil
}
func NormalizeArgs(args []string) []string {
	if len(args) == 0 {
		return []string{"--help"}
	}
	return append([]string(nil), args...)
}
func ExecuteWithOptions(ctx context.Context, o ExecuteOptions) CommandResult {
	args := NormalizeArgs(o.Args)
	opts := o.ClientOptions
	if o.WorkingDirectory != "" {
		opts.WorkingDirectory = o.WorkingDirectory
	}
	if o.OrganizationID != "" {
		opts.OrganizationID = o.OrganizationID
	}
	if o.Token != "" {
		opts.Token = o.Token
	}
	if o.AgentToken != "" {
		opts.AgentToken = o.AgentToken
	}
	out := CommandResult{Command: append([]string{"bitwave"}, args...), Directory: opts.WorkingDirectory}
	req, help, err := ParseCommand(args)
	if err == nil && help != "" {
		out.Stdout = help
		return out
	}
	if err == nil {
		out.Operation = req.Operation
		req.Input = o.Input
		var result Result
		result, err = NewClient(opts).Invoke(ctx, req)
		out.Stdout, out.Stderr, out.Truncated = result.Output, result.Diagnostics, result.Truncated
		out.StdoutTruncated, out.StderrTruncated = result.OutputTruncated, result.DiagnosticsTruncated
	}
	if err != nil {
		out.Err = err
		out.ExitCode = 1
		if errors.Is(err, context.Canceled) {
			out.ExitCode = 130
		}
		out.Stderr += err.Error() + "\n"
	}
	return out
}
func Execute(ctx context.Context, args []string, organizationID string) CommandResult {
	return ExecuteWithOptions(ctx, ExecuteOptions{Args: args, OrganizationID: organizationID})
}

// ParseCommand is syntax adaptation only: it maps a command path and pflag
// values to the SDK's structured request. It never invokes a command engine.
func ParseCommand(args []string) (Request, string, error) {
	if err := ValidateArgs(args); err != nil {
		return Request{}, "", err
	}
	root := operations.NewRoot()
	d := root
	args = NormalizeArgs(args)
	for len(args) > 0 {
		if args[0] == "--quiet" || strings.HasPrefix(args[0], "--quiet=") {
			args = args[1:]
			continue
		}
		var child *operation.Definition
		for _, candidate := range d.Commands() {
			if candidate.Name() == args[0] {
				child = candidate
				break
			}
			for _, a := range candidate.Aliases {
				if a == args[0] {
					child = candidate
					break
				}
			}
		}
		if child == nil {
			break
		}
		d = child
		args = args[1:]
	}
	// Parse adapter flags with the parameter parser too: a string value equal
	// to "--help" or "--quiet" must remain data, not become an instruction.
	flags := d.Flags()
	flags.BoolP("help", "h", false, "Show operation help")
	flags.Bool("quiet", false, "Compatibility flag; SDK output is always scoped")
	if err := flags.Parse(args); err != nil {
		return Request{}, "", err
	}
	help, _ := flags.GetBool("help")
	if help || (!d.Runnable() && len(flags.Args()) == 0) {
		return Request{}, operationHelp(d), nil
	}
	if !d.Runnable() {
		return Request{}, "", fmt.Errorf("unknown or non-executable Bitwave operation %q", strings.Join(args, " "))
	}
	input := map[string]any{"arguments": d.Flags().Args()}
	var bindErr error
	d.Flags().Visit(func(f *pflag.Flag) {
		if f.Name == "help" || f.Name == "quiet" {
			return
		}
		if bindErr != nil {
			return
		}
		input[f.Name], bindErr = flagJSON(d.Flags(), f)
	})
	if bindErr != nil {
		return Request{}, "", bindErr
	}
	raw, err := json.Marshal(input)
	return Request{Operation: operation.ToolName(d.Path()), Arguments: raw}, "", err
}
func flagJSON(fs *pflag.FlagSet, f *pflag.Flag) (any, error) {
	switch f.Value.Type() {
	case "bool":
		return fs.GetBool(f.Name)
	case "stringSlice", "stringArray":
		if v, ok := f.Value.(pflag.SliceValue); ok {
			return v.GetSlice(), nil
		}
	case "stringToString":
		return fs.GetStringToString(f.Name)
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
		return json.Number(f.Value.String()), nil
	case "boolSlice":
		v, e := fs.GetBoolSlice(f.Name)
		return v, e
	}
	if v, ok := f.Value.(pflag.SliceValue); ok {
		out := make([]any, 0)
		for _, s := range v.GetSlice() {
			if _, e := strconv.ParseFloat(s, 64); e != nil {
				return nil, e
			}
			out = append(out, json.Number(s))
		}
		return out, nil
	}
	return f.Value.String(), nil
}
func operationHelp(d *operation.Definition) string {
	var b strings.Builder
	fmt.Fprintln(&b, d.Short)
	fmt.Fprintf(&b, "\nUsage:\n  %s\n", d.CommandPath())
	if d.Long != "" {
		fmt.Fprintln(&b, "\n"+d.Long)
	}
	if len(d.Commands()) > 0 {
		fmt.Fprintln(&b, "\nAvailable Commands:")
		for _, child := range d.Commands() {
			fmt.Fprintf(&b, "  %-24s %s\n", child.Name(), child.Short)
		}
	}
	if d.Flags().HasFlags() {
		fmt.Fprintln(&b, "\nFlags:")
		b.WriteString(d.Flags().FlagUsages())
	}
	return b.String()
}
