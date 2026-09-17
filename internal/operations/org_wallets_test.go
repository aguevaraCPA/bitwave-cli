package operations

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeAndBuildOrganizationWallet(t *testing.T) {
	input := orgWalletInput{
		Name:         " Treasury ",
		Address:      "0xAbC",
		NetworkID:    "Ethereum",
		SubsidiaryID: "sub-1",
	}
	if err := normalizeAndValidateOrgWallet(&input); err != nil {
		t.Fatal(err)
	}
	if input.NetworkID != "eth" || input.Address != "0xAbC" {
		t.Fatalf("normalization lost canonical network or address case: %#v", input)
	}
	wallet := buildOrgWalletPayload(input)
	if wallet["type"] != "accountBasedBlockchain" || wallet["subsidiaryId"] != "sub-1" {
		t.Fatalf("unexpected wallet: %#v", wallet)
	}
	blockchain, ok := wallet["accountBasedBlockchain"].(map[string]any)
	if !ok || blockchain["networkId"] != "eth" || blockchain["address"] != "0xAbC" {
		t.Fatalf("unexpected blockchain payload: %#v", wallet["accountBasedBlockchain"])
	}
}

func TestCantonWalletCarriesSyncerVersion(t *testing.T) {
	input := orgWalletInput{Name: "Canton", Address: "party::id", NetworkID: "canton"}
	if err := normalizeAndValidateOrgWallet(&input); err != nil {
		t.Fatal(err)
	}
	wallet := buildOrgWalletPayload(input)
	if _, ok := wallet["structuredSyncerVersionConfig"]; !ok {
		t.Fatalf("Canton wallet missing structured syncer version: %#v", wallet)
	}
}

func TestHDWalletUsesWatchShape(t *testing.T) {
	input := orgWalletInput{Name: "Bitcoin xpub", Address: "xpub123", NetworkID: "btc", AddressType: "hd"}
	if err := normalizeAndValidateOrgWallet(&input); err != nil {
		t.Fatal(err)
	}
	wallet := buildOrgWalletPayload(input)
	want := map[string]any{"name": "Bitcoin xpub", "type": "watch", "watch": map[string]any{"coin": "BTC", "type": "hd", "derivationKey": "xpub123"}}
	if !reflect.DeepEqual(wallet, want) {
		t.Fatalf("wallet = %#v, want %#v", wallet, want)
	}
}

func TestUnknownNetworkIsForwardCompatible(t *testing.T) {
	input := orgWalletInput{Name: "Future", Address: "future-address", NetworkID: "future-chain"}
	if err := normalizeAndValidateOrgWallet(&input); err != nil {
		t.Fatalf("unknown canonical network should be delegated to the API: %v", err)
	}
	if got := buildOrgWalletPayload(input)["accountBasedBlockchain"].(map[string]any)["networkId"]; got != "future-chain" {
		t.Fatalf("networkId = %v", got)
	}
}

func TestOrganizationWalletSyncGuidance(t *testing.T) {
	guidance := organizationWalletSyncGuidance()
	if guidance["expectedDuration"] != "15 minutes to 24 hours" {
		t.Fatalf("expectedDuration = %v", guidance["expectedDuration"])
	}
	if guidance["checkCommand"] != "bitwave transaction search --wallet WALLET_NAME --limit 1 --json" {
		t.Fatalf("checkCommand = %v", guidance["checkCommand"])
	}
}

func TestOrganizationWalletAddHelpIncludesSyncWindow(t *testing.T) {
	cmd := testDefinition(newOrgWalletsAddCmd())
	if !strings.Contains(cmd.Long, "15 minutes") || !strings.Contains(cmd.Long, "24 hours") {
		t.Fatalf("add help does not explain sync timing: %q", cmd.Long)
	}
}
