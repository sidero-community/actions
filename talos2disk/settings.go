package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sidero-community/actions/pkg/detect"
	"github.com/sidero-community/actions/pkg/disks"
	"github.com/sidero-community/actions/pkg/factory"
	"github.com/sidero-community/actions/pkg/hardware"
	"github.com/sidero-community/actions/pkg/kargs"
	"github.com/sidero-community/actions/pkg/talosconfig"
	"github.com/sidero-community/actions/pkg/talosnet"
)

// DiskChoice is how the install disk is to be chosen: by selector or by path.
type DiskChoice struct {
	Selector *disks.Selector
	Path     string
	// Source names the layer the choice came from, for logging.
	Source string
}

// Settings are the resolved inputs of the install after the fall-through:
// environment, then the machine configuration, then the Hardware, then defaults.
type Settings struct {
	Disk          DiskChoice
	Version       string
	VersionSource string
	// KernelArgs is the config's extraKernelArgs with KERNEL_ARGS merged on top.
	KernelArgs string
	// Extensions are the explicit extensions: annotation plus EXTENSIONS.
	Extensions []string
	// NVIDIA are the extensions added when an NVIDIA GPU is detected.
	NVIDIA  []string
	Overlay *factory.Overlay
	Network talosnet.Overrides
}

func resolveSettings(in Inputs, hw *hardware.Hardware, cfg *talosconfig.Config) (Settings, error) {
	var s Settings

	disk, err := resolveDisk(in, hw, cfg)
	if err != nil {
		return Settings{}, err
	}

	s.Disk = disk

	s.Version, s.VersionSource, err = resolveVersion(in, hw, cfg)
	if err != nil {
		return Settings{}, err
	}

	var configArgs string
	if cfg != nil {
		configArgs = strings.Join(cfg.Install.ExtraKernelArgs, " ")
		s.Network = talosnet.Overrides{Hostname: cfg.Network.Hostname, Nameservers: cfg.Network.Nameservers, TimeServers: cfg.Time.Servers}
	}

	s.KernelArgs = kargs.Merge(configArgs, in.KernelArgs)

	s.Extensions = append(detect.SplitList(hw.Annotation(hardware.AnnotationExtensions)), detect.SplitList(in.Extensions)...)
	s.NVIDIA = detect.SplitList(in.NVIDIAExtensions)

	overlay := in.Overlay
	if overlay == "" {
		overlay = hw.Annotation(hardware.AnnotationOverlay)
	}

	s.Overlay, err = factory.ParseOverlay(overlay)
	if err != nil {
		return Settings{}, fmt.Errorf("overlay %q: %w", overlay, err)
	}

	return s, nil
}

func resolveDisk(in Inputs, hw *hardware.Hardware, cfg *talosconfig.Config) (DiskChoice, error) {
	switch {
	case in.DiskSelector != "":
		sel, err := disks.ParseSelector([]byte(in.DiskSelector))
		if err != nil {
			return DiskChoice{}, fmt.Errorf("DISK_SELECTOR: %w", err)
		}

		return DiskChoice{Selector: sel, Source: "DISK_SELECTOR"}, nil
	case cfg != nil && len(cfg.Install.DiskSelector) > 0:
		sel, err := disks.ParseSelector(cfg.Install.DiskSelector)
		if err != nil {
			return DiskChoice{}, fmt.Errorf("machine.install.diskSelector: %w", err)
		}

		return DiskChoice{Selector: sel, Source: "machine.install.diskSelector"}, nil
	case cfg.InstallDiskPath() != "":
		return DiskChoice{Path: cfg.InstallDiskPath(), Source: "machine.install.disk"}, nil
	case hw.FirstDisk() != "":
		return DiskChoice{Path: hw.FirstDisk(), Source: "spec.disks[0].device"}, nil
	default:
		return DiskChoice{Selector: disks.DefaultSelector(), Source: "default"}, nil
	}
}

func resolveVersion(in Inputs, hw *hardware.Hardware, cfg *talosconfig.Config) (string, string, error) {
	candidates := []struct{ value, source string }{
		{in.TalosVersion, "TALOS_VERSION"},
		{cfg.ImageTag(), "machine.install.image"},
		{hw.OperatingSystemVersion(), "metadata.instance.operating_system.version"},
		{hw.Annotation(hardware.AnnotationContract), hardware.AnnotationContract},
	}

	for _, c := range candidates {
		if c.value == "" {
			continue
		}

		if !factory.IsFullVersion(c.value) && !factory.IsBareMinor(c.value) {
			return "", "", fmt.Errorf("%s: %q is neither a full Talos version (v1.14.2) nor a bare minor (v1.14)", c.source, c.value)
		}

		return c.value, c.source, nil
	}

	return "", "", errors.New("no Talos version: set TALOS_VERSION, or provide machine.install.image, metadata.instance.operating_system.version or the talos.tinkerbell.org/contract annotation on the Hardware")
}
