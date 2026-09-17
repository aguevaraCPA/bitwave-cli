package operations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
	"github.com/bitwave-io/bitwave-cli/internal/blockchainquery"
	walletsync "github.com/bitwave-io/bitwave-wallet-sdk/sync"
	"github.com/bitwave-io/bitwave-wallet-sdk/wallet"
)

// resolveSyncWallet looks up a wallet by id/name/address/keystore-path. Tries
// keystore files first (locally-custodied wallets), then falls back to scanning
// the workspace's tagged accounts so watch-only wallets resolve too.
func resolveSyncWallet(ctx context.Context, s store.Store, dir, ref string) (*wallet.Wallet, error) {
	if ref == "" {
		return nil, errors.New("--wallet is required")
	}
	if w, _, err := resolveWallet(ctx, dir, ref); err == nil {
		return w, nil
	}
	p, err := s.Project(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range p.Accounts {
		if a.Tags["wallet"] == "" {
			continue
		}
		parts := strings.Split(a.Name, ":")
		acctName := ""
		if len(parts) > 0 {
			acctName = parts[len(parts)-1]
		}
		addr := a.Tags["address"]
		if a.Tags["wallet"] == ref || acctName == ref || strings.EqualFold(addr, ref) {
			return &wallet.Wallet{
				Id:      a.Tags["wallet"],
				Name:    acctName,
				Address: addr,
			}, nil
		}
	}
	return nil, fmt.Errorf("no wallet matches %q (looked in keystores and account tags)", ref)
}

// syncFlags collects the user-facing controls. The defaults are tuned for
// running an end-of-day sync against the Bitwave blockchain query API.
type syncFlags struct {
	walletRef     string
	networkName   string
	fromUnix      string // RFC3339 or YYYY-MM-DD; default: last watermark or "30 days ago"
	limit         int
	baseURL       string
	journalFlag   string
	dryRun        bool
	confirmations int
	avgBlockSecs  int
	pageMax       int
}

func newWalletsSyncCmd() *operation.Definition {
	var f syncFlags
	cmd := &operation.Definition{
		Use:   "sync",
		Short: "Pull on-chain history for a wallet via the Bitwave blockchain query API and append entries",
		Long: `Reads the wallet's transactions from the Bitwave blockchain query API, transforms each
into a ledger entry, and appends new ones to the workspace journal.

Resumes from a per-(wallet, network) watermark stored alongside the keystore.
Entries from blocks within the confirmation window are written with pending (!)
status; older blocks are marked cleared (*).`,
		RunE: func(cmd *operation.Call, _ []string) error {
			return runWalletsSync(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	cmd.Flags().StringVar(&f.walletRef, "wallet", "", "Wallet id, name, or keystore path (required)")
	cmd.Flags().StringVar(&f.networkName, "network", "", "ethereum or base (required)")
	cmd.Flags().StringVar(&f.fromUnix, "from", "", "Start time (RFC3339 or YYYY-MM-DD); defaults to last watermark, or full history on the first sync")
	cmd.Flags().IntVar(&f.limit, "limit", 200, "Server page size")
	cmd.Flags().IntVar(&f.pageMax, "max-pages", 100, "Safety cap on pagination loops")
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "Override the Bitwave blockchain query API base URL")
	cmd.Flags().StringVar(&f.journalFlag, "journal", "", "Journal id (defaults to workspace default)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Print what would be written without modifying the journal")
	cmd.Flags().IntVar(&f.confirmations, "confirmations", 12, "Blocks of confirmation required before an entry is marked cleared")
	cmd.Flags().IntVar(&f.avgBlockSecs, "avg-block-secs", 12, "Average block time (used to convert confirmations to seconds)")
	return cmd
}

// blockchainQueryTokenResolver returns a resolver, or nil when the base URL
// points at localhost — local dev instances don't speak our auth flow and
// failing the PKCE refresh just to surface a 401 is noise.
func blockchainQueryTokenResolver(ctx context.Context, baseURL string) func() (string, error) {
	if isLocalhostBaseURL(baseURL) {
		return nil
	}
	return makeTokenResolver(ctx)
}

func isLocalhostBaseURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// defaultBlockchainQueryBaseURL is the fallback when neither --base-url nor
// BITWAVE_BASE_URL_BLOCKCHAIN_QUERY is set. Overridable at build time, e.g.:
//
//	go build -ldflags "-X github.com/bitwave-io/bitwave-cli/internal/bitwave/cmd.defaultBlockchainQueryBaseURL=http://localhost:8080"
//
// `make cli-local` builds a bitwave that points at localhost by default.
var defaultBlockchainQueryBaseURL = "https://api4.bitwave.io"

func resolveBlockchainQueryBaseURL(ctx context.Context, flag string) string {
	if flag != "" {
		return flag
	}
	if v := operation.RuntimeFrom(ctx).Options.BlockchainQueryBaseURL; v != "" {
		return v
	}
	return defaultBlockchainQueryBaseURL
}

func runWalletsSync(ctx context.Context, out io.Writer, f syncFlags) error {
	if f.walletRef == "" || f.networkName == "" {
		return errors.New("--wallet and --network are required")
	}
	s, cfg, dir, err := resolveStore(ctx)
	if err != nil {
		return err
	}
	journalId, err := resolveJournal(ctx, s, cfg, f.journalFlag)
	if err != nil {
		return err
	}
	w, err := resolveSyncWallet(ctx, s, dir, f.walletRef)
	if err != nil {
		return err
	}
	net, err := wallet.LookupNetwork(f.networkName, "")
	if err != nil {
		return err
	}
	walletAccount := fmt.Sprintf("Assets:Crypto:%s:%s", net.Name, w.Name)

	state, _, err := loadWalletSyncState(ctx, dir, w.Id, net.Name)
	if err != nil {
		return err
	}
	startMs, err := resolveSyncStart(f.fromUnix, state.LastBlockTimeUnixMs)
	if err != nil {
		return err
	}

	existingHashes, err := loadExistingTxnHashes(ctx, s, w.Id, net.Name)
	if err != nil {
		return fmt.Errorf("scan existing entries: %w", err)
	}

	baseURL := resolveBlockchainQueryBaseURL(ctx, f.baseURL)
	client := blockchainquery.New(baseURL, blockchainQueryTokenResolver(ctx, baseURL))
	client.Context = ctx
	if httpClient := operation.RuntimeFrom(ctx).Options.HTTPClient; httpClient != nil {
		client.HTTPClient = httpClient
	}
	confirmCutoffMs := time.Now().UnixMilli() - int64(f.confirmations*f.avgBlockSecs)*1000

	var (
		cursor    string
		appended  int
		skipped   int
		pages     int
		newestMs  = state.LastBlockTimeUnixMs
		newestTxn = state.LastTxnHash
	)
	for pages < f.pageMax {
		pages++
		resp, err := client.ScanAddress(blockchainquery.ScanRequest{
			Chain:           net.Name,
			Address:         w.Address,
			StartTimeUnixMs: startMs,
			Cursor:          cursor,
			Limit:           f.limit,
			Order:           "asc",
			FetchPayloads:   true,
		})
		if err != nil {
			return fmt.Errorf("scan page %d: %w", pages, err)
		}
		for _, r := range resp.Results {
			hash := strings.ToLower(r.TxnHash)
			if hash != "" && existingHashes[hash] {
				skipped++
				continue
			}
			confirmed := r.BlockTimeUnixMs > 0 && r.BlockTimeUnixMs <= confirmCutoffMs
			entry, err := walletsync.TransformToEntry(walletsync.SyncTransformInput{
				Wallet:          w,
				Network:         net,
				WalletAccount:   walletAccount,
				Payload:         r.Payload,
				BlockTimeUnixMs: r.BlockTimeUnixMs,
				BlockNumber:     r.BlockNumber,
				TxnHash:         r.TxnHash,
				Confirmed:       confirmed,
			})
			if err != nil {
				return fmt.Errorf("transform %s: %w", r.TxnHash, err)
			}
			if entry == nil {
				continue
			}
			if f.dryRun {
				_, _ = fmt.Fprintf(out, "DRY: would append %s (%s) — %d postings\n", r.TxnHash, statusFlag(entry.Status), len(entry.Postings))
			} else {
				id, err := s.AddEntry(ctx, journalId, *entry)
				if err != nil {
					return fmt.Errorf("write entry for %s: %w", r.TxnHash, err)
				}
				_, _ = fmt.Fprintf(out, "appended %s (%s) — %s\n", r.TxnHash, statusFlag(entry.Status), id)
			}
			appended++
			if hash != "" {
				existingHashes[hash] = true
			}
			if r.BlockTimeUnixMs > newestMs {
				newestMs = r.BlockTimeUnixMs
				newestTxn = r.TxnHash
			}
		}
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	if pages >= f.pageMax {
		_, _ = fmt.Fprintf(out, "WARNING: hit --max-pages=%d; rerun to continue\n", f.pageMax)
	}

	_, _ = fmt.Fprintf(out, "sync complete: %d appended, %d skipped (already present)\n", appended, skipped)
	if f.dryRun {
		_, _ = fmt.Fprintln(out, "(dry-run: watermark NOT updated)")
		return nil
	}
	if newestMs > state.LastBlockTimeUnixMs {
		next := walletsync.SyncState{
			WalletId:            w.Id,
			Network:             net.Name,
			LastBlockTimeUnixMs: newestMs,
			LastTxnHash:         newestTxn,
		}
		if err := saveWalletSyncState(ctx, dir, next); err != nil {
			return fmt.Errorf("save watermark: %w", err)
		}
		_, _ = fmt.Fprintf(out, "watermark advanced to %s (txn %s)\n", time.UnixMilli(newestMs).UTC().Format(time.RFC3339), newestTxn)
	}
	return nil
}

func statusFlag(s model.Status) string {
	switch s {
	case model.StatusPending:
		return "!"
	case model.StatusCleared:
		return "*"
	}
	return " "
}

// resolveSyncStart picks the start watermark: explicit flag > saved watermark
// > 0 (full history). The first sync intentionally pulls everything; the
// watermark from that run bounds every subsequent sync.
func resolveSyncStart(flag string, savedMs int64) (int64, error) {
	if flag != "" {
		// Try RFC3339 first, then YYYY-MM-DD.
		if t, err := time.Parse(time.RFC3339, flag); err == nil {
			return t.UnixMilli(), nil
		}
		if t, err := time.Parse("2006-01-02", flag); err == nil {
			return t.UTC().UnixMilli(), nil
		}
		return 0, fmt.Errorf("--from %q: want RFC3339 or YYYY-MM-DD", flag)
	}
	return savedMs, nil
}

// loadExistingTxnHashes scans the workspace for entries already tagged with
// this wallet + network and returns the set of lowercased txn hashes.
func loadExistingTxnHashes(ctx context.Context, s store.Store, walletId, network string) (map[string]bool, error) {
	p, err := s.Project(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range p.Entries {
		tags := parseNoteTags(e.Note)
		if tags["wallet"] != walletId || tags["network"] != network {
			continue
		}
		if h := strings.ToLower(tags["txn"]); h != "" {
			out[h] = true
		}
	}
	return out, nil
}

// parseNoteTags pulls "key:value" tokens out of an entry note. Free-form
// segments are ignored.
func parseNoteTags(note string) map[string]string {
	tags := map[string]string{}
	for _, tok := range strings.Fields(note) {
		i := strings.IndexByte(tok, ':')
		if i <= 0 || i == len(tok)-1 {
			continue
		}
		tags[tok[:i]] = tok[i+1:]
	}
	return tags
}
