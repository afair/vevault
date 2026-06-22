package config

import (
	"os"
	"path/filepath"
	"testing"
)

func tempHome(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("VV_HOME", d)
	return d
}

func writeTOML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// --- Load / Default --------------------------------------------------

func TestLoad_NoConfig_ReturnsDefaults(t *testing.T) {
	dir := tempHome(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Core.VaultsDir != filepath.Join(dir, "vaults") {
		t.Errorf("VaultsDir = %q, want %q", cfg.Core.VaultsDir, filepath.Join(dir, "vaults"))
	}
	if len(cfg.Vaults) != 0 {
		t.Errorf("expected 0 vaults, got %d", len(cfg.Vaults))
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	home := tempHome(t)
	configPath := filepath.Join(home, "config.toml")
	writeTOML(t, configPath, `
[core]
central_host = "homeserver"
vaults_dir = "/data/vaults"

[[vaults]]
name = "personal"
path = "/data/vaults/personal"

[[vaults]]
name = "work"
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Core.CentralHost != "homeserver" {
		t.Errorf("CentralHost = %q, want homeserver", cfg.Core.CentralHost)
	}
	if cfg.Core.VaultsDir != "/data/vaults" {
		t.Errorf("VaultsDir = %q", cfg.Core.VaultsDir)
	}
	if len(cfg.Vaults) != 2 {
		t.Fatalf("expected 2 vaults, got %d", len(cfg.Vaults))
	}
	if cfg.Vaults[1].Name != "work" {
		t.Errorf("Vaults[1].Name = %q", cfg.Vaults[1].Name)
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	home := tempHome(t)
	configPath := filepath.Join(home, "config.toml")
	writeTOML(t, configPath, `this is not toml [[[`)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

// --- Save + Roundtrip -------------------------------------------------

func TestSaveAndLoad_Roundtrip(t *testing.T) {
	home := tempHome(t)
	cfg := Default()
	cfg.Core.CentralHost = "nas"
	cfg.Vaults = []VaultConfig{
		{Name: "docs", Path: "/data/docs"},
		{Name: "media", Path: "/data/media", Symlinks: []string{"/home/user/Media"}},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after Save: %v", err)
	}

	if loaded.Core.CentralHost != "nas" {
		t.Errorf("CentralHost = %q", loaded.Core.CentralHost)
	}
	if len(loaded.Vaults) != 2 {
		t.Fatalf("expected 2 vaults, got %d", len(loaded.Vaults))
	}
	if loaded.Vaults[1].Symlinks[0] != "/home/user/Media" {
		t.Errorf("Symlinks = %v", loaded.Vaults[1].Symlinks)
	}
	if loaded.path != filepath.Join(home, "config.toml") {
		t.Errorf("path = %q", loaded.path)
	}
}

// --- Validate ---------------------------------------------------------

func TestValidate_DuplicateVault(t *testing.T) {
	cfg := Default()
	cfg.Vaults = []VaultConfig{
		{Name: "a"},
		{Name: "a"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for duplicate vault name")
	}
}

func TestValidate_EmptyVaultName(t *testing.T) {
	cfg := Default()
	cfg.Vaults = []VaultConfig{
		{Name: ""},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for empty vault name")
	}
}

func TestValidate_UnknownVaultInSubscription(t *testing.T) {
	cfg := Default()
	cfg.Vaults = []VaultConfig{
		{Name: "real"},
	}
	cfg.Subscriptions = []Subscription{
		{Host: "laptop", Vaults: []string{"real", "fake"}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for subscription to unknown vault")
	}
}

func TestValidate_EmptyVaultsDir(t *testing.T) {
	cfg := Default()
	cfg.Core.VaultsDir = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for empty vaults_dir")
	}
}

// --- IsCentral --------------------------------------------------------

func TestIsCentral_EmptyCentralHost(t *testing.T) {
	cfg := Default()
	if !cfg.IsCentral() {
		t.Error("expected IsCentral=true when CentralHost is empty")
	}
}

func TestIsCentral_MatchesHostname(t *testing.T) {
	cfg := Default()
	hostname, _ := os.Hostname()
	cfg.Core.CentralHost = hostname
	if !cfg.IsCentral() {
		t.Errorf("expected IsCentral=true when CentralHost matches hostname %q", hostname)
	}
}

func TestIsCentral_DifferentHost(t *testing.T) {
	cfg := Default()
	cfg.Core.CentralHost = "some-other-host"
	if cfg.IsCentral() {
		t.Error("expected IsCentral=false for different host")
	}
}

// --- Helpers ----------------------------------------------------------

func TestVault_Found(t *testing.T) {
	cfg := Default()
	cfg.Vaults = []VaultConfig{
		{Name: "a", Path: "/a"},
		{Name: "b", Path: "/b"},
	}
	v := cfg.Vault("b")
	if v == nil || v.Path != "/b" {
		t.Errorf("Vault(b) = %v", v)
	}
}

func TestVault_NotFound(t *testing.T) {
	cfg := Default()
	if v := cfg.Vault("nonexistent"); v != nil {
		t.Errorf("expected nil for unknown vault, got %v", v)
	}
}

func TestVaultPath_PerVaultOverride(t *testing.T) {
	cfg := Default()
	cfg.Core.VaultsDir = "/default"
	cfg.Vaults = []VaultConfig{
		{Name: "custom", Path: "/custom/path"},
	}
	if p := cfg.VaultPath("custom"); p != "/custom/path" {
		t.Errorf("VaultPath = %q, want /custom/path", p)
	}
}

func TestVaultPath_Fallback(t *testing.T) {
	cfg := Default()
	cfg.Core.VaultsDir = "/default"
	cfg.Vaults = []VaultConfig{
		{Name: "standard"},
	}
	if p := cfg.VaultPath("standard"); p != "/default/standard" {
		t.Errorf("VaultPath = %q, want /default/standard", p)
	}
}

func TestVaultPath_Unknown(t *testing.T) {
	cfg := Default()
	cfg.Core.VaultsDir = "/default"
	// Unknown vault still returns the default-derived path.
	if p := cfg.VaultPath("ghost"); p != "/default/ghost" {
		t.Errorf("VaultPath = %q, want /default/ghost", p)
	}
}

func TestSubscribedVaults(t *testing.T) {
	cfg := Default()
	cfg.Subscriptions = []Subscription{
		{Host: "laptop", Vaults: []string{"a", "b"}},
		{Host: "desktop", Vaults: []string{"c"}},
	}
	subs := cfg.SubscribedVaults("laptop")
	if len(subs) != 2 || subs[0] != "a" || subs[1] != "b" {
		t.Errorf("SubscribedVaults(laptop) = %v", subs)
	}
	if s := cfg.SubscribedVaults("nonexistent"); s != nil {
		t.Errorf("expected nil for unsubscribed host, got %v", s)
	}
}

func TestRemoteVaultPath_Override(t *testing.T) {
	cfg := Default()
	cfg.Core.VaultsDir = "/home/allen/vaults"
	cfg.Vaults = []VaultConfig{
		{Name: "docs"},
	}
	cfg.Subscriptions = []Subscription{
		{Host: "macbook", Vaults: []string{"docs"}, Paths: map[string]string{
			"docs": "/Users/allen/vaults/docs",
		}},
	}

	p := cfg.RemoteVaultPath("docs", "macbook")
	if p != "/Users/allen/vaults/docs" {
		t.Errorf("RemoteVaultPath = %q, want /Users/allen/vaults/docs", p)
	}
}

func TestRemoteVaultPath_Fallback(t *testing.T) {
	cfg := Default()
	cfg.Core.VaultsDir = "/home/allen/vaults"
	cfg.Vaults = []VaultConfig{
		{Name: "docs"},
	}
	cfg.Subscriptions = []Subscription{
		{Host: "laptop", Vaults: []string{"docs"}},
	}

	// No paths override — should fall back to VaultPath.
	p := cfg.RemoteVaultPath("docs", "laptop")
	expected := cfg.VaultPath("docs")
	if p != expected {
		t.Errorf("RemoteVaultPath = %q, want %q", p, expected)
	}
}

func TestVaultPath_TildeExpansion(t *testing.T) {
	cfg := Default()
	cfg.Core.VaultsDir = "~/vaults"

	p := cfg.VaultPath("docs")
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, "vaults", "docs")
	if p != expected {
		t.Errorf("VaultPath = %q, want %q", p, expected)
	}
}

func TestVaultPath_TildeOnly(t *testing.T) {
	cfg := Default()
	cfg.Core.VaultsDir = "~"
	cfg.Vaults = []VaultConfig{
		{Name: "rootvault", Path: "~"},
	}

	p := cfg.VaultPath("rootvault")
	home, _ := os.UserHomeDir()
	if p != home {
		t.Errorf("VaultPath = %q, want %q", p, home)
	}
}

func TestCentralAddress_Fallback(t *testing.T) {
	cfg := Default()
	cfg.Core.CentralHost = "homeserver"
	if a := cfg.CentralAddress(); a != "homeserver" {
		t.Errorf("CentralAddress = %q, want homeserver", a)
	}
}

func TestCentralAddress_Override(t *testing.T) {
	cfg := Default()
	cfg.Core.CentralHost = "homeserver"
	cfg.Core.CentralAddress = "100.64.0.5"
	if a := cfg.CentralAddress(); a != "100.64.0.5" {
		t.Errorf("CentralAddress = %q, want 100.64.0.5", a)
	}
}

func TestHostAddress_Fallback(t *testing.T) {
	cfg := Default()
	cfg.Subscriptions = []Subscription{
		{Host: "macbook", Vaults: []string{"docs"}},
	}
	if a := cfg.HostAddress("macbook"); a != "macbook" {
		t.Errorf("HostAddress = %q, want macbook", a)
	}
}

func TestHostAddress_Override(t *testing.T) {
	cfg := Default()
	cfg.Subscriptions = []Subscription{
		{Host: "macbook", Address: "macbook.tailnet.ts.net", Vaults: []string{"docs"}},
	}
	if a := cfg.HostAddress("macbook"); a != "macbook.tailnet.ts.net" {
		t.Errorf("HostAddress = %q, want macbook.tailnet.ts.net", a)
	}
}

func TestHostAddress_Unknown(t *testing.T) {
	cfg := Default()
	if a := cfg.HostAddress("ghost"); a != "ghost" {
		t.Errorf("HostAddress = %q, want ghost", a)
	}
}

func TestLocalHostName_Configured(t *testing.T) {
	cfg := Default()
	cfg.Core.LocalHost = "air"
	if h := cfg.LocalHostName(); h != "air" {
		t.Errorf("LocalHostName = %q, want air", h)
	}
}

func TestLocalHostName_Fallback(t *testing.T) {
	cfg := Default()
	h := cfg.LocalHostName()
	hostname, _ := os.Hostname()
	if h != hostname {
		t.Errorf("LocalHostName = %q, want %q", h, hostname)
	}
}

func TestExpandTilde(t *testing.T) {
	home, _ := os.UserHomeDir()

	cases := []struct{ in, want string }{
		{"/abs/path", "/abs/path"},
		{"~", home},
		{"~/docs", filepath.Join(home, "docs")},
		{"relative/path", "relative/path"},
		{"not~tilde", "not~tilde"},
	}
	for _, c := range cases {
		if got := expandTilde(c.in); got != c.want {
			t.Errorf("expandTilde(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDir_RespectsVVHome(t *testing.T) {
	t.Setenv("VV_HOME", "/custom/vevault")
	t.Setenv("VEVAULT_PROFILE", "should-be-ignored")
	if d := Dir(); d != "/custom/vevault" {
		t.Errorf("Dir() = %q, want /custom/vevault", d)
	}
}

func TestDir_RespectsProfile(t *testing.T) {
	os.Unsetenv("VV_HOME")
	t.Setenv("VEVAULT_PROFILE", "work")
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".local", "share", "work")
	if d := Dir(); d != expected {
		t.Errorf("Dir() = %q, want %q", d, expected)
	}
}

func TestDir_FallsBack(t *testing.T) {
	os.Unsetenv("VV_HOME")
	os.Unsetenv("VEVAULT_PROFILE")
	d := Dir()
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".local", "share", "vevault")
	if d != expected {
		t.Errorf("Dir() = %q, want %q", d, expected)
	}
}