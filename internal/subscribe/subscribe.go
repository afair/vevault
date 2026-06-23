// Package subscribe implements "vv subscribe" and "vv unsubscribe" for
// managing host subscriptions to vaults.
package subscribe

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"vevault/internal/config"

	"github.com/spf13/cobra"
)

// NewSubscribeCmd returns the "vv subscribe" top-level command.
func NewSubscribeCmd(cfg *config.Config) *cobra.Command {
	var (
		hosts   []string
		symlink string
		address string
		vpath   string
	)

	cmd := &cobra.Command{
		Use:   "subscribe <vault>",
		Short: "Subscribe hosts to a vault",
		Long: `Subscribe one or more hosts to a vault.

On the central node, use --host to subscribe other hosts:
    vv subscribe personal --host laptop --host workstation

On a non-central host, subscribe this host (delegates to central via SSH):
    vv subscribe personal --symlink ~/Documents/Personal
    vv subscribe personal --address macbook.tailnet.ts.net`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vaultName := args[0]

			if cfg.IsCentral() {
				return subscribeOnCentral(cfg, vaultName, hosts, address, vpath)
			}
			return subscribeFromRemote(cfg, vaultName, symlink, address)
		},
	}

	cmd.Flags().StringArrayVar(&hosts, "host", nil, "Hosts to subscribe (central-only, repeatable)")
	cmd.Flags().StringVar(&symlink, "symlink", "", "Create a symlink at this path after subscribing (remote only)")
	cmd.Flags().StringVar(&address, "address", "", "How to reach this host (Tailscale name, IP, etc.)")
	cmd.Flags().StringVar(&vpath, "path", "", "Vault path on the remote host (auto-detected from remote)")

	return cmd
}

// NewUnsubscribeCmd returns the "vv unsubscribe" top-level command.
func NewUnsubscribeCmd(cfg *config.Config) *cobra.Command {
	var (
		host  string
		purge bool
	)

	cmd := &cobra.Command{
		Use:   "unsubscribe <vault>",
		Short: "Unsubscribe hosts from a vault",
		Long: `Remove a host's subscription to a vault.

On the central node, use --host:
    vv unsubscribe personal --host laptop

On a non-central host, unsubscribe this host (delegates via SSH):
    vv unsubscribe personal --purge`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vaultName := args[0]

			if cfg.IsCentral() {
				return unsubscribeOnCentral(cfg, vaultName, host)
			}
			return unsubscribeFromRemote(cfg, vaultName, purge)
		},
	}

	cmd.Flags().StringVar(&host, "host", "", "Host to unsubscribe (central-only)")
	cmd.Flags().BoolVar(&purge, "purge", false, "Also delete local vault data and symlinks")

	return cmd
}

// --- central-side logic -----------------------------------------------

func subscribeOnCentral(cfg *config.Config, vaultName string, hosts []string, address, vpath string) error {
	if len(hosts) == 0 {
		return fmt.Errorf("--host is required when running on central")
	}

	v := cfg.Vault(vaultName)
	if v == nil {
		return fmt.Errorf("vault %q does not exist", vaultName)
	}

	var syncTargets []string
	for _, h := range hosts {
		if h == "" {
			continue
		}
		added, err := addSubscription(cfg, vaultName, h, address, vpath)
		if err != nil {
			return err
		}
		if !added {
			fmt.Printf("Host %q is already subscribed to vault %q.\n", h, vaultName)
			continue
		}
		syncTargets = append(syncTargets, h)
	}

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	// Trigger initial sync for each new subscriber.
	for _, h := range syncTargets {
		fmt.Printf("Syncing vault %q to %s (initial sync)...\n", vaultName, h)
		if err := execBisync(cfg, vaultName, h, true); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: initial sync to %s failed: %v\n", h, err)
			fmt.Fprintf(os.Stderr, "  Run 'vv updates %s %s' to retry.\n", h, vaultName)
		}
	}

	fmt.Printf("Subscribed %v to vault %q.\n", hosts, vaultName)
	return nil
}

func unsubscribeOnCentral(cfg *config.Config, vaultName, host string) error {
	if host == "" {
		return fmt.Errorf("--host is required when running on central")
	}

	removed := false
	for i := range cfg.Subscriptions {
		s := &cfg.Subscriptions[i]
		filtered := s.Vaults[:0]
		for _, vn := range s.Vaults {
			if vn == vaultName && s.Host == host {
				removed = true
				continue
			}
			filtered = append(filtered, vn)
		}
		s.Vaults = filtered
	}

	if !removed {
		return fmt.Errorf("host %q is not subscribed to vault %q", host, vaultName)
	}

	// Remove subscription entries with no vaults left.
	kept := cfg.Subscriptions[:0]
	for _, s := range cfg.Subscriptions {
		if len(s.Vaults) > 0 {
			kept = append(kept, s)
		}
	}
	cfg.Subscriptions = kept

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Printf("Unsubscribed %s from vault %q.\n", host, vaultName)
	return nil
}

// --- remote-side logic -----------------------------------------------

func subscribeFromRemote(cfg *config.Config, vaultName, symlink, address string) error {
	central := cfg.Core.CentralHost
	if central == "" {
		return fmt.Errorf("central_host not set — cannot subscribe from a remote host without it")
	}

	myHost, _ := os.Hostname()
	if address != "" {
		myHost = address // Use --address value as the host identity.
	}

	// Create the local vault directory so rclone bisync --resync can
	// populate it from central.
	vaultPath := cfg.VaultPath(vaultName)
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		return fmt.Errorf("creating local vault directory: %w", err)
	}

	fmt.Printf("Subscribing this host (%s) to vault %q via %s...\n", myHost, vaultName, central)

	// 1. Delegate subscription + initial sync to central.
	args := []string{cfg.CentralAddress(), "vv", "subscribe", vaultName, "--host", myHost}
	if address != "" {
		args = append(args, "--address", address)
	}
	// Pass the remote vault path so central can store it for this host.
	remoteVaultPath := cfg.VaultPath(vaultName)
	args = append(args, "--path", remoteVaultPath)
	cmd := exec.Command("ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("remote subscribe failed: %w", err)
	}

	// 2. Create symlink if requested (vault dir now exists after sync).
	if symlink != "" {
		vaultPath := cfg.VaultPath(vaultName)
		if err := createSymlink(symlink, vaultPath); err != nil {
			return fmt.Errorf("creating symlink: %w", err)
		}
		fmt.Printf("Created symlink: %s -> %s\n", symlink, vaultPath)
	}

	// 3. Add vault to local config so vault list/info work.
	if cfg.Vault(vaultName) == nil {
		cfg.Vaults = append(cfg.Vaults, config.VaultConfig{
			Name:     vaultName,
			Path:     cfg.VaultPath(vaultName),
			Symlinks: symlinks(symlink),
		})
		// Store the host identity for future sync delegation.
		if myHost != cfg.Core.LocalHost && cfg.Core.LocalHost == "" {
			cfg.Core.LocalHost = myHost
		}
		if err := cfg.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: saving local config: %v\n", err)
		}
	}

	return nil
}

func unsubscribeFromRemote(cfg *config.Config, vaultName string, purge bool) error {
	central := cfg.Core.CentralHost
	if central == "" {
		return fmt.Errorf("central_host not set")
	}

	myHost := cfg.LocalHostName()

	// 1. Delegate unsubscribe to central (best-effort).
	args := []string{cfg.CentralAddress(), "vv", "unsubscribe", vaultName, "--host", myHost}
	cmd := exec.Command("ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	sshErr := cmd.Run()

	// 2. Optionally purge local data (happens regardless of SSH result).
	if purge {
		purgeLocalVault(cfg, vaultName)
	}

	if sshErr != nil {
		return fmt.Errorf("remote unsubscribe failed: %w", sshErr)
	}
	return nil
}

func purgeLocalVault(cfg *config.Config, vaultName string) {
	v := cfg.Vault(vaultName)
	if v == nil {
		return
	}
	// Remove symlinks.
	for _, s := range v.Symlinks {
		os.Remove(s)
	}
	// Remove vault directory.
	vaultPath := cfg.VaultPath(vaultName)
	if err := os.RemoveAll(vaultPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: removing vault data: %v\n", err)
	} else {
		fmt.Printf("Removed local vault data at %s\n", vaultPath)
	}

	// Remove from local config.
	filtered := cfg.Vaults[:0]
	for _, x := range cfg.Vaults {
		if x.Name != vaultName {
			filtered = append(filtered, x)
		}
	}
	cfg.Vaults = filtered
	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: saving local config: %v\n", err)
	}
}

// --- helpers ----------------------------------------------------------

func addSubscription(cfg *config.Config, vaultName, host, address, vpath string) (bool, error) {
	// Find existing subscription for this host or create one.
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].Host == host {
			// Update address if provided.
			if address != "" {
				cfg.Subscriptions[i].Address = address
			}
			// Update path override if provided.
			if vpath != "" {
				if cfg.Subscriptions[i].Paths == nil {
					cfg.Subscriptions[i].Paths = make(map[string]string)
				}
				cfg.Subscriptions[i].Paths[vaultName] = vpath
			}
			// Check if already subscribed.
			for _, v := range cfg.Subscriptions[i].Vaults {
				if v == vaultName {
					return false, nil // Already subscribed.
				}
			}
			cfg.Subscriptions[i].Vaults = append(cfg.Subscriptions[i].Vaults, vaultName)
			return true, nil
		}
	}

	// New subscription entry.
	sub := config.Subscription{
		Host:    host,
		Address: address,
		Vaults:  []string{vaultName},
	}
	if vpath != "" {
		sub.Paths = map[string]string{vaultName: vpath}
	}
	cfg.Subscriptions = append(cfg.Subscriptions, sub)
	return true, nil
}

// execBisync shells out to rclone bisync. If resync is true, central is
// treated as authoritative (used for initial subscription sync).
func execBisync(cfg *config.Config, vaultName, host string, resync bool) error {
	localPath := cfg.VaultPath(vaultName)
	remotePath := cfg.RemoteVaultPath(vaultName, host)
	home, _ := os.UserHomeDir()

	args := []string{
		"bisync",
		localPath,
		fmt.Sprintf(":sftp,host=%s,known_hosts_file=%s/.ssh/known_hosts:%s",
			cfg.HostAddress(host), home, remotePath),
		"--create-empty-src-dirs",
	}
	if resync {
		args = append(args, "--resync")
	}
	args = append(args,
		"--log-level", "ERROR",
		"--exclude", ".DS_Store",
		"--exclude", "*.lck",
		"--exclude", "*.lck-*",
		"--exclude", "*.conflict1",
		"--exclude", "*.conflict2",
		"--exclude", "*~",
		"--exclude", "*.swp",
		"--exclude", ".~lock.*",
		"--exclude", ".Trash/",
		"--exclude", "node_modules/",
		"--exclude", "__pycache__/",
		"--exclude", ".venv/",
		"--exclude", ".git/",
		"--exclude", "target/",
	)

	// If a .vvignore file exists in the vault, pass it as a filter.
	vvignore := filepath.Join(localPath, ".vvignore")
	if _, err := os.Stat(vvignore); err == nil {
		args = append(args, "--filter-from", vvignore)
	}

	fmt.Printf("  rclone %s\n", strings.Join(args, " "))

	cmd := exec.Command("rclone", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func createSymlink(target, source string) error {
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("symlink target already exists: %s", target)
	}
	return os.Symlink(source, target)
}

func symlinks(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}