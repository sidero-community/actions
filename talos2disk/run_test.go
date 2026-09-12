package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/partition/gpt"
	"github.com/klauspost/compress/zstd"
	"github.com/sidero-community/actions/pkg/disks"
	"github.com/sidero-community/actions/pkg/meta"
	"github.com/sidero-community/actions/pkg/partition"
	"github.com/sidero-community/actions/pkg/talosnet"
	"github.com/sidero-community/actions/pkg/uki"
	"github.com/sidero-community/actions/pkg/uki/ukitest"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// talosLikeImage builds an 8 MiB GPT image with EFI and META partitions.
func talosLikeImage(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image.raw")
	d, err := diskfs.Create(path, 8*1024*1024, diskfs.SectorSize512)
	if err != nil {
		t.Fatal(err)
	}
	table := &gpt.Table{
		Partitions: []*gpt.Partition{
			{Index: 1, Start: 2048, End: 4095, Type: gpt.EFISystemPartition, Name: "EFI"},
			{Index: 2, Start: 4096, End: 6143, Type: gpt.LinuxFilesystem, Name: "META"},
		},
		LogicalSectorSize: 512, PhysicalSectorSize: 512, ProtectiveMBR: true,
	}
	if err := d.Partition(table); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func zstdBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type recorder struct {
	mu        sync.Mutex
	posts     int
	imageGets []string
}

// newFactoryServer serves /versions and the raw image for any schematic ID at v1.14.1,
// and records every POST so tests can assert that the action registers nothing.
func newFactoryServer(t *testing.T, image []byte) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		switch {
		case r.Method == http.MethodPost:
			rec.posts++
			http.Error(w, "the action must not POST", http.StatusMethodNotAllowed)
		case r.Method == http.MethodGet && r.URL.Path == "/versions":
			_, _ = w.Write([]byte(`["v1.13.9","v1.14.0","v1.14.1"]`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/image/") && strings.HasSuffix(r.URL.Path, "/v1.14.1/metal-amd64.raw.zst"):
			rec.imageGets = append(rec.imageGets, r.URL.Path)
			_, _ = w.Write(image)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

type fakeProber map[string]disks.Disk

func (f fakeProber) Probe(devPath string) (disks.Disk, error) {
	d, ok := f[filepath.Base(devPath)]
	if !ok {
		return disks.Disk{}, errors.New("no such device")
	}
	return d, nil
}

type dirMounter struct{ dir string }

func (m dirMounter) Mount(_, target string) error { return os.Symlink(m.dir, target) }
func (m dirMounter) Unmount(target string) error  { return os.Remove(target) }

type fixture struct {
	root, devRoot, sysRoot, efiDir, diskFile string
	deps                                     Deps
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{root: root, devRoot: filepath.Join(root, "dev"), sysRoot: filepath.Join(root, "sys"), efiDir: filepath.Join(root, "efi")}
	for _, dir := range []string{
		f.devRoot,
		filepath.Join(f.sysRoot, "block", "sda", "sda1"),
		filepath.Join(f.sysRoot, "block", "loop0"),
		filepath.Join(f.efiDir, "EFI", "Linux"),
		filepath.Join(root, "nodes"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.diskFile = filepath.Join(f.devRoot, "sda")
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(f.diskFile, nil, 0o600))
	must(os.WriteFile(filepath.Join(f.sysRoot, "block", "sda", "sda1", "dev"), []byte("8:1\n"), 0o644))
	must(ukitest.Write(filepath.Join(f.efiDir, "EFI", "Linux", "Talos-v1.14.1.efi"), ukitest.TalosLike("talos.platform=metal console=ttyS0"), ukitest.Options{}))

	f.deps = Deps{
		Logger:     quietLogger(),
		HTTP:       http.DefaultClient,
		SysRoot:    f.sysRoot,
		DevRoot:    f.devRoot,
		Prober:     fakeProber{"sda": {Size: 500_000_000_000, Transport: "sata", Model: "TestDisk", Serial: "S1"}, "loop0": {Size: 1 << 40}},
		Mounter:    dirMounter{dir: f.efiDir},
		MountPoint: filepath.Join(root, "mnt"),
		Nodes: partition.NodeOptions{
			SysRoot: f.sysRoot, Wait: 10 * time.Millisecond, Poll: time.Millisecond, PrivateDir: filepath.Join(root, "nodes"),
			Mknod: func(path string, _, _ uint32) error { return os.WriteFile(path, nil, 0o600) },
		},
		Arch: "amd64",
		Namer: func(kernelNames bool) talosnet.Namer {
			return &talosnet.SysfsNamer{Root: f.sysRoot, Predictable: !kernelNames}
		},
	}
	return f
}

const machineConfig = `version: v1alpha1
machine:
  type: worker
  install:
    disk: DISK_ID
    image: factory.talos.dev/metal-installer/` + cfgID + `:v1.14.1
    extraKernelArgs:
      - talos.logging.kernel=udp://10.0.0.5:514/
  network:
    hostname: node1
cluster:
  clusterName: demo
`

func (f *fixture) hardwareJSON(t *testing.T, userData string, osSlug string) string {
	t.Helper()
	instance := map[string]any{"hostname": "inst"}
	if osSlug != "" {
		instance["operating_system"] = map[string]any{"slug": osSlug, "version": "v1.14"}
	}
	hw := map[string]any{
		"metadata": map[string]any{"name": "node1", "namespace": "tinkerbell"},
		"spec": map[string]any{
			"disks": []map[string]string{{"device": f.diskFile}},
			"interfaces": []map[string]any{{"dhcp": map[string]any{
				"mac": "52:54:00:12:34:01", "iface_name": "eth0", "hostname": "dhcp-name",
				"ip": map[string]any{"address": "10.0.80.10", "netmask": "255.255.255.0", "gateway": "10.0.80.1", "family": 4},
			}}},
			"metadata": map[string]any{"instance": instance},
		},
		"status": map[string]any{"attributes": map[string]any{"outOfBand": map[string]any{
			"blockDevices": []map[string]any{{"serialNumber": "S1", "model": "TestDisk"}},
		}}},
	}
	if userData != "" {
		hw["spec"].(map[string]any)["userData"] = userData
	}
	raw, err := json.Marshal(hw)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRunInstallsFromTheConfiguredSchematic(t *testing.T) {
	f := newFixture(t)
	image := talosLikeImage(t)
	srv, rec := newFactoryServer(t, zstdBytes(t, image))

	in := Inputs{
		Hardware:    f.hardwareJSON(t, machineConfig, ""),
		KernelArgs:  "net.ifnames=0 talos.config=http://10.0.0.1:7080/2009-04-04/user-data",
		FactoryURL:  srv.URL,
		RetryWindow: 0,
	}
	if err := run(context.Background(), in, f.deps); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	if rec.posts != 0 {
		t.Fatalf("the action must not register schematics, saw %d POSTs", rec.posts)
	}
	if len(rec.imageGets) != 1 || rec.imageGets[0] != "/image/"+cfgID+"/v1.14.1/metal-amd64.raw.zst" {
		t.Fatalf("image fetched from %v, want the configured schematic", rec.imageGets)
	}

	got, err := os.ReadFile(f.diskFile)
	if err != nil {
		t.Fatal(err)
	}
	// The META partition (sectors 4096-6143) is rewritten by the network
	// configuration step; everything else must match the image byte for byte.
	const metaStart, metaEnd = 4096 * 512, 6144 * 512
	if len(got) != len(image) || !bytes.Equal(got[:metaStart], image[:metaStart]) || !bytes.Equal(got[metaEnd:], image[metaEnd:]) {
		t.Fatal("disk content outside META differs from the Factory image")
	}

	info, err := uki.Inspect(filepath.Join(f.efiDir, "EFI", "Linux", "Talos-v1.14.1.efi"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Cmdlines[0] != "talos.platform=metal console=ttyS0 talos.logging.kernel=udp://10.0.0.5:514/ net.ifnames=0 talos.config=http://10.0.0.1:7080/2009-04-04/user-data" {
		t.Errorf("cmdline = %q", info.Cmdlines[0])
	}

	doc, ok, err := meta.ReadTag(f.diskFile, meta.MetalNetworkPlatformConfig)
	if err != nil || !ok {
		t.Fatalf("META tag: ok=%v err=%v", ok, err)
	}
	for _, want := range []string{"hostname: node1", "address: 10.0.80.10/24", "linkName: eth0", "gateway: 10.0.80.1"} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("META document lacks %q:\n%s", want, doc)
		}
	}
}

func TestRunSchematicIDEnvOverridesTheConfig(t *testing.T) {
	f := newFixture(t)
	srv, rec := newFactoryServer(t, zstdBytes(t, talosLikeImage(t)))

	in := Inputs{Hardware: f.hardwareJSON(t, machineConfig, ""), SchematicID: envID, FactoryURL: srv.URL}
	if err := run(context.Background(), in, f.deps); err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if len(rec.imageGets) != 1 || !strings.Contains(rec.imageGets[0], envID) {
		t.Fatalf("image fetched from %v, want SCHEMATIC_ID", rec.imageGets)
	}
}

func TestRunFallsBackToHardwareOperatingSystem(t *testing.T) {
	f := newFixture(t)
	srv, rec := newFactoryServer(t, zstdBytes(t, talosLikeImage(t)))

	in := Inputs{Hardware: f.hardwareJSON(t, "", hwID), FactoryURL: srv.URL}
	if err := run(context.Background(), in, f.deps); err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if len(rec.imageGets) != 1 || rec.imageGets[0] != "/image/"+hwID+"/v1.14.1/metal-amd64.raw.zst" {
		t.Fatalf("image fetched from %v, want the Hardware slug with the bare minor resolved to v1.14.1", rec.imageGets)
	}
}

func TestRunDryRunTouchesNothing(t *testing.T) {
	f := newFixture(t)
	srv, rec := newFactoryServer(t, zstdBytes(t, talosLikeImage(t)))

	in := Inputs{Hardware: f.hardwareJSON(t, machineConfig, ""), KernelArgs: "net.ifnames=0", FactoryURL: srv.URL, DryRun: true}
	if err := run(context.Background(), in, f.deps); err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if st, err := os.Stat(f.diskFile); err != nil || st.Size() != 0 {
		t.Fatalf("disk must stay empty, size=%d err=%v", st.Size(), err)
	}
	if len(rec.imageGets) != 0 || rec.posts != 0 {
		t.Fatalf("dry run fetched %v and posted %d times", rec.imageGets, rec.posts)
	}
}

func TestRunFailsBeforeWriteOnBadInput(t *testing.T) {
	f := newFixture(t)
	if err := run(context.Background(), Inputs{}, f.deps); err == nil || !strings.Contains(err.Error(), "HARDWARE") {
		t.Fatalf("expected a HARDWARE error, got %v", err)
	}
	if err := run(context.Background(), Inputs{Hardware: f.hardwareJSON(t, "machine: [broken", "")}, f.deps); err == nil || !strings.Contains(err.Error(), "userData") {
		t.Fatalf("expected a userData error, got %v", err)
	}
	if err := run(context.Background(), Inputs{Hardware: f.hardwareJSON(t, "", ""), TalosVersion: "v1.14.1"}, f.deps); err == nil || !strings.Contains(err.Error(), "schematic") {
		t.Fatalf("expected a missing-schematic error, got %v", err)
	}
	if err := run(context.Background(), Inputs{Hardware: f.hardwareJSON(t, "", hwID), TalosVersion: "v1.14.1", DiskSelector: `{"model": "nothing*"}`, FactoryURL: "http://127.0.0.1:9"}, f.deps); err == nil || !strings.Contains(err.Error(), "no disk matches") {
		t.Fatalf("expected a disk selection error before any network call, got %v", err)
	}
	if st, _ := os.Stat(f.diskFile); st.Size() != 0 {
		t.Fatal("disk must be untouched after early failures")
	}
}
