package operations

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
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

func TestDefiWalletBuildsDefiPayload(t *testing.T) {
	input := orgWalletInput{Name: "Monad Staking", Type: "DeFi", Address: "0xAbC", NetworkID: "monad", VaultAddress: " 0x0000000000000000000000000000000000001000 ", Protocol: "MonadStaking"}
	if err := normalizeAndValidateOrgWallet(&input); err != nil {
		t.Fatal(err)
	}
	wallet := buildOrgWalletPayload(input)
	if wallet["type"] != "defi" {
		t.Fatalf("type = %v", wallet["type"])
	}
	if _, present := wallet["accountBasedBlockchain"]; present {
		t.Fatalf("defi wallet must not carry accountBasedBlockchain: %#v", wallet)
	}
	defi, ok := wallet["defi"].(map[string]any)
	if !ok {
		t.Fatalf("missing defi payload: %#v", wallet)
	}
	want := map[string]any{"networkId": "monad", "walletAddress": "0xAbC", "vaultAddress": "0x0000000000000000000000000000000000001000", "isSyncEnabled": true, "protocol": "MonadStaking"}
	if !reflect.DeepEqual(defi, want) {
		t.Fatalf("defi = %#v, want %#v", defi, want)
	}
}

func TestDefiWalletRequiresVaultAddress(t *testing.T) {
	input := orgWalletInput{Name: "Monad Staking", Type: "defi", Address: "0xAbC", NetworkID: "monad"}
	err := normalizeAndValidateOrgWallet(&input)
	if err == nil || !strings.Contains(err.Error(), "vaultAddress is required") {
		t.Fatalf("expected vaultAddress error, got %v", err)
	}
	blockchain := orgWalletInput{Name: "Plain", Address: "0xAbC", NetworkID: "eth", VaultAddress: "0x1"}
	err = normalizeAndValidateOrgWallet(&blockchain)
	if err == nil || !strings.Contains(err.Error(), "require type defi") {
		t.Fatalf("expected type defi error, got %v", err)
	}
	unknown := orgWalletInput{Name: "Odd", Type: "vault", Address: "0xAbC", NetworkID: "eth"}
	if err := normalizeAndValidateOrgWallet(&unknown); err == nil {
		t.Fatal("unknown type should be rejected")
	}
}

func TestDefiWalletDuplicateDetectionIsPerVault(t *testing.T) {
	existing := []orgreports.Wallet{
		{ID: "w-plain", NetworkID: "monad", Address: "0xabc"},
		{ID: "w-pool-a", NetworkID: "monad", Address: "0xabc", VaultAddress: "0x00000000000000000000000000000000000000aa"},
	}
	staking := orgWalletInput{Name: "Staking", Type: "defi", Address: "0xABC", NetworkID: "monad", VaultAddress: "0x0000000000000000000000000000000000001000"}
	if err := normalizeAndValidateOrgWallet(&staking); err != nil {
		t.Fatal(err)
	}
	if match := findExistingOrgWallet(existing, staking); match != nil {
		t.Fatalf("a plain wallet or a different pool must not count as the same DeFi position: %#v", match)
	}
	poolA := orgWalletInput{Name: "Pool A", Type: "defi", Address: "0xABC", NetworkID: "monad", VaultAddress: "0x00000000000000000000000000000000000000AA"}
	if err := normalizeAndValidateOrgWallet(&poolA); err != nil {
		t.Fatal(err)
	}
	if match := findExistingOrgWallet(existing, poolA); match == nil || match.ID != "w-pool-a" {
		t.Fatalf("same wallet + vault should match the existing DeFi wallet, got %#v", match)
	}
	plain := orgWalletInput{Name: "Plain", Address: "0xABC", NetworkID: "monad"}
	if err := normalizeAndValidateOrgWallet(&plain); err != nil {
		t.Fatal(err)
	}
	if match := findExistingOrgWallet(existing, plain); match == nil || match.ID != "w-plain" {
		t.Fatalf("plain wallet should still match the plain existing wallet, got %#v", match)
	}
	if sameOrgWalletInput(staking, poolA) {
		t.Fatal("different vaults are different DeFi inputs")
	}
}
