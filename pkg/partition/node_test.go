package partition

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureNodeReturnsExistingNode(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "sda")
	if err := os.WriteFile(filepath.Join(dir, "sda1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	node, err := EnsureNode(disk, 1, NodeOptions{Wait: 10 * time.Millisecond, Poll: time.Millisecond})
	if err != nil || node != filepath.Join(dir, "sda1") {
		t.Fatalf("EnsureNode() = %q, %v", node, err)
	}
}

func TestEnsureNodeWaitsForTheNode(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "nvme0n1")
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(dir, "nvme0n1p1"), nil, 0o600)
	}()

	node, err := EnsureNode(disk, 1, NodeOptions{Wait: 2 * time.Second, Poll: 5 * time.Millisecond})
	if err != nil || node != filepath.Join(dir, "nvme0n1p1") {
		t.Fatalf("EnsureNode() = %q, %v", node, err)
	}
}

func TestEnsureNodeFallsBackToMknodFromSysfs(t *testing.T) {
	dir := t.TempDir()
	devDir := filepath.Join(dir, "dev")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(devDir, "sda")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(devDir, "by-id-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	sysRoot := filepath.Join(dir, "sys")
	if err := os.MkdirAll(filepath.Join(sysRoot, "block", "sda", "sda1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysRoot, "block", "sda", "sda1", "dev"), []byte("259:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotPath string
	var gotMajor, gotMinor uint32
	opts := NodeOptions{
		SysRoot:    sysRoot,
		Wait:       10 * time.Millisecond,
		Poll:       time.Millisecond,
		PrivateDir: filepath.Join(dir, "nodes"),
		Mknod: func(path string, major, minor uint32) error {
			gotPath, gotMajor, gotMinor = path, major, minor
			return os.WriteFile(path, nil, 0o600)
		},
	}

	node, err := EnsureNode(link, 1, opts)
	if err != nil {
		t.Fatalf("EnsureNode() error: %v", err)
	}
	if node != filepath.Join(dir, "nodes", "sda1") || gotPath != node || gotMajor != 259 || gotMinor != 1 {
		t.Fatalf("node=%q path=%q major=%d minor=%d", node, gotPath, gotMajor, gotMinor)
	}
}

func TestEnsureNodeErrorsWithoutSysfsEntry(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "sda")
	if err := os.WriteFile(disk, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := EnsureNode(disk, 1, NodeOptions{SysRoot: t.TempDir(), Wait: 5 * time.Millisecond, Poll: time.Millisecond, PrivateDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error when neither the node nor sysfs is available")
	}
}
