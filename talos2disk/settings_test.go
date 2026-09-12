package main

import (
	"strings"
	"testing"
	"time"

	"github.com/sidero-community/actions/pkg/hardware"
	"github.com/sidero-community/actions/pkg/talosconfig"
)

func hw(annotations map[string]string, disks ...string) *hardware.Hardware {
	h := &hardware.Hardware{Metadata: hardware.ObjectMeta{Name: "node1", Namespace: "tinkerbell", Annotations: annotations}}
	for _, d := range disks {
		h.Spec.Disks = append(h.Spec.Disks, hardware.Disk{Device: d})
	}
	return h
}

func TestDiskChain(t *testing.T) {
	cfgSelector := &talosconfig.Config{Found: true, Install: talosconfig.Install{DiskSelector: []byte("type: nvme"), Disk: "/dev/sdz"}}
	cfgPath := &talosconfig.Config{Found: true, Install: talosconfig.Install{Disk: "/dev/sdz"}}
	cfgPlaceholder := &talosconfig.Config{Found: true, Install: talosconfig.Install{Disk: "DISK_ID"}}

	tests := []struct {
		name       string
		in         Inputs
		hw         *hardware.Hardware
		cfg        *talosconfig.Config
		wantSel    string
		wantPath   string
		wantSource string
	}{
		{"env wins", Inputs{DiskSelector: `{"model": "X*"}`}, hw(nil, "/dev/sda"), cfgSelector, `{"model": "X*"}`, "", "DISK_SELECTOR"},
		{"config selector", Inputs{}, hw(nil, "/dev/sda"), cfgSelector, "type: nvme", "", "machine.install.diskSelector"},
		{"config path", Inputs{}, hw(nil, "/dev/sda"), cfgPath, "", "/dev/sdz", "machine.install.disk"},
		{"placeholder falls to hardware", Inputs{}, hw(nil, "/dev/sda"), cfgPlaceholder, "", "/dev/sda", "spec.disks[0].device"},
		{"no config falls to hardware", Inputs{}, hw(nil, "/dev/sda"), nil, "", "/dev/sda", "spec.disks[0].device"},
		{"default", Inputs{}, hw(nil), nil, `{ "size": ">= 100GB" }`, "", "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.TalosVersion = "v1.14.1"
			s, err := resolveSettings(tt.in, tt.hw, tt.cfg)
			if err != nil {
				t.Fatalf("resolveSettings() error: %v", err)
			}
			if s.Disk.Source != tt.wantSource || s.Disk.Path != tt.wantPath {
				t.Fatalf("disk = %+v", s.Disk)
			}
			if tt.wantSel != "" && (s.Disk.Selector == nil || s.Disk.Selector.String() != tt.wantSel) {
				t.Fatalf("selector = %v, want %q", s.Disk.Selector, tt.wantSel)
			}
			if tt.wantSel == "" && s.Disk.Selector != nil {
				t.Fatalf("expected no selector, got %v", s.Disk.Selector)
			}
		})
	}

	if _, err := resolveSettings(Inputs{TalosVersion: "v1.14.1", DiskSelector: `{"name": "sda"}`}, hw(nil), nil); err == nil {
		t.Fatal("expected an error for a name selector")
	}
}

func TestVersionChain(t *testing.T) {
	withOS := hw(map[string]string{hardware.AnnotationContract: "v1.13"})
	withOS.Spec.Metadata = &hardware.Metadata{Instance: &hardware.Instance{OperatingSystem: &hardware.OperatingSystem{Version: "v1.14.0"}}}
	cfg := &talosconfig.Config{Found: true, Install: talosconfig.Install{Image: "factory.talos.dev/metal-installer/abc:v1.14.2"}}

	tests := []struct {
		name, want, source string
		in                 Inputs
		hw                 *hardware.Hardware
		cfg                *talosconfig.Config
		hasErr             bool
	}{
		{"env", "v1.15", "TALOS_VERSION", Inputs{TalosVersion: "v1.15"}, withOS, cfg, false},
		{"config image tag", "v1.14.2", "machine.install.image", Inputs{}, withOS, cfg, false},
		{"operating_system", "v1.14.0", "metadata.instance.operating_system.version", Inputs{}, withOS, nil, false},
		{"contract", "v1.13", "talos.tinkerbell.org/contract", Inputs{}, hw(map[string]string{hardware.AnnotationContract: "v1.13"}), nil, false},
		{"nothing", "", "", Inputs{}, hw(nil), nil, true},
		{"garbage env", "", "", Inputs{TalosVersion: "latest"}, withOS, cfg, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := resolveSettings(tt.in, tt.hw, tt.cfg)
			if tt.hasErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSettings() error: %v", err)
			}
			if s.Version != tt.want || s.VersionSource != tt.source {
				t.Fatalf("version = %q from %q", s.Version, s.VersionSource)
			}
		})
	}
}

func TestKernelArgsExtensionsOverlayAndNetwork(t *testing.T) {
	cfg := &talosconfig.Config{
		Found:   true,
		Install: talosconfig.Install{ExtraKernelArgs: []string{"talos.logging.kernel=udp://1.2.3.4:514/", "net.ifnames=1"}},
		Network: talosconfig.Network{Hostname: "cfg-host", Nameservers: []string{"1.1.1.1"}},
		Time:    talosconfig.Time{Servers: []string{"time.example"}},
	}
	h := hw(map[string]string{
		hardware.AnnotationExtensions: "siderolabs/util-linux-tools, siderolabs/iscsi-tools",
		hardware.AnnotationOverlay:    "rpi_generic@ghcr.io/siderolabs/sbc-raspberrypi",
	})
	in := Inputs{TalosVersion: "v1.14.1", KernelArgs: "net.ifnames=0 talos.config=http://x/user-data", Extensions: "siderolabs/gvisor", NVIDIAExtensions: "a/b,c/d"}

	s, err := resolveSettings(in, h, cfg)
	if err != nil {
		t.Fatalf("resolveSettings() error: %v", err)
	}
	if s.KernelArgs != "talos.logging.kernel=udp://1.2.3.4:514/ net.ifnames=0 talos.config=http://x/user-data" {
		t.Errorf("KernelArgs = %q", s.KernelArgs)
	}
	if strings.Join(s.Extensions, ",") != "siderolabs/util-linux-tools,siderolabs/iscsi-tools,siderolabs/gvisor" {
		t.Errorf("Extensions = %v", s.Extensions)
	}
	if strings.Join(s.NVIDIA, ",") != "a/b,c/d" {
		t.Errorf("NVIDIA = %v", s.NVIDIA)
	}
	if s.Overlay == nil || s.Overlay.Name != "rpi_generic" {
		t.Errorf("Overlay = %+v", s.Overlay)
	}
	if s.Network.Hostname != "cfg-host" || strings.Join(s.Network.Nameservers, ",") != "1.1.1.1" || strings.Join(s.Network.TimeServers, ",") != "time.example" {
		t.Errorf("Network = %+v", s.Network)
	}

	in.Overlay = "other@img"
	s, err = resolveSettings(in, h, cfg)
	if err != nil || s.Overlay.Name != "other" {
		t.Fatalf("OVERLAY env must win, got %+v, %v", s.Overlay, err)
	}

	in.Overlay = "broken"
	if _, err := resolveSettings(in, h, cfg); err == nil {
		t.Fatal("expected an error for a malformed overlay")
	}
}

func TestInputsFromEnv(t *testing.T) {
	t.Setenv("HARDWARE", "{}")
	t.Setenv("FACTORY_URL", "")
	t.Setenv("RETRY_DURATION_MINUTES", "")
	t.Setenv("NVIDIA_EXTENSIONS", "")
	t.Setenv("LINK_NAMING", "")
	t.Setenv("DRY_RUN", "true")
	in, err := inputsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if in.FactoryURL != "https://factory.talos.dev" || in.RetryWindow != 10*time.Minute || !in.DryRun || in.NVIDIAExtensions != "" {
		t.Fatalf("inputs = %+v", in)
	}

	t.Setenv("RETRY_DURATION_MINUTES", "0")
	in, err = inputsFromEnv()
	if err != nil || in.RetryWindow != 0 {
		t.Fatalf("RETRY_DURATION_MINUTES=0 -> %v, %v", in.RetryWindow, err)
	}

	t.Setenv("RETRY_DURATION_MINUTES", "soon")
	if _, err := inputsFromEnv(); err == nil {
		t.Fatal("expected an error for a non-numeric retry duration")
	}
	t.Setenv("RETRY_DURATION_MINUTES", "")

	t.Setenv("LINK_NAMING", "random")
	if _, err := inputsFromEnv(); err == nil {
		t.Fatal("expected an error for an unknown LINK_NAMING")
	}
}
