package main

import (
	"fmt"
	"os"
	"strings"

	"vevault/internal/config"
	"vevault/internal/copycmd"
	"vevault/internal/initcmd"
	"vevault/internal/subscribe"
	"vevault/internal/sync"
	"vevault/internal/vault"

	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags. Falls back to "dev" for
// local builds.
var Version = "dev"

func main() {
	// Parse --profile / -p early so config.Dir() knows where to look.
	if profile := parseProfileFlag(); profile != "" {
		os.Setenv("VEVAULT_PROFILE", profile)
	}

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
node over SSH using rclone bisync.

Use --profile <name> to switch between independent vault sets.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip profile check for the init command itself.
			if cmd.Name() == "init" {
				return nil
			}
			if !initcmd.AlreadyInitialized() {
				profile := os.Getenv("VEVAULT_PROFILE")
				if profile != "" && profile != "vevault" {
					return fmt.Errorf("profile %q not initialized — run: vv --profile %s init", profile, profile)
				}
			}
			return nil
		},
	}

	root.PersistentFlags().StringP("profile", "p", "", "Vevault profile name (default: vevault)")
	root.Version = Version

	root.AddCommand(initcmd.NewCmd(cfg))
	root.AddCommand(vault.NewCmd(cfg))
	root.AddCommand(sync.NewCmd(cfg))
	root.AddCommand(sync.NewUpdatesCmd(cfg))
	root.AddCommand(subscribe.NewSubscribeCmd(cfg))
	root.AddCommand(subscribe.NewUnsubscribeCmd(cfg))
	root.AddCommand(copycmd.NewCmd(cfg))
	// Future: root.AddCommand(subscribe.NewCmd(cfg))
	// Future: root.AddCommand(backup.NewCmd(cfg))
	// Future: root.AddCommand(crypto.NewCmd(cfg))

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "vv: %v\n", err)
		os.Exit(1)
	}
}

// parseProfileFlag scans os.Args for --profile or -p before cobra starts,
// so config.Dir() can resolve the profile directory.
func parseProfileFlag() string {
	for i, a := range os.Args {
		switch {
		case (a == "--profile" || a == "-p") && i+1 < len(os.Args):
			return os.Args[i+1]
		case strings.HasPrefix(a, "--profile="):
			return strings.TrimPrefix(a, "--profile=")
		case strings.HasPrefix(a, "-p="):
			return strings.TrimPrefix(a, "-p=")
		case strings.HasPrefix(a, "-p") && a != "-p" && !strings.HasPrefix(a, "-p="):
			return a[2:]
		}
	}
	return ""
}