package disks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeProber map[string]Disk

func (f fakeProber) Probe(devPath string) (Disk, error) {
	d, ok := f[filepath.Base(devPath)]
	if !ok {
		return Disk{}, errors.New("probe failed")
	}
	return d, nil
}

func TestEnumerateListsSysBlockAndSortsByName(t *testing.T) {
	sysRoot := t.TempDir()
	for _, name := range []string{"sdb", "nvme0n1", "loop0", "sda", "broken"} {
		if err := os.MkdirAll(filepath.Join(sysRoot, "block", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	devRoot := "/dev"
	prober := fakeProber{
		"sda":     {Size: 100, Transport: "sata"},
		"sdb":     {Size: 200, Transport: "sata"},
		"nvme0n1": {Size: 300, Transport: "nvme"},
		"loop0":   {Size: 400},
	}

	enum, err := Enumerate(sysRoot, devRoot, prober)
	if err != nil {
		t.Fatalf("Enumerate() error: %v", err)
	}
	if len(enum.Disks) != 4 {
		t.Fatalf("expected 4 disks, got %+v", enum.Disks)
	}
	for i, want := range []string{"loop0", "nvme0n1", "sda", "sdb"} {
		if enum.Disks[i].Name != want || enum.Disks[i].DevPath != filepath.Join(devRoot, want) {
			t.Errorf("disk %d = %+v, want name %s", i, enum.Disks[i], want)
		}
	}
	if _, skipped := enum.Skipped["broken"]; !skipped || len(enum.Skipped) != 1 {
		t.Fatalf("expected only 'broken' to be skipped, got %v", enum.Skipped)
	}
	if got := Transports(enum.Disks); len(got) != 2 || got[0] != "nvme" || got[1] != "sata" {
		t.Fatalf("Transports() = %v", got)
	}
}

func TestEnumerateFailsWithoutSysBlock(t *testing.T) {
	if _, err := Enumerate(t.TempDir(), "/dev", fakeProber{}); err == nil {
		t.Fatal("expected an error when /sys/block is missing")
	}
}
