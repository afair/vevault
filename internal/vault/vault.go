// Package vault implements vault CRUD operations: create, delete, list, info.
package vault

import (
	"fmt"
	"os"
	"path/filepath"

	"vevault/internal/config"

	"github.com/spf13/cobra"
)

// NewCmd returns the "vault" subcommand with its own subcommands.
func NewCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Manage vaults",
		Long:  "Create, delete, list, and inspect vaults.",
	}

	cmd.AddCommand(newCreateCmd(cfg))
	cmd.AddCommand(newDeleteCmd(cfg))
	cmd.AddCommand(newListCmd(cfg))
	cmd.AddCommand(newInfoCmd(cfg))

	return cmd
}

// --- create -----------------------------------------------------------

func newCreateCmd(cfg *config.Config) *cobra.Command {
	var (
		path    string
		symlink string
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new vault (any path on the filesystem)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			if cfg.Vault(name) != nil {
				return fmt.Errorf("vault %q already exists", name)
			}

			vaultPath := path
			if vaultPath == "" {
				vaultPath = cfg.VaultPath(name)
			}

			vaultPath, err := filepath.Abs(vaultPath)
			if err != nil {
				return fmt.Errorf("resolving path: %w", err)
			}

			if err := os.MkdirAll(vaultPath, 0o755); err != nil {
				return fmt.Errorf("creating vault directory: %w", err)
			}

			vc := config.VaultConfig{
				Name: name,
				Path: vaultPath,
			}

			if symlink != "" {
				symlink, err := filepath.Abs(symlink)
				if err != nil {
					return fmt.Errorf("resolving symlink path: %w", err)
				}
				if err := createSymlink(symlink, vaultPath); err != nil {
					return fmt.Errorf("creating symlink: %w", err)
				}
				vc.Symlinks = []string{symlink}
			}

			cfg.Vaults = append(cfg.Vaults, vc)

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("Vault %q created at %s\n", name, vaultPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&path, "path", "", "Vault path (default: VAULTS_DIR/<name>). Can be anywhere.")
	cmd.Flags().StringVar(&symlink, "symlink", "", "Create a symlink at this path pointing to the vault")

	return cmd
}

// --- delete -----------------------------------------------------------

func newDeleteCmd(cfg *config.Config) *cobra.Command {
	var (
		yesImSure  bool
		deleteData bool
	)

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a vault",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			v := cfg.Vault(name)
			if v == nil {
				return fmt.Errorf("vault %q does not exist", name)
			}

			if !yesImSure {
				fmt.Printf("Delete vault %q? Use --yes-im-sure to confirm.\n", name)
				return nil
			}

			// Remove symlinks.
			for _, s := range v.Symlinks {
				if err := os.Remove(s); err != nil && !os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "Warning: removing symlink %s: %v\n", s, err)
				}
			}

			// Remove data directory.
			if deleteData {
				vaultPath := cfg.VaultPath(name)
				if err := os.RemoveAll(vaultPath); err != nil {
					return fmt.Errorf("removing vault data: %w", err)
				}
				fmt.Printf("Removed vault data at %s\n", vaultPath)
			}

			// Remove from config.
			filtered := cfg.Vaults[:0]
			for _, x := range cfg.Vaults {
				if x.Name != name {
					filtered = append(filtered, x)
				}
			}
			cfg.Vaults = filtered

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("Vault %q deleted.\n", name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&yesImSure, "yes-im-sure", false, "Confirm deletion")
	cmd.Flags().BoolVar(&deleteData, "delete-data", false, "Also remove vault directory from disk")

	return cmd
}

// --- list -------------------------------------------------------------

func newListCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all vaults",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(cfg.Vaults) == 0 {
				fmt.Println("No vaults configured.")
				return nil
			}

			for _, v := range cfg.Vaults {
				vaultPath := cfg.VaultPath(v.Name)
				size := dirSize(vaultPath)
				status := "•"
				if v.Encryption.Enabled {
					status = "🔒"
				}
				fmt.Printf("  %s %s  %s  (%s)\n", status, v.Name, formatBytes(size), vaultPath)
			}
			return nil
		},
	}
}

// --- info -------------------------------------------------------------

func newInfoCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show detailed vault information",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			v := cfg.Vault(name)
			if v == nil {
				return fmt.Errorf("vault %q does not exist", name)
			}

			vaultPath := cfg.VaultPath(name)
			size := dirSize(vaultPath)
			files := countFiles(vaultPath)

			fmt.Printf("Name:       %s\n", v.Name)
			fmt.Printf("Path:       %s\n", vaultPath)
			fmt.Printf("Size:       %s\n", formatBytes(size))
			fmt.Printf("Files:      %d\n", files)
			fmt.Printf("Encrypted:  %v\n", v.Encryption.Enabled)
			fmt.Printf("Symlinks:   %v\n", v.Symlinks)

			// Show subscribed hosts (only meaningful on central).
			hosts := cfg.SubscribedVaults(name)
			if len(hosts) > 0 {
				fmt.Printf("Hosts:      %v\n", hosts)
			}

			return nil
		},
	}
}

// --- helpers ----------------------------------------------------------

func createSymlink(target, source string) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("symlink target already exists: %s", target)
	}
	return os.Symlink(source, target)
}

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		size += fi.Size()
		return nil
	})
	return size
}

func countFiles(path string) int {
	var n int
	filepath.Walk(path, func(_ string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !fi.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}