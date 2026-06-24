// Package initcmd implements the "vv init" command, which bootstraps the
// vevault directory structure and configuration on a new host.
package initcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vevault/internal/config"

	"github.com/spf13/cobra"
)

// NewCmd returns the "vv init" subcommand.
func NewCmd(cfg *config.Config) *cobra.Command {
	var (
		central string
		vaults  string
		force   bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize vevault on this host",
		Long: `Initialize the vevault directory structure and configuration.

Creates ~/.local/share/vevault/ with the full directory tree and a
default config.toml. Safe to run multiple times — existing config is
preserved unless --force is used.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cfg, central, vaults, force)
		},
	}

	cmd.Flags().StringVar(&central, "central", "", "SSH alias for the central node")
	cmd.Flags().StringVar(&vaults, "vaults", "", "Comma-separated vault names to create")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing config")

	return cmd
}

func runInit(cfg *config.Config, central, vaultList string, force bool) error {
	dir := config.Dir()
	configPath := config.Path()

	profile := os.Getenv("VEVAULT_PROFILE")
	profileLabel := ""
	if profile != "" && profile != "vevault" && os.Getenv("VV_HOME") == "" {
		profileLabel = fmt.Sprintf(" [profile %q]", profile)
	}

	// Check if already initialized.
	if _, err := os.Stat(configPath); err == nil && !force {
		fmt.Printf("Vevault is already initialized at %s%s\n", dir, profileLabel)
		fmt.Println("Use --force to overwrite the existing configuration.")
		return nil
	}

	if force {
		fmt.Printf("Re-initializing vevault at %s%s...\n", dir, profileLabel)
	} else {
		fmt.Printf("Initializing vevault at %s%s...\n", dir, profileLabel)
	}

	// Create directory structure.
	//
	// vaults/ - all vault data (plaintext for unencrypted vaults,
	//           ciphertext via gocryptfs for encrypted vaults)
	// keys/   - encryption key material (v1.1)
	// backups/- backup config cache (v1.1)
	dirs := []string{
		cfg.Core.VaultsDir,
		filepath.Join(dir, "keys"),
		filepath.Join(dir, "backups"),
	}

	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
		fmt.Printf("  ✓ %s/\n", d)
	}

	// Build config.
	if force {
		cfg.Vaults = nil // Reset vaults on re-init.
	}
	if central != "" {
		cfg.Core.CentralHost = central
	}
	if cfg.Core.VaultsDir == "" {
		cfg.Core.VaultsDir = filepath.Join(dir, "vaults")
	}

	// Create initial vaults if requested.
	if vaultList != "" {
		for _, name := range strings.Split(vaultList, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			vaultPath := cfg.VaultPath(name)

			if err := os.MkdirAll(vaultPath, 0o755); err != nil {
				return fmt.Errorf("creating vault %q: %w", name, err)
			}

			cfg.Vaults = append(cfg.Vaults, config.VaultConfig{
				Name: name,
				Path: vaultPath,
			})
		}
	}

	// Write config.
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	fmt.Printf("  ✓ %s\n", configPath)

	// Print next steps.
	fmt.Println()
	fmt.Println("vevault is ready.")
	printNextSteps(cfg)

	return nil
}

func printNextSteps(cfg *config.Config) {
	fmt.Println()
	fmt.Println("Next steps:")

	// If this isn't the central node, tell them to set up.
	if cfg.Core.CentralHost != "" {
		fmt.Println("  1. Ensure this host can SSH to the central node:")
		fmt.Printf("       ssh %s echo ok\n", cfg.Core.CentralHost)
		fmt.Println("  2. Subscribe this host to vaults from here:")
		fmt.Println("       vv subscribe <vault> --symlink ~/<vault>")
		fmt.Println()
		fmt.Println("   Or from the central node:")
		fmt.Printf("       ssh %s vv subscribe <vault> --host $(hostname)\n", cfg.Core.CentralHost)
	} else {
		fmt.Println("  1. Create your first vault:")
		fmt.Println("       vv vault create personal")
		fmt.Println("  2. Subscribe remote hosts to vaults:")
		fmt.Println("       vv subscribe personal --host laptop")
		fmt.Println("  3. Sync with a host:")
		fmt.Println("       vv updates laptop")
	}
	fmt.Println()
}

// AlreadyInitialized returns true if config.toml exists at the default path.
func AlreadyInitialized() bool {
	_, err := os.Stat(config.Path())
	return err == nil || !errors.Is(err, os.ErrNotExist)
}