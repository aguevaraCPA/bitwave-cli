package operations

import (
	"context"

	"github.com/bitwave-io/bitwave-cli/internal/bitwave/shares"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/workspaces"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/workspaceshare"
	"github.com/bitwave-io/bitwave-cli/internal/operation"
)

func newWorkspaceClient(ctx context.Context, orgID string) *workspaces.Client {
	c := workspaces.New(resolveGLBaseURL(ctx), orgID, makeOrgTokenResolver(ctx, orgID))
	c.Context = ctx
	if client := operation.RuntimeFrom(ctx).Options.HTTPClient; client != nil {
		c.HTTPClient = client
	}
	return c
}

func newCloudStore(ctx context.Context, orgID, workspaceID string) *store.Cloud {
	c := store.NewCloud(resolveGLBaseURL(ctx), orgID, workspaceID, makeOrgTokenResolver(ctx, orgID))
	c.SetRequestContext(ctx, operation.RuntimeFrom(ctx).Options.HTTPClient)
	return c
}

func newShareClient(ctx context.Context, orgID, workspaceID string) *shares.Client {
	c := shares.New(resolveGLBaseURL(ctx), workspaceID, makeOrgTokenResolver(ctx, orgID))
	if client := operation.RuntimeFrom(ctx).Options.HTTPClient; client != nil {
		c.HTTPClient = client
	}
	return c
}

func newWorkspaceShareClient(ctx context.Context, orgID string) *workspaceshare.Client {
	c := workspaceshare.New(resolveGLBaseURL(ctx), makeOrgTokenResolver(ctx, orgID))
	c.Files = operation.RuntimeFrom(ctx).Files
	if client := operation.RuntimeFrom(ctx).Options.HTTPClient; client != nil {
		c.HTTPClient = client
	}
	return c
}
