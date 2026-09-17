package operations

import (
	"errors"
	"fmt"
	"strings"
)

const solanaBase58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// normalizeTransactionID accepts the network-qualified identifiers used by
// Bitwave and the raw transaction identifiers commonly returned by explorers.
// Solana signatures are unambiguous enough to detect. Other raw identifiers
// require an explicit network so the CLI never silently targets the wrong
// transaction.
func normalizeTransactionID(value, network string) (string, error) {
	transactionID := strings.TrimSpace(value)
	if transactionID == "" {
		return "", errors.New("transaction ID is required")
	}
	if strings.Contains(transactionID, ".") {
		return transactionID, nil
	}

	prefix := normalizeTransactionNetwork(network)
	if prefix != "" {
		return prefix + "." + transactionID, nil
	}
	if isSolanaSignature(transactionID) {
		return "SOL." + transactionID, nil
	}
	return "", fmt.Errorf("raw transaction ID %q has no Bitwave network prefix; pass --network (for example --network ETH or --network BSC)", transactionID)
}

func normalizeTransactionNetwork(value string) string {
	network := strings.ToUpper(strings.TrimSpace(value))
	switch network {
	case "SOLANA":
		return "SOL"
	case "ETHEREUM":
		return "ETH"
	case "BINANCE", "BINANCE-SMART-CHAIN", "BNB", "BNB-SMART-CHAIN":
		return "BSC"
	case "MATIC":
		return "POLYGON"
	case "APTOS":
		return "APT"
	default:
		return network
	}
}

func isSolanaSignature(value string) bool {
	// Ed25519 signatures are 64 bytes and encode to 87 or 88 base58 chars.
	if len(value) != 87 && len(value) != 88 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune(solanaBase58Alphabet, character) {
			return false
		}
	}
	return true
}
