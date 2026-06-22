// Package config handles loading, validating, and saving the vevault TOML
// configuration file (~/.local/share/vevault/config.toml).
package config

import (
	"fmt"
	"os"
	"path/filepath"

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
	CentralHost string `toml:"central_host"`
	VaultsDir   string `toml:"vaults_dir"`
}

// VaultConfig defines a single vault.
type VaultConfig struct {
	Name       string   `toml:"name"`
	Path       string   `toml:"path,omitempty"` // Override for vaults_dir/<name>
	Symlinks   []string `toml:"symlinks,omitempty"`
	Encryption bool     `toml:"encryption"` // v1.1
}

// Subscription maps a remote host to the vaults it subscribes to.
type Subscription struct {
	Host   string   `toml:"host"`
	Vaults []string `toml:"vaults"`
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
func (c *Config) VaultPath(name string) string {
	v := c.Vault(name)
	if v != nil && v.Path != "" {
		return v.Path
	}
	return filepath.Join(c.Core.VaultsDir, name)
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