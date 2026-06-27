package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vevault/internal/config"
)

func init() {
	// All tests in this package run in dry-run mode — they verify command
	// assembly, not actual execution.
	DryRun = true
}

// testConfig builds a Config with the given central host and vaults.
func testConfig(centralHost string, vaults []config.VaultConfig, subs []config.Subscription) *config.Config {
	cfg := config.Default()
	cfg.Core.CentralHost = centralHost
	cfg.Core.VaultsDir = "/home/user/vaults"
	if len(vaults) > 0 {
		cfg.Vaults = vaults
	}
	if len(subs) > 0 {
		cfg.Subscriptions = subs
	}
	return cfg
}

// captureStdout runs fn and returns everything written to stdout.
func captureStdout(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

// ---------------------------------------------------------------------------
// bisyncVault — rclone command assembly
// ---------------------------------------------------------------------------

func TestBisyncVault_Default(t *testing.T) {
	cfg := testConfig("", []config.VaultConfig{
		{Name: "docs", Path: "/home/user/vaults/docs"},
	}, nil)

	out := captureStdout(func() {
		_ = bisyncVault(cfg, "docs", "laptop", false)
	})

	if !strings.Contains(out, "rclone bisync") {
		t.Errorf("expected rclone bisync in output, got: %s", out)
	}
	if !strings.Contains(out, ":sftp,host=laptop:") {
		t.Errorf("expected SFTP backend with host=laptop, got: %s", out)
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("expected --force flag, got: %s", out)
	}
	if strings.Contains(out, "--resync") {
		t.Errorf("did not expect --resync flag, got: %s", out)
	}
	// Default excludes should be present.
	for _, excl := range []string{".DS_Store", "*.conflict1", "node_modules/"} {
		if !strings.Contains(out, excl) {
			t.Errorf("expected exclude %q in rclone args, got: %s", excl, out)
		}
	}
}

func TestBisyncVault_Resync(t *testing.T) {
	cfg := testConfig("", []config.VaultConfig{
		{Name: "docs", Path: "/home/user/vaults/docs"},
	}, nil)

	out := captureStdout(func() {
		_ = bisyncVault(cfg, "docs", "laptop", true)
	})

	if !strings.Contains(out, "--resync") {
		t.Errorf("expected --resync flag in resync mode, got: %s", out)
	}
	if strings.Contains(out, "--force") {
		t.Errorf("did not expect --force flag in resync mode, got: %s", out)
	}
}

func TestBisyncVault_RemotePathOverride(t *testing.T) {
	cfg := testConfig("", []config.VaultConfig{
		{Name: "docs", Path: "/home/user/vaults/docs"},
	}, []config.Subscription{
		{Host: "macbook", Vaults: []string{"docs"}, Paths: map[string]string{
			"docs": "/Users/allen/vaults/docs",
		}},
	})

	out := captureStdout(func() {
		_ = bisyncVault(cfg, "docs", "macbook", false)
	})

	if !strings.Contains(out, "/Users/allen/vaults/docs") {
		t.Errorf("expected macOS path override, got: %s", out)
	}
}

func TestBisyncVault_HostAddress(t *testing.T) {
	cfg := testConfig("", []config.VaultConfig{
		{Name: "docs", Path: "/home/user/vaults/docs"},
	}, []config.Subscription{
		{Host: "laptop", Address: "laptop.tailnet.ts.net", Vaults: []string{"docs"}},
	})

	out := captureStdout(func() {
		_ = bisyncVault(cfg, "docs", "laptop", false)
	})

	if !strings.Contains(out, "host=laptop.tailnet.ts.net") {
		t.Errorf("expected Tailscale address in SFTP host, got: %s", out)
	}
}

func TestBisyncVault_Vvignore(t *testing.T) {
	// Create a temp vault dir with a .vvignore file.
	dir := t.TempDir()
	vvignore := filepath.Join(dir, ".vvignore")
	if err := os.WriteFile(vvignore, []byte("*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig("", []config.VaultConfig{
		{Name: "docs", Path: dir},
	}, nil)

	out := captureStdout(func() {
		_ = bisyncVault(cfg, "docs", "laptop", false)
	})

	if !strings.Contains(out, "--filter-from") || !strings.Contains(out, ".vvignore") {
		t.Errorf("expected --filter-from .vvignore, got: %s", out)
	}
}

func TestBisyncVault_NoVvignore(t *testing.T) {
	dir := t.TempDir() // no .vvignore

	cfg := testConfig("", []config.VaultConfig{
		{Name: "docs", Path: dir},
	}, nil)

	out := captureStdout(func() {
		_ = bisyncVault(cfg, "docs", "laptop", false)
	})

	if strings.Contains(out, "--filter-from") {
		t.Errorf("did not expect --filter-from when no .vvignore exists, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// delegateToCentral — SSH command assembly
// ---------------------------------------------------------------------------

func TestDelegateToCentral_Default(t *testing.T) {
	cfg := testConfig("homeserver", nil, nil)
	// Override local host name for determinism.
	cfg.Core.LocalHost = "testhost"

	out := captureStdout(func() {
		_ = delegateToCentral(cfg, "docs", false, false)
	})

	if !strings.Contains(out, "ssh homeserver vv updates testhost docs") {
		t.Errorf("expected SSH delegation command, got: %s", out)
	}
}

func TestDelegateToCentral_Pull(t *testing.T) {
	cfg := testConfig("homeserver", nil, nil)
	cfg.Core.LocalHost = "testhost"

	out := captureStdout(func() {
		_ = delegateToCentral(cfg, "", true, false)
	})

	if !strings.Contains(out, "--no-propagate") {
		t.Errorf("expected --no-propagate in pull mode, got: %s", out)
	}
}

func TestDelegateToCentral_Resync(t *testing.T) {
	cfg := testConfig("homeserver", nil, nil)
	cfg.Core.LocalHost = "testhost"

	out := captureStdout(func() {
		_ = delegateToCentral(cfg, "docs", false, true)
	})

	if !strings.Contains(out, "--resync") {
		t.Errorf("expected --resync flag, got: %s", out)
	}
}

func TestDelegateToCentral_CentralAddress(t *testing.T) {
	cfg := testConfig("homeserver", nil, nil)
	cfg.Core.CentralAddress = "100.64.0.5"
	cfg.Core.LocalHost = "testhost"

	out := captureStdout(func() {
		_ = delegateToCentral(cfg, "docs", false, false)
	})

	if !strings.Contains(out, "ssh 100.64.0.5 vv updates") {
		t.Errorf("expected CentralAddress (100.64.0.5) in SSH command, got: %s", out)
	}
}

func TestDelegateToCentral_NoCentralHost(t *testing.T) {
	cfg := testConfig("", nil, nil)

	err := delegateToCentral(cfg, "docs", false, false)
	if err == nil {
		t.Error("expected error when central_host is empty")
	}
}

// ---------------------------------------------------------------------------
// syncAll — host collection
// ---------------------------------------------------------------------------

func TestSyncAll_NoSubscriptions(t *testing.T) {
	cfg := testConfig("", nil, nil)

	out := captureStdout(func() {
		_ = syncAll(cfg, "", false)
	})

	if !strings.Contains(out, "No subscribed hosts") {
		t.Errorf("expected 'No subscribed hosts' message, got: %s", out)
	}
}

func TestSyncAll_CollectsUniqueHosts(t *testing.T) {
	cfg := testConfig("", []config.VaultConfig{
		{Name: "a", Path: "/home/user/vaults/a"},
		{Name: "b", Path: "/home/user/vaults/b"},
	}, []config.Subscription{
		{Host: "laptop", Vaults: []string{"a"}},
		{Host: "laptop", Vaults: []string{"b"}}, // duplicate host, different vault
		{Host: "desktop", Vaults: []string{"a"}},
	})

	out := captureStdout(func() {
		_ = syncAll(cfg, "", false)
	})

	// Should sync with 2 unique hosts, not 3.
	if !strings.Contains(out, "Syncing with 2 host(s)") {
		t.Errorf("expected 2 unique hosts, got: %s", out)
	}
	if !strings.Contains(out, "── laptop ──") {
		t.Errorf("expected laptop section, got: %s", out)
	}
	if !strings.Contains(out, "── desktop ──") {
		t.Errorf("expected desktop section, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// runUpdates — vault scope and propagation
// ---------------------------------------------------------------------------

func TestRunUpdates_SpecificVault(t *testing.T) {
	cfg := testConfig("", []config.VaultConfig{
		{Name: "a", Path: "/home/user/vaults/a"},
		{Name: "b", Path: "/home/user/vaults/b"},
	}, []config.Subscription{
		{Host: "laptop", Vaults: []string{"a", "b"}},
	})

	out := captureStdout(func() {
		_ = runUpdates(cfg, "laptop", "a", true, false)
	})

	// Should only sync vault "a", not "b".
	if strings.Contains(out, `Syncing vault "b"`) {
		t.Errorf("should not sync vault b when a is specified, got: %s", out)
	}
	if !strings.Contains(out, `Syncing vault "a"`) {
		t.Errorf("expected sync of vault a, got: %s", out)
	}
}

func TestRunUpdates_AllVaults(t *testing.T) {
	cfg := testConfig("", []config.VaultConfig{
		{Name: "a", Path: "/home/user/vaults/a"},
		{Name: "b", Path: "/home/user/vaults/b"},
	}, []config.Subscription{
		{Host: "laptop", Vaults: []string{"a", "b"}},
	})

	out := captureStdout(func() {
		_ = runUpdates(cfg, "laptop", "", true, false)
	})

	if !strings.Contains(out, `Syncing vault "a"`) {
		t.Errorf("expected sync of vault a, got: %s", out)
	}
	if !strings.Contains(out, `Syncing vault "b"`) {
		t.Errorf("expected sync of vault b, got: %s", out)
	}
}

func TestRunUpdates_NoPropagate(t *testing.T) {
	cfg := testConfig("", []config.VaultConfig{
		{Name: "shared", Path: "/home/user/vaults/shared"},
	}, []config.Subscription{
		{Host: "laptop", Vaults: []string{"shared"}},
		{Host: "desktop", Vaults: []string{"shared"}},
	})

	out := captureStdout(func() {
		_ = runUpdates(cfg, "laptop", "", true, false)
	})

	// With noPropagate=true, should NOT see "Propagating" to desktop.
	if strings.Contains(out, "Propagating") {
		t.Errorf("did not expect propagation with noPropagate=true, got: %s", out)
	}
}

func TestRunUpdates_WithPropagation(t *testing.T) {
	cfg := testConfig("", []config.VaultConfig{
		{Name: "shared", Path: "/home/user/vaults/shared"},
	}, []config.Subscription{
		{Host: "laptop", Vaults: []string{"shared"}},
		{Host: "desktop", Vaults: []string{"shared"}},
	})

	out := captureStdout(func() {
		_ = runUpdates(cfg, "laptop", "", false, false)
	})

	// With noPropagate=false, should propagate to desktop.
	if !strings.Contains(out, "Propagating") {
		t.Errorf("expected propagation to desktop, got: %s", out)
	}
}

func TestRunUpdates_NoVaultsForHost(t *testing.T) {
	cfg := testConfig("", nil, []config.Subscription{
		{Host: "laptop", Vaults: []string{}},
	})

	out := captureStdout(func() {
		_ = runUpdates(cfg, "laptop", "", false, false)
	})

	if !strings.Contains(out, "No vaults to sync") {
		t.Errorf("expected 'No vaults to sync' message, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// vaultList
// ---------------------------------------------------------------------------

func TestVaultList_SpecificVault(t *testing.T) {
	cfg := testConfig("", nil, []config.Subscription{
		{Host: "laptop", Vaults: []string{"a", "b"}},
	})

	result := vaultList(cfg, "laptop", "a")
	if len(result) != 1 || result[0] != "a" {
		t.Errorf("expected [a], got %v", result)
	}
}

func TestVaultList_AllVaults(t *testing.T) {
	cfg := testConfig("", nil, []config.Subscription{
		{Host: "laptop", Vaults: []string{"a", "b"}},
	})

	result := vaultList(cfg, "laptop", "")
	if len(result) != 2 {
		t.Errorf("expected 2 vaults, got %d: %v", len(result), result)
	}
}

func TestVaultList_UnsubscribedHost(t *testing.T) {
	cfg := testConfig("", nil, nil)

	result := vaultList(cfg, "ghost", "")
	if result != nil {
		t.Errorf("expected nil for unsubscribed host, got %v", result)
	}
}