package main

import (
	"fmt"
	"os"

	"vevault/internal/config"
	"vevault/internal/initcmd"
	"vevault/internal/sync"
	"vevault/internal/vault"

	"github.com/spf13/cobra"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vevault: %v\n", err)
		os.Exit(1)
	}

	root := &cobra.Command{
		Use:   "vv",
		Short: "VeVault — personal file vault management",
		Long: `VeVault manages a share of encrypted, synchronized file vaults across
multiple hosts. Vaults are synchronized bidirectionally with a central
node over SSH using rclone bisync.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(initcmd.NewCmd(cfg))
	root.AddCommand(vault.NewCmd(cfg))
	root.AddCommand(sync.NewCmd(cfg))
	root.AddCommand(sync.NewUpdatesCmd(cfg))
	// Future: root.AddCommand(subscribe.NewCmd(cfg))
	// Future: root.AddCommand(backup.NewCmd(cfg))
	// Future: root.AddCommand(crypto.NewCmd(cfg))

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "vv: %v\n", err)
		os.Exit(1)
	}
}