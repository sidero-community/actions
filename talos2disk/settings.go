package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

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
	Disk             DiskChoice
	SchematicID      string
	SchematicSource  string
	Version          string
	VersionSource    string
	FactoryURL       string
	FactoryURLSource string
	// KernelArgs is the config's extraKernelArgs with KERNEL_ARGS merged on top.
	KernelArgs string
	Network    talosnet.Overrides
}

var schematicID = regexp.MustCompile(`^[0-9a-f]{64}$`)

func resolveSettings(in Inputs, hw *hardware.Hardware, cfg *talosconfig.Config) (Settings, error) {
	var (
		s   Settings
		err error
	)

	if s.Disk, err = resolveDisk(in, hw, cfg); err != nil {
		return Settings{}, err
	}

	var ref factory.InstallerReference

	if cfg != nil {
		ref, _ = factory.ParseInstallerReference(cfg.Install.Image)
	}

	if s.SchematicID, s.SchematicSource, err = resolveSchematic(in, hw, ref); err != nil {
		return Settings{}, err
	}

	if s.Version, s.VersionSource, err = resolveVersion(in, hw, cfg); err != nil {
		return Settings{}, err
	}

	s.FactoryURL, s.FactoryURLSource = resolveFactoryURL(in, ref)

	var configArgs string
	if cfg != nil {
		configArgs = strings.Join(cfg.Install.ExtraKernelArgs, " ")
		s.Network = talosnet.Overrides{Hostname: cfg.Network.Hostname, Nameservers: cfg.Network.Nameservers, TimeServers: cfg.Time.Servers}
	}

	s.KernelArgs = kargs.Merge(configArgs, in.KernelArgs)

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

// resolveSchematic picks the schematic ID: SCHEMATIC_ID, the Factory installer reference in
// machine.install.image, or the operating_system slug a resolver wrote on the Hardware.
func resolveSchematic(in Inputs, hw *hardware.Hardware, ref factory.InstallerReference) (string, string, error) {
	if in.SchematicID != "" {
		if !schematicID.MatchString(in.SchematicID) {
			return "", "", fmt.Errorf("SCHEMATIC_ID %q is not a 64-character hexadecimal schematic ID", in.SchematicID)
		}

		return in.SchematicID, "SCHEMATIC_ID", nil
	}

	if ref.ID != "" {
		return ref.ID, "machine.install.image", nil
	}

	if slug := hw.OperatingSystemSlug(); slug != "" {
		return slug, "metadata.instance.operating_system.slug", nil
	}

	return "", "", errors.New("no schematic: set SCHEMATIC_ID, or provide a Factory installer reference in machine.install.image, or metadata.instance.operating_system.slug on the Hardware")
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

// resolveFactoryURL picks the Factory base URL: FACTORY_URL, the host of the installer
// reference over https, or the public Factory.
func resolveFactoryURL(in Inputs, ref factory.InstallerReference) (string, string) {
	if in.FactoryURL != "" {
		return strings.TrimRight(in.FactoryURL, "/"), "FACTORY_URL"
	}

	if ref.Host != "" {
		return "https://" + ref.Host, "machine.install.image"
	}

	return factory.DefaultURL, "default"
}
