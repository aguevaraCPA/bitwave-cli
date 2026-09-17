// Package operation defines business operations independently of terminal
// parsing, Cobra, process state and transport. A fresh definition tree binds
// invocation-local values; SDK callers dispatch handlers with structured input.
package operation

import (
	"context"
	"fmt"
	"github.com/spf13/pflag"
	"io"
	"sort"
	"strings"
)

type Bounds struct{ Min, Max int }

var NoArgs = &Bounds{0, 0}
var ArbitraryArgs = &Bounds{0, -1}

func ExactArgs(n int) *Bounds        { return &Bounds{n, n} }
func MinimumNArgs(n int) *Bounds     { return &Bounds{n, -1} }
func MaximumNArgs(n int) *Bounds     { return &Bounds{0, n} }
func RangeArgs(min, max int) *Bounds { return &Bounds{min, max} }
func (b *Bounds) Validate(args []string) error {
	if b == nil {
		return nil
	}
	if len(args) < b.Min || (b.Max >= 0 && len(args) > b.Max) {
		return fmt.Errorf("expected %d..%d positional arguments, got %d", b.Min, b.Max, len(args))
	}
	return nil
}

type Definition struct {
	Use, Short, Long, Example string
	Aliases                   []string
	Hidden                    bool
	Args                      *Bounds
	RunE                      func(*Call, []string) error
	Run                       func(*Call, []string)
	flags                     *pflag.FlagSet
	children                  []*Definition
	parent                    *Definition
	required                  map[string]bool
}

func (d *Definition) Flags() *pflag.FlagSet {
	if d.flags == nil {
		d.flags = pflag.NewFlagSet(d.Name(), pflag.ContinueOnError)
		d.flags.SetOutput(io.Discard)
	}
	return d.flags
}
func (d *Definition) Name() string {
	parts := strings.Fields(d.Use)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
func (d *Definition) AddCommand(children ...*Definition) {
	for _, c := range children {
		c.parent = d
		d.children = append(d.children, c)
	}
}
func (d *Definition) Commands() []*Definition {
	out := append([]*Definition(nil), d.children...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
func (d *Definition) Path() []string {
	if d.parent == nil {
		return nil
	}
	return append(d.parent.Path(), d.Name())
}
func (d *Definition) CommandPath() string { return "bitwave " + strings.Join(d.Path(), " ") }
func (d *Definition) Runnable() bool      { return d.RunE != nil || d.Run != nil }
func (d *Definition) MarkFlagRequired(name string) error {
	if d.Flags().Lookup(name) == nil {
		return fmt.Errorf("unknown flag %q", name)
	}
	if d.required == nil {
		d.required = map[string]bool{}
	}
	d.required[name] = true
	return nil
}
func (d *Definition) Required(name string) bool { return d.required[name] }
func (d *Definition) Invoke(c *Call, args []string) error {
	if err := d.Args.Validate(args); err != nil {
		return err
	}
	for n := range d.required {
		if !d.Flags().Changed(n) {
			return fmt.Errorf("%s is required", n)
		}
	}
	if d.RunE != nil {
		return d.RunE(c, args)
	}
	if d.Run != nil {
		d.Run(c, args)
		return nil
	}
	return fmt.Errorf("%s is an operation group, not an operation", strings.Join(d.Path(), " "))
}

type Call struct {
	Ctx                 context.Context
	Definition          *Definition
	Input               io.Reader
	Output, ErrorOutput io.Writer
}

func NewCall(ctx context.Context, d *Definition, in io.Reader, out, errOut io.Writer) *Call {
	return &Call{Ctx: ctx, Definition: d, Input: in, Output: out, ErrorOutput: errOut}
}
func (c *Call) Context() context.Context  { return c.Ctx }
func (c *Call) Flags() *pflag.FlagSet     { return c.Definition.Flags() }
func (c *Call) Flag(n string) *pflag.Flag { return c.Flags().Lookup(n) }
func (c *Call) Name() string              { return c.Definition.Name() }
func (c *Call) CommandPath() string       { return c.Definition.CommandPath() }
func (c *Call) OutOrStdout() io.Writer {
	if c.Output == nil {
		return io.Discard
	}
	return c.Output
}
func (c *Call) ErrOrStderr() io.Writer {
	if c.ErrorOutput == nil {
		return io.Discard
	}
	return c.ErrorOutput
}
func (c *Call) InOrStdin() io.Reader {
	if c.Input == nil {
		return strings.NewReader("")
	}
	return c.Input
}
