package orgreports

import (
	"context"
	"net/http"
	"net/url"
)

// DefiScheduleRequest is the body sync-coordinator-svc expects when creating
// one merged DeFi-position sync schedule for a (wallet, pool) pair. The
// walletId comes from the URL path; walletAddress + contractAddress identify
// the position and networkId selects the protocol resolver.
type DefiScheduleRequest struct {
	WalletAddress   string `json:"walletAddress"`
	ContractAddress string `json:"contractAddress"`
	NetworkID       string `json:"networkId"`
}

// DefiScheduleResponse reports the Temporal schedule sync-coordinator-svc
// created (or found) and the protocol it resolved for the position.
type DefiScheduleResponse struct {
	ScheduleID string `json:"scheduleId"`
	Protocol   string `json:"protocol"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

// DefiNetworkScheduleResponse reports the bulk discovery job that schedules
// every DeFi wallet under (org, network).
type DefiNetworkScheduleResponse struct {
	ScheduleID string `json:"scheduleId"`
	RunID      string `json:"runId,omitempty"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

// DefiWalletSchedulePath is the gateway path for scheduling one DeFi wallet.
func DefiWalletSchedulePath(orgID, walletID string) string {
	return "/v3/orgs/" + url.PathEscape(orgID) + "/wallets/" + url.PathEscape(walletID) + "/defi/wallet-schedule"
}

// DefiNetworkSchedulesPath is the gateway path for bulk-scheduling every DeFi
// wallet on one network.
func DefiNetworkSchedulesPath(orgID string) string {
	return "/v3/orgs/" + url.PathEscape(orgID) + "/defi/wallet-schedules"
}

// ScheduleDefiWallet creates (idempotently) the daily DeFi-position sync
// schedule for one organization wallet. The first run is triggered
// immediately by the backend.
func (c *Client) ScheduleDefiWallet(ctx context.Context, orgID, walletID string, request DefiScheduleRequest) (*DefiScheduleResponse, error) {
	var response DefiScheduleResponse
	if err := c.doJSON(ctx, http.MethodPost, DefiWalletSchedulePath(orgID, walletID), request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// ScheduleDefiWalletsForNetwork asks sync-coordinator-svc to discover every
// DeFi wallet under (org, network) and create one schedule per wallet.
// cadenceSeconds <= 0 leaves the backend default in place.
func (c *Client) ScheduleDefiWalletsForNetwork(ctx context.Context, orgID, networkID string, cadenceSeconds int) (*DefiNetworkScheduleResponse, error) {
	body := map[string]any{"networkId": networkID}
	if cadenceSeconds > 0 {
		body["cadenceSeconds"] = cadenceSeconds
	}
	var response DefiNetworkScheduleResponse
	if err := c.doJSON(ctx, http.MethodPost, DefiNetworkSchedulesPath(orgID), body, &response); err != nil {
		return nil, err
	}
	return &response, nil
}
