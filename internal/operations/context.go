package operations

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/bitwave-io/bitwave-cli/internal/orgctx"
	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func resolveCoreBaseURL(ctx context.Context) string {
	if v := operation.RuntimeFrom(ctx).Options.CoreBaseURL; v != "" {
		return v
	}
	return "https://api.bitwave.io"
}
func resolveGLBaseURL(ctx context.Context) string {
	if v := operation.RuntimeFrom(ctx).Options.GLBaseURL; v != "" {
		return v
	}
	return "https://api.bitwave.io"
}
func resolveIdentityEmail(ctx context.Context) string {
	return operation.RuntimeFrom(ctx).Options.IdentityEmail
}
func makeTokenResolver(ctx context.Context) func() (string, error) {
	return makeOrgTokenResolver(ctx, operation.RuntimeFrom(ctx).Options.OrganizationID)
}
func makeOrgTokenResolver(ctx context.Context, org string) func() (string, error) {
	var once sync.Once
	var token string
	var err error
	return func() (string, error) {
		once.Do(func() {
			o := operation.RuntimeFrom(ctx).Options
			if o.OrganizationID != "" && org != "" && o.OrganizationID != org {
				err = errors.New("organization conflicts with SDK request scope")
				return
			}
			if o.TokenResolver != nil {
				token, err = o.TokenResolver(ctx, org)
			} else if o.AgentToken != "" {
				token = o.AgentToken
			} else {
				token = o.Token
			}
			if err == nil && strings.TrimSpace(token) == "" {
				err = errors.New("authenticated SDK operations require an explicit token or token resolver")
			}
		})
		return token, err
	}
}
func requireActiveOrg(ctx context.Context) (*orgctx.Active, error) {
	org := operation.RuntimeFrom(ctx).Options.OrganizationID
	if org == "" {
		return nil, errors.New("this SDK operation requires an explicit organization")
	}
	return &orgctx.Active{OrgID: org}, nil
}
func newReportsClient(ctx context.Context, org string) *orgreports.Client {
	c := orgreports.New(resolveCoreBaseURL(ctx), makeOrgTokenResolver(ctx, org))
	o := operation.RuntimeFrom(ctx).Options
	if o.HTTPClient != nil {
		c.HTTPClient = o.HTTPClient
	}
	if o.API2BaseURL != "" {
		c.API2URL = strings.TrimRight(o.API2BaseURL, "/")
	}
	if o.TransactionsBaseURL != "" {
		c.TransactionsURL = strings.TrimRight(o.TransactionsBaseURL, "/")
	}
	if o.AppBaseURL != "" {
		c.RulesMutationURL = strings.TrimRight(o.AppBaseURL, "/") + "/graphql"
	}
	if o.ReportsBaseURL != "" {
		c.RulesQueryURL = strings.TrimRight(o.ReportsBaseURL, "/") + "/graphql-reports"
	}
	return c
}
