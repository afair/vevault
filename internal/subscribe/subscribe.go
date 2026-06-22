// Package subscribe implements "vv subscribe" and "vv unsubscribe" for
// managing host subscriptions to vaults.
package subscribe

import (
	"fmt"
	"os"
	"os/exec"

	"vevault/internal/config"

	"github.com/spf13/cobra"
)

// NewSubscribeCmd returns the "vv subscribe" top-level command.
func NewSubscribeCmd(cfg *config.Config) *cobra.Command {
	var (
		hosts   []string
		symlink string
	)

	cmd := &cobra.Command{
		Use:   "subscribe <vault>",
		Short: "Subscribe hosts to a vault",
		Long: `Subscribe one or more hosts to a vault.

On the central node, use --host to subscribe other hosts:
    vv subscribe personal --host laptop --host workstation

On a non-central host, subscribe this host (delegates to central via SSH):
    vv subscribe personal --symlink ~/Documents/Personal`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vaultName := args[0]

			if cfg.IsCentral() {
				return subscribeOnCentral(cfg, vaultName, hosts)
			}
			return subscribeFromRemote(cfg, vaultName, symlink)
		},
	}

	cmd.Flags().StringArrayVar(&hosts, "host", nil, "Hosts to subscribe (central-only, repeatable)")
	cmd.Flags().StringVar(&symlink, "symlink", "", "Create a symlink at this path after subscribing (remote only)")

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

func subscribeOnCentral(cfg *config.Config, vaultName string, hosts []string) error {
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
		added, err := addSubscription(cfg, vaultName, h)
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

func subscribeFromRemote(cfg *config.Config, vaultName, symlink string) error {
	central := cfg.Core.CentralHost
	if central == "" {
		return fmt.Errorf("central_host not set — cannot subscribe from a remote host without it")
	}

	myHost, _ := os.Hostname()

	fmt.Printf("Subscribing this host (%s) to vault %q via %s...\n", myHost, vaultName, central)

	// 1. Delegate subscription + initial sync to central.
	args := []string{central, "vv", "subscribe", vaultName, "--host", myHost}
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

	myHost, _ := os.Hostname()

	// 1. Delegate unsubscribe to central (best-effort).
	args := []string{central, "vv", "unsubscribe", vaultName, "--host", myHost}
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

func addSubscription(cfg *config.Config, vaultName, host string) (bool, error) {
	// Find existing subscription for this host or create one.
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].Host == host {
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
	cfg.Subscriptions = append(cfg.Subscriptions, config.Subscription{
		Host:   host,
		Vaults: []string{vaultName},
	})
	return true, nil
}

// execBisync shells out to rclone bisync. If resync is true, central is
// treated as authoritative (used for initial subscription sync).
func execBisync(cfg *config.Config, vaultName, host string, resync bool) error {
	localPath := cfg.VaultPath(vaultName)
	remotePath := cfg.VaultPath(vaultName)

	args := []string{
		"bisync",
		localPath,
		fmt.Sprintf(":sftp:%s:%s", host, remotePath),
		"--sftp-host=" + host,
		"--create-empty-src-dirs",
	}
	if resync {
		args = append(args, "--resync")
	}

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