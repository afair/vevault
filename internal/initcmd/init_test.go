package initcmd

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
	return cfg, home
}

// --- Fresh init -------------------------------------------------------

func TestInit_Fresh(t *testing.T) {
	cfg, home := setup(t)

	if err := runInit(cfg, "", "", false); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Check directories exist.
	for _, sub := range []string{"vaults", "keys", "backups"} {
		p := filepath.Join(home, sub)
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			t.Errorf("directory %s missing", sub)
		}
	}

	// Check config file exists.
	if _, err := os.Stat(config.Path()); err != nil {
		t.Error("config.toml was not created")
	}
}

func TestInit_WithVaults(t *testing.T) {
	cfg, home := setup(t)

	if err := runInit(cfg, "", "personal,work", false); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if len(cfg.Vaults) != 2 {
		t.Fatalf("expected 2 vaults, got %d", len(cfg.Vaults))
	}

	for _, name := range []string{"personal", "work"} {
		v := cfg.Vault(name)
		if v == nil {
			t.Errorf("vault %q not in config", name)
			continue
		}
		vaultPath := filepath.Join(home, "vaults", name)
		if v.Path != vaultPath {
			t.Errorf("vault %q path = %q, want %q", name, v.Path, vaultPath)
		}
		if _, err := os.Stat(vaultPath); os.IsNotExist(err) {
			t.Errorf("vault %q directory not created", name)
		}
	}
}

func TestInit_WithCentral(t *testing.T) {
	cfg, _ := setup(t)

	if err := runInit(cfg, "homeserver", "", false); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if cfg.Core.CentralHost != "homeserver" {
		t.Errorf("CentralHost = %q, want homeserver", cfg.Core.CentralHost)
	}
}

func TestInit_WithVaultsAndCentral(t *testing.T) {
	cfg, _ := setup(t)

	if err := runInit(cfg, "nas", "docs,media", false); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if cfg.Core.CentralHost != "nas" {
		t.Errorf("CentralHost = %q, want nas", cfg.Core.CentralHost)
	}
	if len(cfg.Vaults) != 2 {
		t.Errorf("expected 2 vaults, got %d", len(cfg.Vaults))
	}
}

// --- Re-init ----------------------------------------------------------

func TestInit_AlreadyInitialized_NoOp(t *testing.T) {
	cfg, _ := setup(t)

	// First init.
	if err := runInit(cfg, "", "", false); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Modify config in-memory after first init.
	cfg.Core.CentralHost = "first"

	// Second init without force should no-op (config unchanged).
	if err := runInit(cfg, "second", "", false); err != nil {
		t.Fatalf("second init: %v", err)
	}

	// CentralHost should still be "first".
	if cfg.Core.CentralHost != "first" {
		t.Errorf("CentralHost = %q, want 'first' (should not have been overwritten)", cfg.Core.CentralHost)
	}
}

func TestInit_Force(t *testing.T) {
	cfg, _ := setup(t)

	// First init.
	if err := runInit(cfg, "", "", false); err != nil {
		t.Fatalf("first init: %v", err)
	}
	firstVaults := len(cfg.Vaults)

	// Force re-init should succeed.
	if err := runInit(cfg, "newcentral", "", true); err != nil {
		t.Fatalf("force re-init: %v", err)
	}

	if cfg.Core.CentralHost != "newcentral" {
		t.Errorf("CentralHost = %q, want newcentral", cfg.Core.CentralHost)
	}
	// Vaults should be reset (no --vaults flag means empty).
	if len(cfg.Vaults) != firstVaults {
		t.Errorf("expected %d vaults after force init, got %d", firstVaults, len(cfg.Vaults))
	}
}

func TestInit_ForceWithVaults(t *testing.T) {
	cfg, _ := setup(t)

	// First init with vaults.
	if err := runInit(cfg, "", "old1,old2", false); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Force re-init with different vaults.
	if err := runInit(cfg, "", "new1", true); err != nil {
		t.Fatalf("force re-init: %v", err)
	}

	if len(cfg.Vaults) != 1 || cfg.Vaults[0].Name != "new1" {
		t.Errorf("vaults = %v, want [new1]", cfg.Vaults)
	}
}

// --- AlreadyInitialized -----------------------------------------------

func TestAlreadyInitialized_False(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VV_HOME", home)

	if AlreadyInitialized() {
		t.Error("expected false on fresh dir")
	}
}

func TestAlreadyInitialized_True(t *testing.T) {
	cfg, _ := setup(t)
	if err := runInit(cfg, "", "", false); err != nil {
		t.Fatal(err)
	}
	if !AlreadyInitialized() {
		t.Error("expected true after init")
	}
}

// --- Vaults directory default -----------------------------------------

func TestVaultsDirDefault(t *testing.T) {
	cfg, home := setup(t)
	// Before init, Core.VaultsDir is already set by Default().
	if cfg.Core.VaultsDir != filepath.Join(home, "vaults") {
		t.Errorf("default VaultsDir = %q, want %q", cfg.Core.VaultsDir, filepath.Join(home, "vaults"))
	}
}