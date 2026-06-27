// Package copycmd implements "vv copy clone" and "vv copy import" for
// copying files into and out of vaults. In v1.0 these are cp -r wrappers;
// in v1.1 they will add encrypt/decrypt for encrypted vaults.
package copycmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"vevault/internal/config"

	"github.com/spf13/cobra"
)

// NewCmd returns the "vv copy" subcommand with clone and import subcommands.
func NewCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "copy",
		Short: "Copy files into and out of vaults",
		Long: `Copy files between vaults and the local filesystem.

In v1.0, this copies files directly. In v1.1, copy clone on an
encrypted vault will decrypt files during copy, and copy import
will encrypt them.

Subcommands:
  clone   <vault>[/subdir] <dest>  — Copy out of a vault
  import  <vault>[/subdir] <src>   — Copy into a vault`,
	}

	cmd.AddCommand(newCloneCmd(cfg))
	cmd.AddCommand(newImportCmd(cfg))

	return cmd
}

// newCloneCmd returns "vv copy clone <vault>[/subdir] <dest>".
func newCloneCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "clone <vault>[/subdir] <dest>",
		Short: "Copy files out of a vault to a local destination",
		Long: `Copy the contents of a vault (or a subdirectory within it)
to a local destination path.

Examples:
  vv copy clone personal ~/restore/personal
  vv copy clone personal/docs ~/Documents/restored`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			vaultRef := args[0]
			dest := args[1]
			return cloneVault(cfg, vaultRef, dest)
		},
	}
}

// newImportCmd returns "vv copy import <vault>[/subdir] <src>".
func newImportCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "import <vault>[/subdir] <src>",
		Short: "Copy files from a local source into a vault",
		Long: `Copy files from a local source path into a vault
(or a subdirectory within it).

Examples:
  vv copy import personal ~/Documents/incoming
  vv copy import personal/photos ~/Pictures/vacation`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			vaultRef := args[0]
			src := args[1]
			return importVault(cfg, vaultRef, src)
		},
	}
}

// parseVaultRef splits "vault/subdir" into vault name and optional
// subdirectory path.
func parseVaultRef(ref string) (vaultName, subdir string) {
	parts := strings.SplitN(ref, "/", 2)
	vaultName = parts[0]
	if len(parts) == 2 {
		subdir = parts[1]
	}
	return
}

// cloneVault copies files from a vault to a local destination.
func cloneVault(cfg *config.Config, vaultRef, dest string) error {
	vaultName, subdir := parseVaultRef(vaultRef)

	srcPath := cfg.VaultPath(vaultName)
	if subdir != "" {
		srcPath = filepath.Join(srcPath, subdir)
	}

	// Validate source exists.
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source does not exist: %s", srcPath)
		}
		return fmt.Errorf("accessing source %s: %w", srcPath, err)
	}

	// Validate the path is inside a known vault (safety check).
	if !isInsideVault(cfg, srcPath) {
		return fmt.Errorf("source %s is not inside a known vault", srcPath)
	}

	// Create destination parent directories.
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	fmt.Printf("Copying %s → %s\n", srcPath, dest)

	if srcInfo.IsDir() {
		// If dest exists and is a directory, copy into it.
		if info, err := os.Stat(dest); err == nil && info.IsDir() {
			return copyDir(srcPath, filepath.Join(dest, filepath.Base(srcPath)))
		}
		return copyDir(srcPath, dest)
	}
	return copyFile(srcPath, dest)
}

// importVault copies files from a local source into a vault.
func importVault(cfg *config.Config, vaultRef, src string) error {
	vaultName, subdir := parseVaultRef(vaultRef)

	destPath := cfg.VaultPath(vaultName)
	if subdir != "" {
		destPath = filepath.Join(destPath, subdir)
	}

	// Validate source exists.
	srcInfo, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source does not exist: %s", src)
		}
		return fmt.Errorf("accessing source %s: %w", src, err)
	}

	// Validate vault path exists.
	if _, err := os.Stat(cfg.VaultPath(vaultName)); err != nil {
		return fmt.Errorf("vault %q directory does not exist: %s", vaultName, cfg.VaultPath(vaultName))
	}

	// Validate the destination is inside a known vault (safety check).
	if !isInsideVault(cfg, destPath) {
		return fmt.Errorf("destination %s is not inside a known vault", destPath)
	}

	// Create destination parent directories.
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("creating vault subdirectory: %w", err)
	}

	fmt.Printf("Copying %s → %s\n", src, destPath)

	if srcInfo.IsDir() {
		// Copy directory into vault: src/contents → destPath/<src-basename>/
		return copyDir(src, filepath.Join(destPath, filepath.Base(src)))
	}
	// Copy single file into vault: src → destPath/<src-basename>
	return copyFile(src, filepath.Join(destPath, filepath.Base(src)))
}

// isInsideVault returns true if the given path is within any known vault.
// This prevents accidental copies to/from non-vault locations.
func isInsideVault(cfg *config.Config, path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, v := range cfg.Vaults {
		vaultPath, err := filepath.Abs(cfg.VaultPath(v.Name))
		if err != nil {
			continue
		}
		// Check if abs is inside the vault directory.
		rel, err := filepath.Rel(vaultPath, abs)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}

// copyFile copies a single file from src to dest, preserving permissions.
func copyFile(src, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer srcFile.Close()

	srcStat, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	destFile, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcStat.Mode())
	if err != nil {
		return fmt.Errorf("creating destination: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("copying data: %w", err)
	}

	return nil
}

// copyDir recursively copies a directory tree from src to dest.
// Skips files matching common VCS/temp patterns.
func copyDir(src, dest string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	if err := os.MkdirAll(dest, srcInfo.Mode()); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("reading source directory: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()

		// Skip common ignorable files (same set as bisync excludes).
		if shouldSkip(name) {
			continue
		}

		srcPath := filepath.Join(src, name)
		destPath := filepath.Join(dest, name)

		if entry.IsDir() {
			if err := copyDir(srcPath, destPath); err != nil {
				return err
			}
		} else {
			if entry.Type()&os.ModeSymlink != 0 {
				link, err := os.Readlink(srcPath)
				if err != nil {
					return fmt.Errorf("reading symlink %s: %w", srcPath, err)
				}
				if err := os.Symlink(link, destPath); err != nil {
					return fmt.Errorf("creating symlink %s: %w", destPath, err)
				}
				continue
			}
			if err := copyFile(srcPath, destPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// shouldSkip returns true for files/directories that should be excluded
// from copy operations, matching the bisync exclusion list.
func shouldSkip(name string) bool {
	switch {
	case name == ".DS_Store":
		return true
	case strings.HasPrefix(name, ".~lock."):
		return true
	case strings.HasSuffix(name, "~"):
		return true
	case strings.HasSuffix(name, ".swp"):
		return true
	case strings.HasSuffix(name, ".lck"):
		return true
	case strings.HasPrefix(name, ".lck-"):
		return true
	case strings.HasSuffix(name, ".conflict1"), strings.HasSuffix(name, ".conflict2"):
		return true
	case name == ".git" || name == ".Trash":
		return true
	case name == "node_modules" || name == "__pycache__" || name == ".venv":
		return true
	case name == "target":
		return true
	}
	return false
}