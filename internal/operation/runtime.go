package operation

import (
	"context"
	"github.com/bitwave-io/bitwave-cli/internal/scopedfs"
	"net/http"
)

// Options are explicit invocation dependencies. The SDK never consults CLI
// environment variables, credential files, or the process working directory.
type Options struct {
	WorkingDirectory                                             string
	OrganizationID                                               string
	Token, AgentToken                                            string
	TokenResolver                                                func(context.Context, string) (string, error)
	CoreBaseURL, GLBaseURL, BlockchainQueryBaseURL               string
	API2BaseURL, TransactionsBaseURL, AppBaseURL, ReportsBaseURL string
	HTTPClient                                                   *http.Client
	IdentityEmail                                                string
	// UnrestrictedFiles is for the interactive terminal adapter only. Hosted
	// adapters must leave it false and supply their managed workspace root.
	UnrestrictedFiles bool
	// AllowEndpointOverrides permits operation inputs to select network
	// destinations. Only the trusted terminal adapter should enable this;
	// hosted callers configure service endpoints via Options instead.
	AllowEndpointOverrides bool
}
type Runtime struct {
	Options Options
	Files   *scopedfs.Files
}
type runtimeKey struct{}

func WithRuntime(ctx context.Context, r *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, r)
}
func RuntimeFrom(ctx context.Context) *Runtime {
	r, _ := ctx.Value(runtimeKey{}).(*Runtime)
	if r == nil {
		return &Runtime{Files: &scopedfs.Files{}}
	}
	return r
}
func NewRuntime(o Options) (*Runtime, error) {
	files, e := scopedfs.New(o.WorkingDirectory, o.UnrestrictedFiles)
	if e != nil {
		return nil, e
	}
	o.WorkingDirectory = files.Directory
	return &Runtime{Options: o, Files: files}, nil
}
func (r *Runtime) Close() error { return r.Files.Close() }
