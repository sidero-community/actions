package main

import (
	"strings"
	"testing"
	"time"

	"github.com/sidero-community/actions/pkg/hardware"
	"github.com/sidero-community/actions/pkg/talosconfig"
)

const (
	cfgID = "2222222222222222222222222222222222222222222222222222222222222222"
	envID = "3333333333333333333333333333333333333333333333333333333333333333"
	hwID  = "4444444444444444444444444444444444444444444444444444444444444444"
)

func hw(annotations map[string]string, disks ...string) *hardware.Hardware {
	h := &hardware.Hardware{Metadata: hardware.ObjectMeta{Name: "node1", Namespace: "tinkerbell", Annotations: annotations}}
	for _, d := range disks {
		h.Spec.Disks = append(h.Spec.Disks, hardware.Disk{Device: d})
	}
	return h
}

func withOS(h *hardware.Hardware, slug, version string) *hardware.Hardware {
	h.Spec.Metadata = &hardware.Metadata{Instance: &hardware.Instance{OperatingSystem: &hardware.OperatingSystem{Slug: slug, Version: version}}}
	return h
}

func cfgWithImage(image string) *talosconfig.Config {
	return &talosconfig.Config{Found: true, Install: talosconfig.Install{Image: image}}
}

func TestSchematicAndFactoryChain(t *testing.T) {
	cfg := cfgWithImage("factory.example.test:8443/metal-installer/" + cfgID + ":v1.14.1")

	tests := []struct {
		name                     string
		in                       Inputs
		hw                       *hardware.Hardware
		cfg                      *talosconfig.Config
		wantID, wantIDSource     string
		wantURL, wantURLSource   string
		wantVersion, wantVSource string
	}{
		{
			"env wins",
			Inputs{SchematicID: envID, FactoryURL: "http://mirror:8080/"},
			withOS(hw(nil), hwID, "v1.13.9"), cfg,
			envID, "SCHEMATIC_ID", "http://mirror:8080", "FACTORY_URL", "v1.14.1", "machine.install.image",
		},
		{
			"config reference",
			Inputs{},
			withOS(hw(nil), hwID, "v1.13.9"), cfg,
			cfgID, "machine.install.image", "https://factory.example.test:8443", "machine.install.image", "v1.14.1", "machine.install.image",
		},
		{
			"hardware fallback",
			Inputs{},
			withOS(hw(nil), hwID, "v1.13.9"), nil,
			hwID, "metadata.instance.operating_system.slug", "https://factory.talos.dev", "default", "v1.13.9", "metadata.instance.operating_system.version",
		},
		{
			"non-factory image keeps tag only",
			Inputs{},
			withOS(hw(nil), hwID, "v1.13.9"), cfgWithImage("ghcr.io/siderolabs/installer:v1.14.0"),
			hwID, "metadata.instance.operating_system.slug", "https://factory.talos.dev", "default", "v1.14.0", "machine.install.image",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := resolveSettings(tt.in, tt.hw, tt.cfg)
			if err != nil {
				t.Fatalf("resolveSettings() error: %v", err)
			}
			if s.SchematicID != tt.wantID || s.SchematicSource != tt.wantIDSource {
				t.Errorf("schematic = %q from %q", s.SchematicID, s.SchematicSource)
			}
			if s.FactoryURL != tt.wantURL || s.FactoryURLSource != tt.wantURLSource {
				t.Errorf("factory = %q from %q", s.FactoryURL, s.FactoryURLSource)
			}
			if s.Version != tt.wantVersion || s.VersionSource != tt.wantVSource {
				t.Errorf("version = %q from %q", s.Version, s.VersionSource)
			}
		})
	}

	if _, err := resolveSettings(Inputs{TalosVersion: "v1.14.1"}, hw(nil), nil); err == nil || !strings.Contains(err.Error(), "schematic") {
		t.Fatalf("expected a missing-schematic error, got %v", err)
	}
	if _, err := resolveSettings(Inputs{SchematicID: "not-hex"}, withOS(hw(nil), "", "v1.14.1"), nil); err == nil || !strings.Contains(err.Error(), "SCHEMATIC_ID") {
		t.Fatalf("expected a SCHEMATIC_ID format error, got %v", err)
	}
}

func TestDiskChain(t *testing.T) {
	base := withOS(hw(nil, "/dev/sda"), hwID, "v1.14.1")
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
		{"env wins", Inputs{DiskSelector: `{"model": "X*"}`}, base, cfgSelector, `{"model": "X*"}`, "", "DISK_SELECTOR"},
		{"config selector", Inputs{}, base, cfgSelector, "type: nvme", "", "machine.install.diskSelector"},
		{"config path", Inputs{}, base, cfgPath, "", "/dev/sdz", "machine.install.disk"},
		{"placeholder falls to hardware", Inputs{}, base, cfgPlaceholder, "", "/dev/sda", "spec.disks[0].device"},
		{"default", Inputs{}, withOS(hw(nil), hwID, "v1.14.1"), nil, `{ "size": ">= 100GB" }`, "", "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
		})
	}
}

func TestVersionChainAndKernelArgs(t *testing.T) {
	h := withOS(hw(map[string]string{hardware.AnnotationContract: "v1.13"}), hwID, "")
	cfg := &talosconfig.Config{
		Found:   true,
		Install: talosconfig.Install{ExtraKernelArgs: []string{"talos.logging.kernel=udp://1.2.3.4:514/", "net.ifnames=1"}},
		Network: talosconfig.Network{Hostname: "cfg-host", Nameservers: []string{"1.1.1.1"}},
		Time:    talosconfig.Time{Servers: []string{"time.example"}},
	}

	s, err := resolveSettings(Inputs{KernelArgs: "net.ifnames=0 talos.config=http://x/user-data"}, h, cfg)
	if err != nil {
		t.Fatalf("resolveSettings() error: %v", err)
	}
	if s.Version != "v1.13" || s.VersionSource != hardware.AnnotationContract {
		t.Errorf("version = %q from %q", s.Version, s.VersionSource)
	}
	if s.KernelArgs != "talos.logging.kernel=udp://1.2.3.4:514/ net.ifnames=0 talos.config=http://x/user-data" {
		t.Errorf("KernelArgs = %q", s.KernelArgs)
	}
	if s.Network.Hostname != "cfg-host" || strings.Join(s.Network.Nameservers, ",") != "1.1.1.1" || strings.Join(s.Network.TimeServers, ",") != "time.example" {
		t.Errorf("Network = %+v", s.Network)
	}

	if _, err := resolveSettings(Inputs{TalosVersion: "latest"}, h, cfg); err == nil {
		t.Fatal("expected an error for a malformed version")
	}
	if _, err := resolveSettings(Inputs{}, withOS(hw(nil), hwID, ""), nil); err == nil {
		t.Fatal("expected an error without any version")
	}
}

func TestInputsFromEnv(t *testing.T) {
	for _, key := range []string{"FACTORY_URL", "RETRY_DURATION_MINUTES", "LINK_NAMING", "SCHEMATIC_ID"} {
		t.Setenv(key, "")
	}
	t.Setenv("HARDWARE", "{}")
	t.Setenv("DRY_RUN", "true")
	in, err := inputsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if in.FactoryURL != "" || in.RetryWindow != 10*time.Minute || !in.DryRun {
		t.Fatalf("inputs = %+v", in)
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
