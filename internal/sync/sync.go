// Package sync implements synchronization of vaults between hosts using
// rclone bisync over SFTP. All sync logic runs on the central node;
// non-central hosts delegate via SSH to "vv updates <host>".
package sync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"vevault/internal/config"

	"github.com/spf13/cobra"
)

// DryRun is set by the --dry-run flag. When true, bisyncVault, runSCP,
// delegateToCentral, and delegateConfigSync print the command they
// would execute instead of running it. Exported for testability.
var DryRun bool

// NewCmd returns the "sync" subcommand.
func NewCmd(cfg *config.Config) *cobra.Command {
	var (
		pull   bool
		resync bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "sync [<vault>]",
		Short: "Synchronize vaults with central node",
		Long: `Synchronize vaults bidirectionally with the central node.

On a non-central host, this delegates to central over SSH:
    ssh <central> vv updates <this-host> [<vault>]

Use --pull on a non-central host to catch up without propagating:
    vv sync --pull          # Pull latest from central, skip fan-out

Use --resync after deleting/recreating a vault to reset bisync state:
    vv sync --resync        # Central's copy is authoritative

Use --dry-run to preview commands without executing them.

On the central node, this syncs all vaults with all subscribed hosts.
Use 'vv updates <host>' to target a single host.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			DryRun = dryRun
			vaultName := ""
			if len(args) == 1 {
				vaultName = args[0]
			}

			if !cfg.IsCentral() {
				return delegateToCentral(cfg, vaultName, pull, resync)
			}
			// On central, sync with all subscribed hosts.
			return syncAll(cfg, vaultName, resync)
		},
	}

	cmd.Flags().BoolVar(&pull, "pull", false, "Pull latest from central without propagating to other hosts")
	cmd.Flags().BoolVarP(&resync, "resync", "R", false, "Reset bisync state — central's copy is authoritative")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print commands without executing them")

	cmd.AddCommand(newConfigSyncCmd(cfg))

	return cmd
}

// NewUpdatesCmd returns a top-level "updates" subcommand, registered in
// main.go alongside "sync". It is intended to be invoked on central,
// either directly or via SSH from a non-central host.
func NewUpdatesCmd(cfg *config.Config) *cobra.Command {
	var (
		noPropagate bool
		resync      bool
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:   "updates <host> [<vault>]",
		Short: "Run 2-way sync with a host and propagate to subscribers (central-only)",
		Long: `Central-only command. Runs rclone bisync with the given host,
then propagates changes to all other subscribers of the affected vaults.

Use --no-propagate (-n) to skip fan-out and only sync with the host.
Use --resync (-R) to reset bisync state after recreating vaults.
Use --dry-run to preview commands without executing them.

This is the target of "vv sync" on non-central hosts.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			DryRun = dryRun
			host := args[0]
			vaultName := ""
			if len(args) == 2 {
				vaultName = args[1]
			}

			if !cfg.IsCentral() {
				return fmt.Errorf("updates must run on the central node")
			}
			return runUpdates(cfg, host, vaultName, noPropagate, resync)
		},
	}

	cmd.Flags().BoolVarP(&noPropagate, "no-propagate", "n", false, "Skip propagating changes to other subscribers")
	cmd.Flags().BoolVarP(&resync, "resync", "R", false, "Reset bisync state — central's copy is authoritative")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print commands without executing them")

	return cmd
}

// newConfigSyncCmd is "vv sync --config": pushes config to all hosts.
func newConfigSyncCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync config to all hosts",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cfg.IsCentral() {
				return delegateConfigSync(cfg)
			}
			return syncConfigToAllHosts(cfg)
		},
	}
	// Override the parent "sync" Use so cobra doesn't conflict.
	cmd.Use = "sync --config"
	return cmd
}

// syncConfigToAllHosts SCPs config.toml and keys/ to every subscribed host.
func syncConfigToAllHosts(cfg *config.Config) error {
	configPath := config.Path()
	keysDir := filepath.Join(config.Dir(), "keys")

	fmt.Println("Syncing config to all subscribed hosts...")

	for _, s := range cfg.Subscriptions {
		addr := cfg.HostAddress(s.Host)
		remoteDir := fmt.Sprintf("%s:~/.local/share/vevault/", addr)

		fmt.Printf("  → %s (%s)\n", s.Host, addr)

		// SCP config.toml.
		if err := runSCP(configPath, remoteDir); err != nil {
			fmt.Fprintf(os.Stderr, "    Warning: config sync to %s failed: %v\n", s.Host, err)
			continue
		}

		// SCP keys/ if the directory exists.
		if info, err := os.Stat(keysDir); err == nil && info.IsDir() {
			if err := runSCP("-r", keysDir, remoteDir); err != nil {
				fmt.Fprintf(os.Stderr, "    Warning: keys sync to %s failed: %v\n", s.Host, err)
			}
		}
	}

	fmt.Println("Config sync complete.")
	return nil
}

// runSCP shells out to scp. extraArgs are passed before the source/target
// (e.g. "-r" for recursive).
func runSCP(args ...string) error {
	if DryRun {
		fmt.Printf("  [dry-run] would execute: scp %s\n", strings.Join(args, " "))
		return nil
	}

	cmd := exec.Command("scp", args...)
	// scp can be noisy; suppress progress unless verbose.
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

// --- sync logic -------------------------------------------------------

// syncAll runs updates for every subscribed host. Used when vv sync is
// invoked on central without targeting a specific host.
func syncAll(cfg *config.Config, vaultName string, resync bool) error {
	if len(cfg.Subscriptions) == 0 {
		fmt.Println("No subscribed hosts.")
		fmt.Println("Hint: subscribe a remote host with 'vv subscribe <vault> --host <host>'")
		return nil
	}

	// Collect unique hosts from subscriptions.
	seen := map[string]bool{}
	var hosts []string
	for _, s := range cfg.Subscriptions {
		if !seen[s.Host] {
			seen[s.Host] = true
			hosts = append(hosts, s.Host)
		}
	}

	fmt.Printf("Syncing with %d host(s)...\n", len(hosts))

	for _, host := range hosts {
		fmt.Printf("\n── %s ──\n", host)
		// Don't propagate — each host gets a direct bisync in its own turn.
		if err := runUpdates(cfg, host, vaultName, true, resync); err != nil {
			return fmt.Errorf("sync with %s: %w", host, err)
		}
	}

	fmt.Printf("\nAll hosts synced (%d total).\n", len(hosts))
	return nil
}

// runUpdates performs the core sync: bisync central with host, then
// optionally propagate to all other subscribers.
func runUpdates(cfg *config.Config, host, vaultName string, noPropagate bool, resync bool) error {
	vaults := vaultList(cfg, host, vaultName)

	if len(vaults) == 0 {
		fmt.Printf("No vaults to sync for host %q.\n", host)
		fmt.Println("Hint: subscribe to a vault first with 'vv subscribe <vault>'")
		return nil
	}

	for _, v := range vaults {
		fmt.Printf("Syncing vault %q with %s...\n", v, host)
		fmt.Printf("  central: %s\n", cfg.VaultPath(v))
		fmt.Printf("  remote:  %s @ %s\n", cfg.RemoteVaultPath(v, host), cfg.HostAddress(host))
		if err := bisyncVault(cfg, v, host, resync); err != nil {
			return fmt.Errorf("bisync %q with %s: %w", v, host, err)
		}
		fmt.Printf("  ✓ %s ↔ %s synced\n\n", v, host)

		if noPropagate {
			continue
		}

		// Propagate to other subscribers.
		for _, s := range cfg.Subscriptions {
			for _, sv := range s.Vaults {
				if sv == v && s.Host != host {
					fmt.Printf("  Propagating %q to %s...\n", v, s.Host)
					if err := bisyncVault(cfg, v, s.Host, resync); err != nil {
						return fmt.Errorf("propagating %q to %s: %w", v, s.Host, err)
					}
				}
			}
		}
	}

	fmt.Printf("\nSync complete for host %q (%d vaults).\n", host, len(vaults))
	if !noPropagate && len(cfg.Subscriptions) > 1 {
		fmt.Println("Propagation to other subscribers complete.")
	}
	return nil
}

// bisyncVault runs rclone bisync between central and a remote host for a
// single vault. Uses RemoteVaultPath for the remote side, falling back to
// the local path when no per-host override is set.
func bisyncVault(cfg *config.Config, vaultName, host string, resync bool) error {
	localPath := cfg.VaultPath(vaultName)
	remotePath := cfg.RemoteVaultPath(vaultName, host)

	// Check rclone is available and >= 1.62 (required for bisync).
	rclonePath, err := exec.LookPath("rclone")
	if err != nil {
		return fmt.Errorf("rclone not found in PATH — install rclone (https://rclone.org) to sync vaults")
	}
	// Quick version probe.
	if out, err := exec.Command(rclonePath, "version", "--check").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: rclone version check failed: %v\n", err)
	} else {
		verLine := strings.SplitN(string(out), "\n", 2)[0]
		fmt.Fprintf(os.Stderr, "  rclone: %s\n", verLine)
	}

	args := []string{
		"bisync",
		localPath,
		fmt.Sprintf(":sftp,host=%s:%s", cfg.HostAddress(host), remotePath),
		"--metadata",
		"--create-empty-src-dirs",
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
	}

	if resync {
		args = append(args, "--resync")
		fmt.Println("  (resync mode — central's copy is authoritative)")
	} else {
		args = append(args, "--force")
	}

	fmt.Printf("  rclone %s\n", strings.Join(args, " "))

	// If a .vvignore file exists in the vault, pass it as a filter.
	vvignore := filepath.Join(localPath, ".vvignore")
	if _, err := os.Stat(vvignore); err == nil {
		args = append(args, "--filter-from", vvignore)
	}

	if DryRun {
		fmt.Printf("  [dry-run] would execute: rclone %s\n", strings.Join(args, " "))
		return nil
	}

	cmd := exec.Command("rclone", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	if err != nil {
		// If bisync exited because state is stale (vault recreated),
		// give the user a clear hint so they can recover.
		if !resync && strings.Contains(err.Error(), "exit status") {
			fmt.Fprintf(os.Stderr, "\nHint: bisync state may be out of date. "+
				"Try --resync (-R) to reset:\n"+
				"  vv sync --resync %s\n", vaultName)
		}
		return err
	}
	return nil
}

// vaultList returns the vaults to sync for a host. If vaultName is
// non-empty, returns just that vault (validating subscription). Otherwise
// returns all vaults the host is subscribed to.
func vaultList(cfg *config.Config, host, vaultName string) []string {
	if vaultName != "" {
		// Validate the host is subscribed to this vault.
		subbed := cfg.SubscribedVaults(host)
		for _, v := range subbed {
			if v == vaultName {
				return []string{vaultName}
			}
		}
		return nil // Not subscribed; bisyncVault will still run (central knows all).
	}
	return cfg.SubscribedVaults(host)
}

// --- delegation -------------------------------------------------------

// delegateToCentral runs "ssh <central> vv updates <this-host> [<vault>]"
// with an optional --no-propagate flag for pull-only syncs.
func delegateToCentral(cfg *config.Config, vaultName string, pull bool, resync bool) error {
	central := cfg.Core.CentralHost
	address := cfg.CentralAddress()
	if central == "" {
		return fmt.Errorf("central_host not configured; set it in %s", config.Path())
	}

	myHost := cfg.LocalHostName()
	if myHost == "" {
		myHost, _ = os.Hostname()
	}

	args := []string{address, "vv", "updates", myHost}
	if vaultName != "" {
		args = append(args, vaultName)
	}
	if pull {
		args = append(args, "--no-propagate")
	}
	if resync {
		args = append(args, "--resync")
	}

	if pull && !resync {
		fmt.Printf("Delegating pull-only sync to central (%s)...\n", address)
	} else if resync {
		fmt.Printf("Delegating resync to central (%s)...\n", address)
	} else {
		fmt.Printf("Delegating to central (%s)...\n", address)
	}
	fmt.Printf("  → ssh %s\n", strings.Join(args, " "))

	if DryRun {
		return nil
	}

	cmd := exec.Command("ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func delegateConfigSync(cfg *config.Config) error {
	central := cfg.Core.CentralHost
	if central == "" {
		return fmt.Errorf("central_host not configured")
	}

	fmt.Printf("Delegating config sync to central (%s)...\n", central)

	sshArgs := []string{cfg.CentralAddress(), "vv", "sync", "--config"}
	if DryRun {
		fmt.Printf("  [dry-run] would execute: ssh %s\n", strings.Join(sshArgs, " "))
		return nil
	}

	cmd := exec.Command("ssh", sshArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- helpers ----------------------------------------------------------

func hostname() string {
	h, _ := os.Hostname()
	return h
}

// EnsureVaultsDir creates the vaults directory on this host if it doesn't
// exist. Called during setup or before first sync.
func EnsureVaultsDir(cfg *config.Config) error {
	for _, v := range cfg.Vaults {
		dir := cfg.VaultPath(v.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating vault dir %s: %w", dir, err)
		}
	}
	return nil
}

// ResolveSSHConfig extracts hostname, user, key file, and port from an
// SSH alias by parsing ~/.ssh/config. Placeholder — returns the alias
// as-is for now.
func ResolveSSHConfig(alias string) (host, user, keyFile, port string) {
	// TODO: Parse ~/.ssh/config properly.
	// For MVP, assume the alias is the hostname and let SSH/rclone handle it.
	return alias, "", "", ""
}