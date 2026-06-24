// Package config handles loading, validating, and saving the vevault TOML
// configuration file (~/.local/share/vevault/config.toml).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the top-level vevault configuration.
type Config struct {
	Core          CoreConfig     `toml:"core"`
	Vaults        []VaultConfig  `toml:"vaults"`
	Subscriptions []Subscription `toml:"subscriptions"`

	// path is the absolute path to the config file on disk.
	path string
}

// CoreConfig holds global settings.
type CoreConfig struct {
	CentralHost    string `toml:"central_host"`
	CentralAddress string `toml:"central_address,omitempty"` // How to reach central (SSH/rclone). Falls back to central_host.
	LocalHost      string `toml:"local_host,omitempty"`      // This host's identity on the vault network. Falls back to os.Hostname().
	VaultsDir      string `toml:"vaults_dir"`
}

// VaultConfig defines a single vault.
type VaultConfig struct {
	Name       string           `toml:"name"`
	Path       string           `toml:"path,omitempty"`       // Override for vaults_dir/<name>
	Symlinks   []string         `toml:"symlinks,omitempty"`
	Encryption EncryptionConfig `toml:"encryption,omitempty"` // v1.1
	Backup     BackupConfig     `toml:"backup,omitempty"`     // v1.1
	Backups    []BackupConfig   `toml:"backups,omitempty"`    // v1.1 (3-2-1 multi-destination)
}

// EncryptionConfig holds encryption settings for a vault (v1.1).
// When Encryption.Enabled is true, the vault directory stores ciphertext
// managed by gocryptfs. A FUSE mount provides a plaintext view for daily use.
type EncryptionConfig struct {
	Enabled bool   `toml:"enabled"`
	Cipher  string `toml:"cipher,omitempty"` // "xchacha20-poly1305" (default) or "aes-gcm"
}

// BackupConfig holds backup settings for a vault (v1.1).
// Backups use restic repositories. When multiple [[backups]] entries exist,
// each defines a separate destination for 3-2-1 backup strategy.
type BackupConfig struct {
	Enabled      bool             `toml:"enabled"`
	Repo         string           `toml:"repo,omitempty"`
	PasswordCmd  string           `toml:"password_cmd,omitempty"`  // e.g. "pass show restic/personal"
	PasswordFile string           `toml:"password_file,omitempty"` // Path to file containing password
	PostSync     bool             `toml:"post_sync,omitempty"`     // Auto-backup after successful sync
	Schedule     string           `toml:"schedule,omitempty"`      // "post-sync", "daily", "weekly"
	Retention    RetentionPolicy  `toml:"retention,omitempty"`
	Exclude      []string         `toml:"exclude,omitempty"`
}

// RetentionPolicy defines how many snapshots to keep per time bucket (v1.1).
type RetentionPolicy struct {
	Daily   int `toml:"daily,omitempty"`
	Weekly  int `toml:"weekly,omitempty"`
	Monthly int `toml:"monthly,omitempty"`
	Yearly  int `toml:"yearly,omitempty"`
}

// Subscription maps a remote host to the vaults it subscribes to.
type Subscription struct {
	Host    string            `toml:"host"`
	Address string            `toml:"address,omitempty"` // How to reach this host (SSH/rclone). Falls back to Host.
	Vaults  []string          `toml:"vaults"`
	Paths   map[string]string `toml:"paths,omitempty"` // Per-vault path overrides for this host
}

// Dir returns the vevault data directory.
//
// Resolution order:
//  1. VV_HOME env var (absolute path override, e.g. for testing)
//  2. VEVAULT_PROFILE env var → ~/.local/share/<profile>/
//  3. Default → ~/.local/share/vevault/
func Dir() string {
	if d := os.Getenv("VV_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	profile := os.Getenv("VEVAULT_PROFILE")
	if profile == "" {
		profile = "vevault"
	}
	return filepath.Join(home, ".local", "share", profile)
}

// Path returns the full path to config.toml inside Dir().
func Path() string {
	return filepath.Join(Dir(), "config.toml")
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		Core: CoreConfig{
			VaultsDir: filepath.Join(Dir(), "vaults"),
		},
		path: Path(),
	}
}

// Load reads and parses the config file, falling back to defaults if it
// does not exist.
func Load() (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(cfg.path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // First run; use defaults.
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes the current configuration to disk, creating parent
// directories as needed.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	f, err := os.Create(c.path)
	if err != nil {
		return fmt.Errorf("creating config file: %w", err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// Validate checks the configuration for internal consistency.
func (c *Config) Validate() error {
	if c.Core.VaultsDir == "" {
		return fmt.Errorf("core.vaults_dir must not be empty")
	}

	seen := map[string]bool{}
	for _, v := range c.Vaults {
		if v.Name == "" {
			return fmt.Errorf("vault name must not be empty")
		}
		if seen[v.Name] {
			return fmt.Errorf("duplicate vault name: %q", v.Name)
		}
		seen[v.Name] = true
	}

	// Validate subscriptions reference real vaults.
	for _, s := range c.Subscriptions {
		for _, vn := range s.Vaults {
			if !seen[vn] {
				return fmt.Errorf("subscription for host %q references unknown vault %q", s.Host, vn)
			}
		}
	}

	return nil
}

// IsCentral returns true if this host is configured as the central node.
// A host is central when CentralHost is empty or matches the local hostname.
func (c *Config) IsCentral() bool {
	if c.Core.CentralHost == "" {
		return true
	}
	hostname, _ := os.Hostname()
	return c.Core.CentralHost == hostname
}

// Vault returns the VaultConfig for the named vault, or nil if not found.
func (c *Config) Vault(name string) *VaultConfig {
	for i := range c.Vaults {
		if c.Vaults[i].Name == name {
			return &c.Vaults[i]
		}
	}
	return nil
}

// VaultPath returns the absolute filesystem path for a vault, resolving
// the per-vault override or falling back to vaults_dir/<name>.
// Paths starting with ~ are expanded relative to the current user's home.
func (c *Config) VaultPath(name string) string {
	v := c.Vault(name)
	var p string
	if v != nil && v.Path != "" {
		p = v.Path
	} else {
		p = filepath.Join(c.Core.VaultsDir, name)
	}
	return expandTilde(p)
}

// SubscribedVaults returns the vault names subscribed by the given host.
func (c *Config) SubscribedVaults(host string) []string {
	for _, s := range c.Subscriptions {
		if s.Host == host {
			return s.Vaults
		}
	}
	return nil
}

// RemoteVaultPath returns the filesystem path for a vault on a specific
// remote host. Checks the subscription's paths override first, then falls
// back to the local vault path (suitable when hosts have the same layout).
func (c *Config) RemoteVaultPath(vaultName, host string) string {
	for _, s := range c.Subscriptions {
		if s.Host == host && s.Paths != nil {
			if p, ok := s.Paths[vaultName]; ok && p != "" {
				return p
			}
		}
	}
	return c.VaultPath(vaultName)
}

// CentralAddress returns the address to reach the central node.
// Falls back to CentralHost if CentralAddress is not set.
func (c *Config) CentralAddress() string {
	if c.Core.CentralAddress != "" {
		return c.Core.CentralAddress
	}
	return c.Core.CentralHost
}

// HostAddress returns the address to reach a subscribed host.
// Falls back to the host identity if no address override is set.
func (c *Config) HostAddress(host string) string {
	for _, s := range c.Subscriptions {
		if s.Host == host {
			if s.Address != "" {
				return s.Address
			}
			return s.Host
		}
	}
	return host
}

// LocalHostName returns the configured local host identity, or the system
// hostname as a fallback.
func (c *Config) LocalHostName() string {
	if c.Core.LocalHost != "" {
		return c.Core.LocalHost
	}
	h, _ := os.Hostname()
	return h
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}