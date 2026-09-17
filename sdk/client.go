// Package sdk exposes request-scoped Bitwave business operations shared by the
// terminal CLI and hosted adapters such as MCP. It does not execute a terminal
// command, construct a Cobra command, start processes, or mutate process state.
package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/bitwave-io/bitwave-cli/internal/operations"
)

type Options = operation.Options
type Operation = operation.Descriptor
type Client struct{ options Options }
type Request struct {
	Operation string
	Arguments json.RawMessage
	// Input supplies request payloads for operations with an input="-" field.
	// It belongs to this invocation, never to the process's terminal stdin.
	Input io.Reader
	// Optional terminal-adapter streams. Hosted callers omit these and receive
	// bounded captured output. When supplied, the corresponding result string
	// is empty so the adapter does not print the same output twice.
	Output      io.Writer
	Diagnostics io.Writer
}
type Result struct {
	Operation   string          `json:"operation"`
	Output      string          `json:"output,omitempty"`
	Diagnostics string          `json:"diagnostics,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	Truncated   bool            `json:"truncated,omitempty"`
}

func NewClient(options Options) *Client { return &Client{options: options} }

// Operations describes exactly the shared business handlers available to both
// adapters. Schemas come from declared parameter types, never terminal help.
func Operations() ([]Operation, error) { return operation.Descriptors(operations.NewRoot()) }
func (c *Client) Invoke(ctx context.Context, request Request) (Result, error) {
	result := Result{Operation: request.Operation}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	root := operations.NewRoot()
	d := findOperation(root, request.Operation)
	if d == nil {
		return result, fmt.Errorf("unknown Bitwave operation %q", request.Operation)
	}
	args, err := operation.BindInput(d, request.Arguments)
	if err != nil {
		return result, err
	}
	if !c.options.AllowEndpointOverrides {
		for _, name := range []string{"base-url", "rpc-url"} {
			if flag := d.Flags().Lookup(name); flag != nil && flag.Changed {
				return result, fmt.Errorf("%s is managed by the SDK caller", name)
			}
		}
	}
	runtime, err := operation.NewRuntime(c.options)
	if err != nil {
		return result, err
	}
	defer runtime.Close()
	releaseWorkspace, err := acquireWorkspaceCall(ctx, d.Path(), runtime.Options.WorkingDirectory)
	if err != nil {
		return result, err
	}
	defer releaseWorkspace()
	ctx = operation.WithRuntime(ctx, runtime)
	stdout, stderr := &limitedBuffer{limit: maxOutput}, &limitedBuffer{limit: maxOutput}
	var outWriter, errWriter io.Writer = stdout, stderr
	if request.Output != nil {
		outWriter = io.MultiWriter(request.Output, stdout)
	}
	if request.Diagnostics != nil {
		errWriter = io.MultiWriter(request.Diagnostics, stderr)
	}
	outputSink, diagnosticsSink := &trackingWriter{writer: outWriter}, &trackingWriter{writer: errWriter}
	err = d.Invoke(operation.NewCall(ctx, d, request.Input, outputSink, diagnosticsSink), args)
	// Some presentation handlers deliberately ignore fmt.Fprint's return
	// value. A terminal sink failure still needs to reach the adapter.
	err = errors.Join(err, outputSink.err, diagnosticsSink.err)
	result.Output, result.Diagnostics = stdout.String(), stderr.String()
	if request.Output != nil {
		result.Output = ""
	}
	if request.Diagnostics != nil {
		result.Diagnostics = ""
	}
	result.Truncated = stdout.truncated || stderr.truncated
	if b := bytes.TrimSpace(stdout.buffer.Bytes()); !stdout.truncated && json.Valid(b) {
		result.Data = append(json.RawMessage(nil), b...)
	}
	return result, err
}
func findOperation(root *operation.Definition, name string) *operation.Definition {
	for _, d := range root.Commands() {
		if operation.ToolName(d.Path()) == name && d.Runnable() {
			return d
		}
		if found := findOperation(d, name); found != nil {
			return found
		}
	}
	return nil
}

const maxOutput = 1 << 20

type trackingWriter struct {
	writer io.Writer
	err    error
}

func (w *trackingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if err != nil && w.err == nil {
		w.err = err
	}
	return n, err
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.limit - b.buffer.Len()
	if left <= 0 {
		b.truncated = b.truncated || n > 0
		return n, nil
	}
	if n > left {
		b.truncated = true
		p = p[:left]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}
func (b *limitedBuffer) String() string { return b.buffer.String() }
