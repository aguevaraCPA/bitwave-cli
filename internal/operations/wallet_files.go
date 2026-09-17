package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitwave-io/bitwave-cli/internal/operation"
	walletsync "github.com/bitwave-io/bitwave-wallet-sdk/sync"
	"github.com/bitwave-io/bitwave-wallet-sdk/wallet"
)

// The wallet SDK's path-only persistence helpers use ambient OS access. These
// small adapters preserve its on-disk format while using the request's file
// capability for every read and write (including referenced keystore paths).
func saveWallet(ctx context.Context, dir string, w *wallet.Wallet) (string, error) {
	if err := validateWalletFilePart(w.Id); err != nil {
		return "", err
	}
	files := operation.RuntimeFrom(ctx).Files
	if err := files.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, wallet.FilePrefix+w.Id+".json")
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return "", err
	}
	if err := files.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func loadWallet(ctx context.Context, path string) (*wallet.Wallet, error) {
	data, err := operation.RuntimeFrom(ctx).Files.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var w wallet.Wallet
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	if err := validateWalletFilePart(w.Id); err != nil {
		return nil, err
	}
	return &w, nil
}

func resolveWallet(ctx context.Context, dir, ref string) (*wallet.Wallet, string, error) {
	if ref == "" {
		return nil, "", fmt.Errorf("wallet reference is empty")
	}
	if strings.ContainsAny(ref, "/\\") || strings.HasSuffix(ref, ".json") {
		w, err := loadWallet(ctx, ref)
		return w, ref, err
	}
	entries, err := operation.RuntimeFrom(ctx).Files.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}
	var matched *wallet.Wallet
	var matchedPath string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, wallet.FilePrefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dir, name)
		w, err := loadWallet(ctx, path)
		if err != nil {
			continue
		}
		if w.Id != ref && w.Name != ref {
			continue
		}
		if matched != nil {
			return nil, "", fmt.Errorf("ambiguous wallet ref %q: %s, %s", ref, matched.Id, w.Id)
		}
		matched, matchedPath = w, path
	}
	if matched == nil {
		return nil, "", fmt.Errorf("no wallet matches %q in %s", ref, dir)
	}
	return matched, matchedPath, nil
}

func validateWalletFilePart(value string) error {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00") {
		return fmt.Errorf("invalid wallet file identifier %q", value)
	}
	return nil
}

func syncStatePath(dir, walletID, network string) (string, error) {
	if err := validateWalletFilePart(walletID); err != nil {
		return "", err
	}
	if err := validateWalletFilePart(network); err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%s%s.sync-%s.json", wallet.FilePrefix, walletID, network)), nil
}

func loadWalletSyncState(ctx context.Context, dir, walletID, network string) (walletsync.SyncState, bool, error) {
	path, err := syncStatePath(dir, walletID, network)
	if err != nil {
		return walletsync.SyncState{}, false, err
	}
	data, err := operation.RuntimeFrom(ctx).Files.ReadFile(path)
	if os.IsNotExist(err) {
		return walletsync.SyncState{WalletId: walletID, Network: network}, false, nil
	}
	if err != nil {
		return walletsync.SyncState{}, false, err
	}
	var s walletsync.SyncState
	if err := json.Unmarshal(data, &s); err != nil {
		return s, false, err
	}
	if s.WalletId != walletID || s.Network != network {
		return s, false, fmt.Errorf("sync watermark identity does not match requested wallet/network")
	}
	return s, true, nil
}

func saveWalletSyncState(ctx context.Context, dir string, s walletsync.SyncState) error {
	path, err := syncStatePath(dir, s.WalletId, s.Network)
	if err != nil {
		return err
	}
	files := operation.RuntimeFrom(ctx).Files
	if err := files.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := files.CreateTemp(dir, ".wallet-sync-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer files.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return files.Rename(tmp, path)
}
