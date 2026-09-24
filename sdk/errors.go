package sdk

import "github.com/bitwave-io/bitwave-cli/internal/apierr"

// APIError is a non-success HTTP API response. Both Invoke's error and the
// CLI adapter's CommandResult.Err preserve it through errors.As. Status is
// structured protocol data, not parsed out of terminal presentation text.
//
// URL, Detail and Error() remain untrusted and may contain sensitive upstream
// information. Hosted callers must apply their own disclosure policy; this
// alias does not designate arbitrary server messages as safe for publication.
type APIError = apierr.Error
