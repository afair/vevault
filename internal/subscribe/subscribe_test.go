package subscribe

import (
	"os"
	"path/filepath"
	"testing"

	"vevault/internal/config"
)

func setup(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("VV_HOME", home)

	cfg := config.Default()
	cfg.Core.VaultsDir = filepath.Join(home, "vaults")
	cfg.Vaults = []config.VaultConfig{
		{Name: "docs", Path: filepath.Join(home, "vaults", "docs")},
		{Name: "media", Path: filepath.Join(home, "vaults", "media")},
	}
	os.MkdirAll(cfg.VaultPath("docs"), 0o755)
	os.MkdirAll(cfg.VaultPath("media"), 0o755)
	return cfg, home
}

// --- subscribe on central --------------------------------------------

func TestSubscribeOnCentral_SingleHost(t *testing.T) {
	cfg, _ := setup(t)

	if err := subscribeOnCentral(cfg, "docs", []string{"laptop"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	subs := cfg.SubscribedVaults("laptop")
	if len(subs) != 1 || subs[0] != "docs" {
		t.Errorf("SubscribedVaults(laptop) = %v, want [docs]", subs)
	}
}

func TestSubscribeOnCentral_MultipleHosts(t *testing.T) {
	cfg, _ := setup(t)

	if err := subscribeOnCentral(cfg, "media", []string{"laptop", "workstation"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if s := cfg.SubscribedVaults("laptop"); len(s) != 1 || s[0] != "media" {
		t.Errorf("laptop: %v", s)
	}
	if s := cfg.SubscribedVaults("workstation"); len(s) != 1 || s[0] != "media" {
		t.Errorf("workstation: %v", s)
	}
}

func TestSubscribeOnCentral_MultipleVaultsSameHost(t *testing.T) {
	cfg, _ := setup(t)

	if err := subscribeOnCentral(cfg, "docs", []string{"laptop"}); err != nil {
		t.Fatal(err)
	}
	if err := subscribeOnCentral(cfg, "media", []string{"laptop"}); err != nil {
		t.Fatal(err)
	}

	subs := cfg.SubscribedVaults("laptop")
	if len(subs) != 2 {
		t.Errorf("expected 2 vaults, got %v", subs)
	}
}

func TestSubscribeOnCentral_Duplicate(t *testing.T) {
	cfg, _ := setup(t)

	// First subscription.
	if err := subscribeOnCentral(cfg, "docs", []string{"laptop"}); err != nil {
		t.Fatal(err)
	}
	// Duplicate should be a no-op.
	if err := subscribeOnCentral(cfg, "docs", []string{"laptop"}); err != nil {
		t.Fatal(err)
	}

	// Should still have exactly one entry for laptop.
	subs := cfg.SubscribedVaults("laptop")
	if len(subs) != 1 || subs[0] != "docs" {
		t.Errorf("SubscribedVaults = %v, want [docs]", subs)
	}

	// Only one subscription entry total.
	count := 0
	for _, s := range cfg.Subscriptions {
		if s.Host == "laptop" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 subscription entry for laptop, got %d", count)
	}
}

func TestSubscribeOnCentral_NoHostFlag(t *testing.T) {
	cfg, _ := setup(t)

	cmd := NewSubscribeCmd(cfg)
	cmd.SetArgs([]string{"docs"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --host on central")
	}
}

func TestSubscribeOnCentral_UnknownVault(t *testing.T) {
	cfg, _ := setup(t)

	err := subscribeOnCentral(cfg, "nonexistent", []string{"laptop"})
	if err == nil {
		t.Fatal("expected error for unknown vault")
	}
}

// --- unsubscribe on central ------------------------------------------

func TestUnsubscribeOnCentral(t *testing.T) {
	cfg, _ := setup(t)

	// Subscribe first.
	if err := subscribeOnCentral(cfg, "docs", []string{"laptop", "workstation"}); err != nil {
		t.Fatal(err)
	}

	// Unsubscribe one host.
	if err := unsubscribeOnCentral(cfg, "docs", "laptop"); err != nil {
		t.Fatal(err)
	}

	if s := cfg.SubscribedVaults("laptop"); len(s) != 0 {
		t.Errorf("laptop should have no vaults, got %v", s)
	}
	// Workstation should still be subscribed.
	if s := cfg.SubscribedVaults("workstation"); len(s) != 1 || s[0] != "docs" {
		t.Errorf("workstation should still have docs, got %v", s)
	}
}

func TestUnsubscribeOnCentral_LastVaultCleansEntry(t *testing.T) {
	cfg, _ := setup(t)

	if err := subscribeOnCentral(cfg, "docs", []string{"laptop"}); err != nil {
		t.Fatal(err)
	}
	if err := unsubscribeOnCentral(cfg, "docs", "laptop"); err != nil {
		t.Fatal(err)
	}

	// Subscription entry should be removed entirely.
	for _, s := range cfg.Subscriptions {
		if s.Host == "laptop" {
			t.Error("laptop subscription entry should be removed")
		}
	}
}

func TestUnsubscribeOnCentral_NotSubscribed(t *testing.T) {
	cfg, _ := setup(t)

	err := unsubscribeOnCentral(cfg, "docs", "ghost")
	if err == nil {
		t.Fatal("expected error for unsubscribing unsubscribed host")
	}
}

func TestUnsubscribeOnCentral_NoHostFlag(t *testing.T) {
	cfg, _ := setup(t)

	cmd := NewUnsubscribeCmd(cfg)
	cmd.SetArgs([]string{"docs"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --host on central")
	}
}

// --- remote-side subscribe -------------------------------------------

func TestSubscribeFromRemote_NoCentralHost(t *testing.T) {
	cfg, _ := setup(t)
	cfg.Core.CentralHost = ""

	err := subscribeFromRemote(cfg, "docs", "")
	if err == nil {
		t.Fatal("expected error when central_host is not set")
	}
}

// --- remote-side unsubscribe -----------------------------------------

func TestUnsubscribeFromRemote_NoCentralHost(t *testing.T) {
	cfg, _ := setup(t)
	cfg.Core.CentralHost = ""

	err := unsubscribeFromRemote(cfg, "docs", false)
	if err == nil {
		t.Fatal("expected error when central_host is not set")
	}
}

func TestUnsubscribeFromRemote_Purge(t *testing.T) {
	cfg, home := setup(t)
	cfg.Core.CentralHost = "testhost"

	// Add a local-only vault with symlinks to test purge.
	vaultName := "localdocs"
	vaultPath := filepath.Join(home, "vaults", vaultName)
	os.MkdirAll(vaultPath, 0o755)
	os.WriteFile(filepath.Join(vaultPath, "test.txt"), []byte("data"), 0o644)
	symlinkPath := filepath.Join(home, "symlink")
	os.Symlink(vaultPath, symlinkPath)

	cfg.Vaults = append(cfg.Vaults, config.VaultConfig{
		Name:     vaultName,
		Path:     vaultPath,
		Symlinks: []string{symlinkPath},
	})

	// unsubscribeFromRemote tries to SSH to testhost which will fail,
	// but we want to test the purge behavior regardless.
	err := unsubscribeFromRemote(cfg, vaultName, true)
	if err == nil {
		t.Fatal("expected SSH error (no such host)")
	}

	// Purge should still have cleaned local data despite SSH failure.
	if _, err := os.Stat(vaultPath); !os.IsNotExist(err) {
		t.Error("vault directory should be removed after purge")
	}
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Error("symlink should be removed after purge")
	}
	if cfg.Vault(vaultName) != nil {
		t.Error("vault should be removed from local config after purge")
	}
}

// --- helpers ----------------------------------------------------------

func TestAddSubscription_New(t *testing.T) {
	cfg, _ := setup(t)

	added, err := addSubscription(cfg, "docs", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Error("expected added=true for new subscription")
	}
	if len(cfg.Subscriptions) != 1 {
		t.Fatalf("expected 1 subscription entry, got %d", len(cfg.Subscriptions))
	}
}

func TestAddSubscription_Duplicate(t *testing.T) {
	cfg, _ := setup(t)

	addSubscription(cfg, "docs", "laptop")
	added, err := addSubscription(cfg, "docs", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Error("expected added=false for duplicate subscription")
	}
}

func TestAddSubscription_SecondVault(t *testing.T) {
	cfg, _ := setup(t)

	addSubscription(cfg, "docs", "laptop")
	added, err := addSubscription(cfg, "media", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Error("expected added=true for new vault on existing host")
	}
	subs := cfg.SubscribedVaults("laptop")
	if len(subs) != 2 {
		t.Errorf("expected 2 vaults, got %v", subs)
	}
}

func TestSymlinks(t *testing.T) {
	if s := symlinks(""); s != nil {
		t.Errorf("symlinks(\"\") = %v, want nil", s)
	}
	if s := symlinks("/tmp/x"); len(s) != 1 || s[0] != "/tmp/x" {
		t.Errorf("symlinks(\"/tmp/x\") = %v", s)
	}
}

func TestCreateSymlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	dst := filepath.Join(dir, "dst")

	if err := createSymlink(dst, src); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Lstat(dst)
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("not a symlink")
	}
}

func TestCreateSymlink_Exists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	os.MkdirAll(src, 0o755)
	os.Symlink(src, dst)

	if err := createSymlink(dst, src); err == nil {
		t.Fatal("expected error for existing symlink")
	}
}