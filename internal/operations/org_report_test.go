package operations

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/apierr"
	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func TestBalanceReportInputsUseProductContract(t *testing.T) {
	inputs, err := balanceReportInputs(balanceReportFlags{
		asOf:           "2026-06-30",
		groupBy:        "wallet",
		currency:       "usd",
		walletIDs:      []string{"wallet-1", "wallet-2"},
		subsidiaryIDs:  []string{"sub-1"},
		includeIgnored: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, input := range inputs {
		got[input.Key] = input.Value
	}
	if got["endDate"] != "2026-06-30" || got["groupBy"] != "1" || got["currency"] != "USD" {
		t.Fatalf("inputs = %#v", got)
	}
	if got["walletIds"] != `["wallet-1","wallet-2"]` || got["subsidiaryIds"] != `["sub-1"]` {
		t.Fatalf("array inputs = %#v", got)
	}
	if got["includeIgnored"] != "true" {
		t.Fatalf("includeIgnored = %q", got["includeIgnored"])
	}
}

func TestValidateBalanceReportFlags(t *testing.T) {
	valid := balanceReportFlags{asOf: "2026-06-30", groupBy: "asset", format: "csv", timeout: 1, reportAPI: "v1"}
	if err := validateBalanceReportFlags(valid); err != nil {
		t.Fatalf("valid flags: %v", err)
	}
	invalid := valid
	invalid.asOf = "2026-02-30"
	if err := validateBalanceReportFlags(invalid); err == nil {
		t.Fatal("expected invalid calendar date")
	}
}

type fallbackReportClient struct{}

func (fallbackReportClient) Download(context.Context, string, string) ([]byte, error) {
	return nil, &apierr.Error{Status: 404, Method: "GET", URL: "test"}
}

func (fallbackReportClient) Result(context.Context, string, string) (*orgreports.ReportData, error) {
	return &orgreports.ReportData{
		Columns: []string{"Wallet", "Asset", "Balance"},
		Rows:    []orgreports.ReportRow{{Cells: []string{"Treasury, Main", "BTC", "1"}}},
	}, nil
}

func TestDownloadBalanceCSVFallsBackToReportResult(t *testing.T) {
	data, fallback, err := downloadBalanceCSV(context.Background(), fallbackReportClient{}, "org-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !fallback {
		t.Fatal("expected fallback")
	}
	if string(data) != "Wallet,Asset,Balance\n\"Treasury, Main\",BTC,1\n" {
		t.Fatalf("CSV = %q", data)
	}
}

type failedReportClient struct{}

func (failedReportClient) Download(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("network down")

}
func (failedReportClient) Result(context.Context, string, string) (*orgreports.ReportData, error) {
	return nil, errors.New("must not be called")
}

func TestDownloadBalanceCSVDoesNotHideNon404(t *testing.T) {
	_, fallback, err := downloadBalanceCSV(context.Background(), failedReportClient{}, "org-1", "run-1")
	if err == nil || fallback || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("fallback=%v err=%v", fallback, err)
	}
}
