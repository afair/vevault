package vault

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
	if err := os.MkdirAll(cfg.Core.VaultsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return cfg, home
}

// --- Create -----------------------------------------------------------

func TestCreate_DefaultPath(t *testing.T) {
	cfg, home := setup(t)

	cmd := newCreateCmd(cfg)
	cmd.SetArgs([]string{"mydocs"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	v := cfg.Vault("mydocs")
	if v == nil {
		t.Fatal("vault not found in config")
	}
	expected := filepath.Join(home, "vaults", "mydocs")
	if v.Path != expected {
		t.Errorf("Path = %q, want %q", v.Path, expected)
	}
	if _, err := os.Stat(expected); os.IsNotExist(err) {
		t.Error("vault directory was not created")
	}
}

func TestCreate_CustomPath(t *testing.T) {
	cfg, home := setup(t)
	custom := filepath.Join(home, "somewhere", "else")

	cmd := newCreateCmd(cfg)
	cmd.SetArgs([]string{"customvault", "--path", custom})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	v := cfg.Vault("customvault")
	if v.Path != custom {
		t.Errorf("Path = %q, want %q", v.Path, custom)
	}
	if _, err := os.Stat(custom); os.IsNotExist(err) {
		t.Error("custom vault directory was not created")
	}
}

func TestCreate_WithSymlink(t *testing.T) {
	cfg, home := setup(t)
	linkTarget := filepath.Join(home, "my-link")

	cmd := newCreateCmd(cfg)
	cmd.SetArgs([]string{"linked", "--symlink", linkTarget})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	fi, err := os.Lstat(linkTarget)
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("created path is not a symlink")
	}

	v := cfg.Vault("linked")
	if len(v.Symlinks) != 1 || v.Symlinks[0] != linkTarget {
		t.Errorf("Symlinks = %v, want [%s]", v.Symlinks, linkTarget)
	}
}

func TestCreate_Duplicate(t *testing.T) {
	cfg, _ := setup(t)

	cmd := newCreateCmd(cfg)
	cmd.SetArgs([]string{"dup"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Second create should fail.
	cmd2 := newCreateCmd(cfg)
	cmd2.SetArgs([]string{"dup"})
	if err := cmd2.Execute(); err == nil {
		t.Fatal("expected error for duplicate vault name")
	}
}

// --- Delete -----------------------------------------------------------

func TestDelete_WithoutConfirmation(t *testing.T) {
	cfg, _ := setup(t)

	// Create a vault first.
	create := newCreateCmd(cfg)
	create.SetArgs([]string{"temp"})
	if err := create.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Delete without --yes-im-sure should no-op.
	del := newDeleteCmd(cfg)
	del.SetArgs([]string{"temp"})
	if err := del.Execute(); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Vault should still exist.
	if cfg.Vault("temp") == nil {
		t.Error("vault should still exist after unconfirmed delete")
	}
}

func TestDelete_Confirmed(t *testing.T) {
	cfg, _ := setup(t)

	create := newCreateCmd(cfg)
	create.SetArgs([]string{"temp"})
	if err := create.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}

	del := newDeleteCmd(cfg)
	del.SetArgs([]string{"temp", "--yes-im-sure"})
	if err := del.Execute(); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if cfg.Vault("temp") != nil {
		t.Error("vault should be removed from config")
	}
}

func TestDelete_WithData(t *testing.T) {
	cfg, _ := setup(t)

	create := newCreateCmd(cfg)
	create.SetArgs([]string{"temp"})
	if err := create.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}

	vaultPath := cfg.VaultPath("temp")
	// Write a file so we can verify removal.
	if err := os.WriteFile(filepath.Join(vaultPath, "test.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	del := newDeleteCmd(cfg)
	del.SetArgs([]string{"temp", "--yes-im-sure", "--delete-data"})
	if err := del.Execute(); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := os.Stat(vaultPath); !os.IsNotExist(err) {
		t.Error("vault data directory should be removed")
	}
}

func TestDelete_CleansUpSymlink(t *testing.T) {
	cfg, home := setup(t)
	linkTarget := filepath.Join(home, "dead-link")

	create := newCreateCmd(cfg)
	create.SetArgs([]string{"temp", "--symlink", linkTarget})
	if err := create.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Lstat(linkTarget); err != nil {
		t.Fatalf("symlink should exist: %v", err)
	}

	del := newDeleteCmd(cfg)
	del.SetArgs([]string{"temp", "--yes-im-sure"})
	if err := del.Execute(); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := os.Lstat(linkTarget); !os.IsNotExist(err) {
		t.Error("symlink should be removed on delete")
	}
}

func TestDelete_Nonexistent(t *testing.T) {
	cfg, _ := setup(t)

	del := newDeleteCmd(cfg)
	del.SetArgs([]string{"ghost", "--yes-im-sure"})
	if err := del.Execute(); err == nil {
		t.Fatal("expected error for nonexistent vault")
	}
}

// --- List -------------------------------------------------------------

func TestList_Empty(t *testing.T) {
	cfg, _ := setup(t)

	cmd := newListCmd(cfg)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	// No assertions on stdout — just verify no panic.
}

func TestList_WithVaults(t *testing.T) {
	cfg, _ := setup(t)

	cfg.Vaults = []config.VaultConfig{
		{Name: "a", Path: "/tmp/a"},
		{Name: "b", Path: "/tmp/b", Encryption: config.EncryptionConfig{Enabled: true}},
	}

	cmd := newListCmd(cfg)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
}

// --- Info -------------------------------------------------------------

func TestInfo(t *testing.T) {
	cfg, _ := setup(t)

	cfg.Vaults = []config.VaultConfig{
		{Name: "docs", Path: "/tmp/docs", Symlinks: []string{"/home/link"}},
	}

	cmd := newInfoCmd(cfg)
	cmd.SetArgs([]string{"docs"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("info: %v", err)
	}
}

func TestInfo_Nonexistent(t *testing.T) {
	cfg, _ := setup(t)

	cmd := newInfoCmd(cfg)
	cmd.SetArgs([]string{"nope"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unknown vault")
	}
}

// --- Helpers ----------------------------------------------------------

func TestCreateSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "link")
	source := filepath.Join(dir, "real")

	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(target, source); err != nil {
		t.Fatalf("createSymlink: %v", err)
	}

	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("stat symlink: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("not a symlink")
	}
}

func TestCreateSymlink_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "link")
	source := filepath.Join(dir, "real")

	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(target, source); err != nil {
		t.Fatal(err)
	}
	// Second attempt should fail.
	if err := createSymlink(target, source); err == nil {
		t.Fatal("expected error for existing symlink target")
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := dirSize(dir); s != 10 {
		t.Errorf("dirSize = %d, want 10", s)
	}
}

func TestDirSize_Empty(t *testing.T) {
	dir := t.TempDir()
	if s := dirSize(dir); s != 0 {
		t.Errorf("dirSize = %d, want 0", s)
	}
}

func TestCountFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "1"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "2"), []byte(""), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "3"), []byte(""), 0o644)

	if n := countFiles(dir); n != 3 {
		t.Errorf("countFiles = %d, want 3", n)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.n); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}