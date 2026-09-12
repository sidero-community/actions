package cmdline

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/sidero-community/actions/pkg/uki"
	"github.com/sidero-community/actions/pkg/uki/ukitest"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newEFITree(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for _, name := range []string{"EFI/Linux/Talos-v1.14.0.efi", "EFI/Linux/Talos-v1.14.0~1.efi"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := ukitest.Write(path, ukitest.TalosLike("talos.platform=metal console=ttyS0"), ukitest.Options{}); err != nil {
			t.Fatal(err)
		}
	}
	bootloader := filepath.Join(root, "EFI/boot/BOOTX64.efi")
	if err := os.MkdirAll(filepath.Dir(bootloader), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bootloader, []byte("not a uki"), 0o644); err != nil {
		t.Fatal(err)
	}

	return root
}

func TestEditTreeUpdatesEveryMatchAndIsIdempotent(t *testing.T) {
	root := newEFITree(t)
	args := "talos.config=http://192.168.1.2:7080/2009-04-04/user-data"

	res, err := EditTree(root, DefaultPattern, args, false, quietLogger())
	if err != nil {
		t.Fatalf("EditTree() error: %v", err)
	}
	if res.Updated != 2 || res.Unchanged != 0 {
		t.Fatalf("expected 2 updated / 0 unchanged, got %d / %d", res.Updated, res.Unchanged)
	}

	want := []string{
		"talos.platform=metal console=ttyS0 " + args,
		"talos.platform=metal console=ttyS0" + ukitest.ResetSuffix + " " + args,
	}
	for _, name := range []string{"EFI/Linux/Talos-v1.14.0.efi", "EFI/Linux/Talos-v1.14.0~1.efi"} {
		info, err := uki.Inspect(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("Inspect(%s) error: %v", name, err)
		}
		if len(info.Cmdlines) != 2 || info.Cmdlines[0] != want[0] || info.Cmdlines[1] != want[1] {
			t.Fatalf("%s: unexpected cmdlines %q, want %q", name, info.Cmdlines, want)
		}
		got := res.Cmdlines[name]
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("%s: Result.Cmdlines = %q, want %q", name, got, want)
		}
	}

	if got, _ := os.ReadFile(filepath.Join(root, "EFI/boot/BOOTX64.efi")); string(got) != "not a uki" {
		t.Fatal("expected files outside the pattern to be untouched")
	}

	res, err = EditTree(root, DefaultPattern, args, false, quietLogger())
	if err != nil {
		t.Fatalf("second EditTree() error: %v", err)
	}
	if res.Updated != 0 || res.Unchanged != 2 {
		t.Fatalf("expected 0 updated / 2 unchanged on rerun, got %d / %d", res.Updated, res.Unchanged)
	}
	if got := res.Cmdlines["EFI/Linux/Talos-v1.14.0.efi"]; len(got) != 2 || got[0] != want[0] {
		t.Fatalf("unchanged UKIs must still report their cmdlines, got %q", got)
	}
}

func TestEditTreeFailsWithoutMatches(t *testing.T) {
	if _, err := EditTree(t.TempDir(), DefaultPattern, "a=1", false, quietLogger()); err == nil {
		t.Fatal("expected an error when no UKI matches")
	}
}

func TestEditTreeRefusesSignedUnlessStripping(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "EFI/Linux/Talos-v1.14.0.efi")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ukitest.Write(path, ukitest.TalosLike("a=1"), ukitest.Options{Signed: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := EditTree(root, DefaultPattern, "a=2", false, quietLogger()); !errors.Is(err, uki.ErrSigned) {
		t.Fatalf("expected ErrSigned, got %v", err)
	}

	res, err := EditTree(root, DefaultPattern, "a=2", true, quietLogger())
	if err != nil || res.Updated != 1 {
		t.Fatalf("expected the signed UKI to be rewritten with stripping, updated=%d err=%v", res.Updated, err)
	}
}

func TestValidatePattern(t *testing.T) {
	for _, bad := range []string{"/EFI/Linux/*.efi", "../EFI/*.efi", "EFI/../x"} {
		if err := ValidatePattern(bad); err == nil {
			t.Errorf("ValidatePattern(%q) accepted an unsafe pattern", bad)
		}
	}
	if err := ValidatePattern(DefaultPattern); err != nil {
		t.Errorf("ValidatePattern(default) = %v", err)
	}
}

// dirMounter "mounts" a prepared directory by symlinking it at the target.
type dirMounter struct {
	dir       string
	mounted   string
	unmounted string
	device    string
}

func (m *dirMounter) Mount(device, target string) error {
	m.device = device
	m.mounted = target
	return os.Symlink(m.dir, target)
}

func (m *dirMounter) Unmount(target string) error {
	m.unmounted = target
	return os.Remove(target)
}

func TestApplyMountsEditsAndUnmounts(t *testing.T) {
	tree := newEFITree(t)
	m := &dirMounter{dir: tree}
	mountPoint := filepath.Join(t.TempDir(), "mnt")

	res, err := Apply("/dev/fake1", "talos.config=http://x/user-data", Options{Mounter: m, MountPoint: mountPoint, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if res.Updated != 2 {
		t.Fatalf("expected 2 updated, got %d", res.Updated)
	}
	if m.device != "/dev/fake1" || m.mounted != mountPoint || m.unmounted != mountPoint {
		t.Fatalf("mounter calls: %+v", m)
	}
	if _, err := os.Lstat(mountPoint); !os.IsNotExist(err) {
		t.Fatal("expected the mount point to be released")
	}

	info, err := uki.Inspect(filepath.Join(tree, "EFI/Linux/Talos-v1.14.0.efi"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Cmdlines[0] != "talos.platform=metal console=ttyS0 talos.config=http://x/user-data" {
		t.Fatalf("unexpected cmdline %q", info.Cmdlines[0])
	}
}

func TestApplyRejectsEmptyArgsAndBadPattern(t *testing.T) {
	m := &dirMounter{dir: t.TempDir()}
	if _, err := Apply("/dev/fake1", "   ", Options{Mounter: m, MountPoint: filepath.Join(t.TempDir(), "mnt")}); err == nil {
		t.Fatal("expected an error for empty args")
	}
	if _, err := Apply("/dev/fake1", "a=1", Options{Mounter: m, MountPoint: filepath.Join(t.TempDir(), "mnt"), Pattern: "/abs"}); err == nil {
		t.Fatal("expected an error for an absolute pattern")
	}
	if m.mounted != "" {
		t.Fatal("nothing should be mounted when validation fails")
	}
}
