package operations

import (
	"strings"
	"testing"
)

func TestNormalizeTransactionID(t *testing.T) {
	solanaSignature := "4MkxKMkXFPxHRzvQtBsdn3Dn37UaVQzpcFg5SyD1wSKBNTXeAEFCjqpTBPqept9KFx9Tycto9wspspfY9MxMnBm5"
	tests := []struct {
		name    string
		value   string
		network string
		want    string
		wantErr string
	}{
		{name: "qualified", value: "SOL." + solanaSignature, want: "SOL." + solanaSignature},
		{name: "detect solana", value: solanaSignature, want: "SOL." + solanaSignature},
		{name: "explicit ethereum alias", value: "0xabc", network: "ethereum", want: "ETH.0xabc"},
		{name: "explicit bnb alias", value: "0xabc", network: "bnb", want: "BSC.0xabc"},
		{name: "ambiguous raw hash", value: "0xabc", wantErr: "pass --network"},
		{name: "empty", value: " ", wantErr: "transaction ID is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeTransactionID(test.value, test.network)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("normalizeTransactionID() error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeTransactionID() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("normalizeTransactionID() = %q, want %q", got, test.want)
			}
		})
	}
}
