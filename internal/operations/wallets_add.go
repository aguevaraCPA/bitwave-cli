package operations

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-wallet-sdk/wallet"
)

func newWalletsAddCmd() *operation.Definition {
	var name, networksFlag string
	var watch bool
	cmd := &operation.Definition{
		Use:   "add <address>",
		Short: "Track an external EVM address (watch-only, no keystore)",
		Long: `Adds an EVM address as a watch-only wallet: the workspace gets one
account per network tagged with the address, but no keystore file is written.

Watch wallets work with sync (read-only history pull) but not with send
(no private key on disk).`,
		Args: operation.ExactArgs(1),
		RunE: func(cmd *operation.Call, args []string) error {
			if !watch {
				return errors.New("--watch=false is not supported yet; use `bitwave wallets new` to generate a keypair")
			}
			parsed, err := wallet.ParseAddress(args[0])
			if err != nil {
				return fmt.Errorf("invalid address: %w", err)
			}
			addr := parsed.Hex()

			s, _, _, err := resolveStore(cmd.Context())
			if err != nil {
				return err
			}

			p, err := s.Project(cmd.Context())
			if err != nil {
				return err
			}
			for _, a := range p.Accounts {
				if strings.EqualFold(a.Tags["address"], addr) {
					return fmt.Errorf("address %s already tracked by wallet %s (account %s)", addr, a.Tags["wallet"], a.Name)
				}
			}

			if name == "" {
				name = defaultWatchName(addr)
			}
			nets, err := parseNetworksFlag(networksFlag)
			if err != nil {
				return err
			}

			id := wallet.NewID()
			declared := make([]string, 0, len(nets))
			for _, net := range nets {
				acctName := fmt.Sprintf("Assets:Crypto:%s:%s", net, name)
				a := model.Account{
					Name: acctName,
					Type: model.AccountAsset,
					Tags: map[string]string{
						"wallet":  id,
						"address": addr,
						"network": net,
						"watch":   "true",
					},
				}
				if err := s.AddAccount(cmd.Context(), a); err != nil {
					return fmt.Errorf("declare account %s: %w", acctName, err)
				}
				declared = append(declared, acctName)
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Added watch-only wallet %s (%s)\n", id, name)
			_, _ = fmt.Fprintf(out, "  address: %s\n", addr)
			for _, a := range declared {
				_, _ = fmt.Fprintf(out, "  account: %s\n", a)
			}
			_, _ = fmt.Fprintf(out, "\nNext: bitwave wallets sync --wallet %s --network %s\n", name, nets[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Wallet name (defaults to watch_<short-addr>)")
	cmd.Flags().StringVar(&networksFlag, "networks", "ethereum,base", "Comma-separated networks to track")
	cmd.Flags().BoolVar(&watch, "watch", true, "Track without a keystore (read-only sync only)")
	return cmd
}

func defaultWatchName(addr string) string {
	s := strings.TrimPrefix(strings.ToLower(addr), "0x")
	if len(s) > 8 {
		s = s[:8]
	}
	return "watch_" + s
}
