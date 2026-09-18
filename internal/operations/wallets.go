package operations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/shopspring/decimal"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-wallet-sdk/wallet"
)

// broadcasterFactory is injected by tests; production code uses the real
// ethclient dialer.
var broadcasterFactory = func(rpcURL string) (wallet.Broadcaster, error) {
	return wallet.NewEthBroadcaster(rpcURL)
}

const walletKeystoreWarning = `WARNING: the keystore file above holds your unencrypted private key.
Do NOT commit it to source control. Add wallet-*.json to .gitignore.`

func newWalletsCmd() *operation.Definition {
	cmd := &operation.Definition{
		Use:   "wallets",
		Short: "Manage workspace EVM wallets (eth, base)",
	}
	cmd.AddCommand(newWalletsNewCmd())
	cmd.AddCommand(newWalletsAddCmd())
	cmd.AddCommand(newWalletsListCmd())
	cmd.AddCommand(newWalletsShowCmd())
	cmd.AddCommand(newWalletsSendCmd())
	cmd.AddCommand(newWalletsSyncCmd())
	return cmd
}

// --- new ---

func newWalletsNewCmd() *operation.Definition {
	var name, networksFlag string
	cmd := &operation.Definition{
		Use:   "new",
		Short: "Generate a new EVM keypair (locally-custodied) and declare it in accounts.ledger",
		RunE: func(cmd *operation.Call, _ []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			s, _, dir, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			nets, err := parseNetworksFlag(networksFlag)
			if err != nil {
				return err
			}
			w, err := wallet.Generate(name)
			if err != nil {
				return err
			}
			w.SupportedNetworks = nets
			path, err := saveWallet(cmd.Context(), dir, w)
			if err != nil {
				return err
			}
			declared := make([]string, 0, len(nets))
			for _, net := range nets {
				acctName := fmt.Sprintf("Assets:Crypto:%s:%s", net, name)
				a := model.Account{
					Name: acctName,
					Type: model.AccountAsset,
					Tags: map[string]string{
						"wallet":  w.Id,
						"address": w.Address,
						"network": net,
					},
				}
				if err := s.AddAccount(cmd.Context(), a); err != nil {
					return fmt.Errorf("declare account %s: %w", acctName, err)
				}
				declared = append(declared, acctName)
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Created wallet %s (%s)\n", w.Id, name)
			_, _ = fmt.Fprintf(out, "  address: %s\n", w.Address)
			_, _ = fmt.Fprintf(out, "  keystore: %s\n", path)
			for _, a := range declared {
				_, _ = fmt.Fprintf(out, "  account: %s\n", a)
			}
			_, _ = fmt.Fprintln(out)
			_, _ = fmt.Fprintln(out, walletKeystoreWarning)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Wallet name (required)")
	cmd.Flags().StringVar(&networksFlag, "networks", "ethereum,base", "Comma-separated networks to declare accounts for")
	return cmd
}

// --- list ---

func newWalletsListCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "list",
		Short: "List wallet declarations grouped by wallet id",
		RunE: func(cmd *operation.Call, _ []string) error {
			s, _, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			p, err := s.Project(cmd.Context())
			if err != nil {
				return err
			}
			groups := groupWalletAccounts(p.Accounts)
			ids := make([]string, 0, len(groups))
			for id := range groups {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			out := cmd.OutOrStdout()
			for _, id := range ids {
				accs := groups[id]
				sort.Slice(accs, func(i, j int) bool { return accs[i].Name < accs[j].Name })
				addr := accs[0].Tags["address"]
				_, _ = fmt.Fprintf(out, "%s  %s\n", id, addr)
				for _, a := range accs {
					_, _ = fmt.Fprintf(out, "  %s  (%s)\n", a.Name, a.Tags["network"])
				}
			}
			return nil
		},
	}
}

// --- show ---

func newWalletsShowCmd() *operation.Definition {
	return &operation.Definition{
		Use:   "show <name-or-id>",
		Short: "Show a wallet's declared accounts and keystore path",
		Args:  operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			s, _, dir, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}
			w, path, err := resolveWallet(cmd.Context(), dir, args[0])
			if err != nil {
				return err
			}
			p, err := s.Project(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "%s  %s (%s)\n", w.Id, w.Name, w.Address)
			_, _ = fmt.Fprintf(out, "  keystore: %s\n", path)
			for _, a := range p.Accounts {
				if a.Tags["wallet"] != w.Id {
					continue
				}
				_, _ = fmt.Fprintf(out, "  %s  (%s)\n", a.Name, a.Tags["network"])
			}
			return nil
		},
	}
}

// --- send ---

type sendFlags struct {
	walletRef       string
	networkName     string
	to              string
	amountEth       string
	amountWei       string
	category        string
	contact         string
	memo            string
	rpcURL          string
	maxFeeGwei      string
	maxPriorityGwei string
	gasLimit        uint64
	nonce           int64
	dryRun          bool
	journalFlag     string
}

func newWalletsSendCmd() *operation.Definition {
	var f sendFlags
	cmd := &operation.Definition{
		Use:   "send",
		Short: "Sign + broadcast a value transfer and append the journal entry",
		RunE: func(cmd *operation.Call, _ []string) error {
			return runWalletsSend(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	cmd.Flags().StringVar(&f.walletRef, "wallet", "", "Wallet id, name, or keystore path (required)")
	cmd.Flags().StringVar(&f.networkName, "network", "", "ethereum or base (required)")
	cmd.Flags().StringVar(&f.to, "to", "", "Destination address (required)")
	cmd.Flags().StringVar(&f.amountEth, "amount-eth", "", "Amount in ETH (decimal)")
	cmd.Flags().StringVar(&f.amountWei, "amount-wei", "", "Amount in wei (integer)")
	cmd.Flags().StringVar(&f.category, "category", "Expenses:Uncategorized", "Contra account (Expenses:* / Assets:* / etc.)")
	cmd.Flags().StringVar(&f.contact, "contact", "", "Payee for the journal entry")
	cmd.Flags().StringVar(&f.memo, "memo", "", "Free-form memo on the entry")
	cmd.Flags().StringVar(&f.rpcURL, "rpc-url", "", "Override the RPC URL for the network")
	if err := cmd.MarkEndpointParameter("rpc-url"); err != nil {
		panic(err) // Invalid static operation definition, never caller input.
	}
	cmd.Flags().StringVar(&f.maxFeeGwei, "max-fee-gwei", "", "EIP-1559 max fee per gas (gwei)")
	cmd.Flags().StringVar(&f.maxPriorityGwei, "max-priority-fee-gwei", "", "EIP-1559 priority fee (gwei)")
	cmd.Flags().Uint64Var(&f.gasLimit, "gas-limit", 0, "Gas limit (defaults to 21000 for value transfers)")
	cmd.Flags().Int64Var(&f.nonce, "nonce", -1, "Override nonce (skips RPC lookup)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Sign but do not broadcast or write the journal")
	cmd.Flags().StringVar(&f.journalFlag, "journal", "", "Journal id (defaults to workspace default)")
	return cmd
}

func runWalletsSend(ctx context.Context, out io.Writer, f sendFlags) error {
	if f.walletRef == "" || f.networkName == "" || f.to == "" {
		return errors.New("--wallet, --network, and --to are required")
	}
	s, cfg, dir, err := resolveStore(ctx)
	if err != nil {
		return err
	}
	if err := validateCategory(f.category); err != nil {
		return err
	}
	journalId, err := resolveJournal(ctx, s, cfg, f.journalFlag)
	if err != nil {
		return err
	}
	w, _, err := resolveWallet(ctx, dir, f.walletRef)
	if err != nil {
		return err
	}
	net, err := wallet.LookupNetwork(f.networkName, f.rpcURL)
	if err != nil {
		return err
	}
	walletAccount := fmt.Sprintf("Assets:Crypto:%s:%s", net.Name, w.Name)

	amountWei, err := resolveAmountWei(f, net.Decimals)
	if err != nil {
		return err
	}
	to, err := wallet.ParseAddress(f.to)
	if err != nil {
		return err
	}

	req := wallet.SendRequest{
		Wallet:           w,
		Network:          net,
		To:               to,
		AmountWei:        amountWei,
		GasLimitOverride: f.gasLimit,
		DryRun:           f.dryRun,
	}
	if f.maxFeeGwei != "" {
		v, err := gweiToWei(f.maxFeeGwei)
		if err != nil {
			return fmt.Errorf("--max-fee-gwei: %w", err)
		}
		req.MaxFeeWei = v
	}
	if f.maxPriorityGwei != "" {
		v, err := gweiToWei(f.maxPriorityGwei)
		if err != nil {
			return fmt.Errorf("--max-priority-fee-gwei: %w", err)
		}
		req.MaxPriorityFeeWei = v
	}
	if f.nonce >= 0 {
		n := uint64(f.nonce)
		req.NonceOverride = &n
	}

	bc, err := acquireBroadcaster(req, net.DefaultRPC)
	if err != nil {
		return err
	}
	defer bc.Close()

	params, err := wallet.PrepareSend(ctx, bc, req)
	if err != nil {
		return err
	}
	resp, err := wallet.Execute(ctx, bc, req, params, time.Now())
	if err != nil {
		return err
	}

	entry := buildSendEntry(w, walletAccount, f, net, amountWei, resp)

	if f.dryRun {
		_, _ = fmt.Fprintln(out, "DRY RUN — nothing broadcast or written.")
		_, _ = fmt.Fprintf(out, "Would broadcast %s on %s\n", resp.TxHash.Hex(), net.Name)
	} else {
		id, err := s.AddEntry(ctx, journalId, entry)
		if err != nil {
			return fmt.Errorf("write journal entry: %w", err)
		}
		_, _ = fmt.Fprintf(out, "Broadcast %s on %s; entry %s\n", resp.TxHash.Hex(), net.Name, id)
	}
	feeEth := wallet.WeiToDecimal(resp.EstimatedFee, net.Decimals)
	_, _ = fmt.Fprintf(out, "  fee: %s %s (gasLimit=%d)\n", feeEth.String(), net.NativeUnit, params.GasLimit)
	_ = cfg // base currency not enforced for native crypto sends (different commodity).
	return nil
}

func acquireBroadcaster(req wallet.SendRequest, defaultRPC string) (wallet.Broadcaster, error) {
	// Fully-offline dry-run: skip the RPC dial when fees + nonce are all
	// supplied by the user.
	if req.DryRun && req.MaxFeeWei != nil && req.MaxPriorityFeeWei != nil && req.NonceOverride != nil {
		return offlineBroadcaster{}, nil
	}
	url := req.Network.DefaultRPC
	if url == "" {
		url = defaultRPC
	}
	return broadcasterFactory(url)
}

func buildSendEntry(w *wallet.Wallet, walletAccount string, f sendFlags, net wallet.Network, amountWei *big.Int, resp *wallet.SendResponse) model.Entry {
	payee := f.contact
	if payee == "" {
		payee = fmt.Sprintf("wallet send %s %s", net.Name, resp.TxHash.Hex())
	}
	amountQty := wallet.WeiToDecimal(amountWei, net.Decimals)
	feeQty := wallet.WeiToDecimal(resp.EstimatedFee, net.Decimals)
	totalDebit := amountQty.Add(feeQty)

	notes := []string{
		"network:" + net.Name,
		"txn:" + resp.TxHash.Hex(),
		"wallet:" + w.Id,
	}
	if f.memo != "" {
		notes = append(notes, f.memo)
	}
	entry := model.Entry{
		Date:   resp.When,
		Payee:  payee,
		Status: model.StatusPending,
		Note:   strings.Join(notes, " "),
		Postings: []model.Posting{
			{Account: f.category, Amount: model.Amount{Quantity: decimalToRat(amountQty), Commodity: net.NativeUnit}},
			{Account: fmt.Sprintf("Expenses:Crypto:Fees:%s", net.Name), Amount: model.Amount{Quantity: decimalToRat(feeQty), Commodity: net.NativeUnit}},
			{Account: walletAccount, Amount: model.Amount{Quantity: decimalToRat(totalDebit.Neg()), Commodity: net.NativeUnit}},
		},
	}
	return entry
}

// --- helpers ---

func parseNetworksFlag(s string) ([]string, error) {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		n := strings.TrimSpace(strings.ToLower(p))
		if n == "" {
			continue
		}
		if _, err := wallet.LookupNetwork(n, ""); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("--networks must list at least one network")
	}
	return out, nil
}

func groupWalletAccounts(accounts []model.Account) map[string][]model.Account {
	groups := map[string][]model.Account{}
	for _, a := range accounts {
		id := a.Tags["wallet"]
		if id == "" {
			continue
		}
		groups[id] = append(groups[id], a)
	}
	return groups
}

var validCategoryTops = map[string]bool{
	"Assets":      true,
	"Liabilities": true,
	"Equity":      true,
	"Income":      true,
	"Expenses":    true,
}

func validateCategory(name string) error {
	if name == "" {
		return errors.New("--category cannot be empty")
	}
	top := strings.SplitN(name, ":", 2)[0]
	if !validCategoryTops[top] {
		return fmt.Errorf("--category %q must start with one of: Assets, Liabilities, Equity, Income, Expenses", name)
	}
	return nil
}

func resolveAmountWei(f sendFlags, decimals int32) (*big.Int, error) {
	switch {
	case f.amountEth != "" && f.amountWei != "":
		return nil, errors.New("pass either --amount-eth or --amount-wei, not both")
	case f.amountEth != "":
		d, err := decimal.NewFromString(f.amountEth)
		if err != nil {
			return nil, fmt.Errorf("--amount-eth: %w", err)
		}
		return wallet.EthToWei(d, decimals)
	case f.amountWei != "":
		v, ok := new(big.Int).SetString(f.amountWei, 10)
		if !ok {
			return nil, fmt.Errorf("--amount-wei: invalid integer %q", f.amountWei)
		}
		return v, nil
	}
	return nil, errors.New("amount required: pass --amount-eth or --amount-wei")
}

func gweiToWei(s string) (*big.Int, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return nil, err
	}
	shifted := d.Shift(9)
	if !shifted.Equal(shifted.Truncate(0)) {
		return nil, fmt.Errorf("more precision than 9 decimals in %s", s)
	}
	return shifted.BigInt(), nil
}

func decimalToRat(d decimal.Decimal) *big.Rat {
	r := new(big.Rat)
	r.SetString(d.String())
	return r
}

// offlineBroadcaster is the no-op Broadcaster used for fully-offline dry-runs.
type offlineBroadcaster struct{}

func (offlineBroadcaster) SuggestFees(ctx context.Context) (*big.Int, *big.Int, error) {
	return nil, nil, errors.New("offline broadcaster: SuggestFees not available — pass --max-fee-gwei + --max-priority-fee-gwei")
}
func (offlineBroadcaster) PendingNonce(ctx context.Context, from wallet.Address) (uint64, error) {
	return 0, errors.New("offline broadcaster: PendingNonce not available — pass --nonce")
}
func (offlineBroadcaster) EstimateGas(ctx context.Context, msg wallet.CallMsg) (uint64, error) {
	return 21000, nil
}
func (offlineBroadcaster) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	return errors.New("offline broadcaster: refusing to broadcast")
}
func (offlineBroadcaster) Close() {}
