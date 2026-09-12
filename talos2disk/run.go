package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sidero-community/actions/pkg/cmdline"
	"github.com/sidero-community/actions/pkg/disks"
	"github.com/sidero-community/actions/pkg/factory"
	"github.com/sidero-community/actions/pkg/hardware"
	"github.com/sidero-community/actions/pkg/image"
	"github.com/sidero-community/actions/pkg/meta"
	"github.com/sidero-community/actions/pkg/partition"
	"github.com/sidero-community/actions/pkg/talosconfig"
	"github.com/sidero-community/actions/pkg/talosnet"
)

const (
	efiLabel   = "EFI"
	apiTimeout = 60 * time.Second
)

// Plan is everything decided before the first byte is written.
type Plan struct {
	Hardware    *hardware.Hardware
	Settings    Settings
	Disk        disks.Disk
	Candidates  []disks.Disk
	SchematicID string
	Version     string
	ImageURL    string
	KernelArgs  string
	KernelNames bool
	NetworkDoc  []byte
}

func run(ctx context.Context, in Inputs, deps Deps) error {
	logger := deps.Logger

	if strings.TrimSpace(in.Hardware) == "" {
		return errors.New("no Hardware object specified with environment variable [HARDWARE]")
	}

	hw, err := hardware.Parse([]byte(in.Hardware))
	if err != nil {
		return fmt.Errorf("HARDWARE: %w", err)
	}

	cfg, err := talosconfig.Parse(hw.UserData())
	if err != nil {
		return fmt.Errorf("spec.userData: %w", err)
	}

	settings, err := resolveSettings(in, hw, cfg)
	if err != nil {
		return err
	}

	if in.NetworkConfig != "" {
		if _, err := talosnet.Parse([]byte(in.NetworkConfig)); err != nil {
			return fmt.Errorf("NETWORK_CONFIG: %w", err)
		}
	}

	p, err := buildPlan(ctx, in, deps, hw, settings)
	if err != nil {
		return err
	}

	logPlan(logger, in, p)

	if in.DryRun {
		logger.Info("DRY_RUN is set; leaving the disk untouched")

		return nil
	}

	return execute(ctx, in, deps, p)
}

func buildPlan(ctx context.Context, in Inputs, deps Deps, hw *hardware.Hardware, settings Settings) (*Plan, error) {
	logger := deps.Logger
	p := &Plan{Hardware: hw, Settings: settings, SchematicID: settings.SchematicID}

	enum, err := disks.Enumerate(deps.SysRoot, deps.DevRoot, deps.Prober)
	if err != nil {
		return nil, err
	}

	for name, err := range enum.Skipped {
		logger.Warn("Skipping block device that could not be probed", "device", name, "error", err)
	}

	for _, d := range enum.Disks {
		logger.Info("Found disk", "device", d.DevPath, "size", d.Size, "transport", d.Transport, "rotational", d.Rotational,
			"readonly", d.Readonly, "cdrom", d.CDROM, "model", d.Model, "serial", d.Serial, "wwid", d.WWID, "busPath", d.BusPath, "eligible", disks.Eligible(d))
	}

	if settings.Disk.Path != "" {
		p.Disk, err = disks.SelectPath(enum.Disks, settings.Disk.Path)
		if err != nil {
			return nil, fmt.Errorf("install disk from %s: %w", settings.Disk.Source, err)
		}

		p.Candidates = []disks.Disk{p.Disk}
	} else {
		p.Disk, p.Candidates, err = disks.Select(enum.Disks, settings.Disk.Selector)
		if err != nil {
			return nil, fmt.Errorf("install disk from %s: %w", settings.Disk.Source, err)
		}
	}

	if len(p.Candidates) > 1 {
		logger.Warn("Several disks match the selector; taking the first by device name as Talos does",
			"selector", settings.Disk.Selector.String(), "candidates", diskPaths(p.Candidates), "chosen", p.Disk.DevPath)
	}

	warnUnknownDisk(logger, hw, p.Disk)

	client, err := factory.NewClient(settings.FactoryURL, deps.HTTP)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", settings.FactoryURLSource, err)
	}

	p.Version = settings.Version
	if factory.IsBareMinor(p.Version) {
		apiCtx, cancel := context.WithTimeout(ctx, apiTimeout)
		defer cancel()

		versions, err := client.Versions(apiCtx)
		if err != nil {
			return nil, err
		}

		p.Version, err = factory.ResolveVersion(p.Version, versions)
		if err != nil {
			return nil, err
		}
	}

	p.ImageURL = client.ImageURL(p.SchematicID, p.Version, deps.Arch)

	p.KernelArgs = settings.KernelArgs
	p.KernelNames = slices.Contains(strings.Fields(p.KernelArgs), "net.ifnames=0")

	switch in.LinkNaming {
	case "kernel":
		p.KernelNames = true
	case "predictable":
		p.KernelNames = false
	}

	switch {
	case in.NetworkConfig != "":
		p.NetworkDoc = []byte(in.NetworkConfig)
	case talosnet.HasStaticAddress(&hw.Spec):
		netCfg, err := talosnet.FromHardwareWithOverrides(&hw.Spec, deps.Namer(p.KernelNames), settings.Network)
		if err != nil {
			return nil, fmt.Errorf("building network configuration: %w", err)
		}

		p.NetworkDoc, err = netCfg.Marshal()
		if err != nil {
			return nil, err
		}
	default:
		logger.Warn("No interface has a static address and NETWORK_CONFIG is unset; META network configuration will be skipped")
	}

	return p, nil
}

func execute(ctx context.Context, in Inputs, deps Deps, p *Plan) error {
	logger := deps.Logger
	device := p.Disk.DevPath

	logger.Info("Writing image", "url", p.ImageURL, "device", device)

	err := image.Retry(ctx, in.RetryWindow, logger, func() error {
		_, err := image.Write(ctx, deps.HTTP, p.ImageURL, device, image.Options{Logger: logger})

		return err
	})
	if err != nil {
		return fmt.Errorf("writing image: %w", err)
	}

	efi, err := partition.Locate(device, efiLabel)
	if err != nil {
		return fmt.Errorf("after writing the image: %w", err)
	}

	if _, err := meta.Locate(device); err != nil {
		return fmt.Errorf("after writing the image: %w", err)
	}

	if p.KernelArgs != "" {
		node, err := partition.EnsureNode(device, efi.Index, deps.Nodes)
		if err != nil {
			return err
		}

		res, err := cmdline.Apply(node, p.KernelArgs, cmdline.Options{StripSignature: in.StripSignature, Mounter: deps.Mounter, MountPoint: deps.MountPoint, Logger: logger})
		if err != nil {
			return fmt.Errorf("setting kernel arguments: %w", err)
		}

		logger.Info("Kernel arguments applied", "updated", res.Updated, "unchanged", res.Unchanged)

		for name, lines := range res.Cmdlines {
			logger.Info("Command line", "uki", name, "cmdlines", lines)
		}
	}

	if len(p.NetworkDoc) > 0 {
		if err := meta.WriteTag(device, meta.MetalNetworkPlatformConfig, p.NetworkDoc); err != nil {
			return fmt.Errorf("writing network configuration to META: %w", err)
		}

		logger.Info("Wrote network configuration to META", "key", fmt.Sprintf("%#x", meta.MetalNetworkPlatformConfig), "bytes", len(p.NetworkDoc))
		logger.Info("Network configuration:\n" + string(p.NetworkDoc))
	}

	logger.Info("Talos installed", "device", device, "schematic", p.SchematicID, "version", p.Version, "image", p.ImageURL)

	return nil
}

// logPlan reports every decision. It never logs userData.
func logPlan(logger *slog.Logger, in Inputs, p *Plan) {
	s := p.Settings

	selector := ""
	if s.Disk.Selector != nil {
		selector = s.Disk.Selector.String()
	}

	linkNaming := "predictable"
	if p.KernelNames {
		linkNaming = "kernel"
	}

	logger.Info("Plan: install disk", "device", p.Disk.DevPath, "source", s.Disk.Source, "selector", selector, "path", s.Disk.Path, "candidates", diskPaths(p.Candidates))
	logger.Info("Plan: schematic", "id", p.SchematicID, "source", s.SchematicSource, "factory", s.FactoryURL, "factorySource", s.FactoryURLSource)
	logger.Info("Plan: version", "version", p.Version, "source", s.VersionSource, "image", p.ImageURL)
	logger.Info("Plan: kernel arguments", "args", p.KernelArgs, "linkNaming", linkNaming)
	logger.Info("Plan: META network configuration", "write", len(p.NetworkDoc) > 0, "verbatim", in.NetworkConfig != "")
}

func warnUnknownDisk(logger *slog.Logger, hw *hardware.Hardware, d disks.Disk) {
	declared, known := 0, false

	for _, hd := range hw.Spec.Disks {
		if hd.Device == "" {
			continue
		}

		declared++

		resolved := hd.Device
		if r, err := filepath.EvalSymlinks(hd.Device); err == nil {
			resolved = r
		}

		if resolved == d.DevPath || hd.Device == d.DevPath {
			known = true
		}
	}

	if declared > 0 && !known {
		logger.Warn("Selected disk is not listed in Hardware spec.disks", "device", d.DevPath)
	}

	if inv := hw.OutOfBand(); inv != nil && len(inv.BlockDevices) > 0 && d.Serial != "" {
		for _, b := range inv.BlockDevices {
			if b.SerialNumber == d.Serial {
				return
			}
		}

		logger.Warn("Selected disk serial is not in the out-of-band inventory", "device", d.DevPath, "serial", d.Serial)
	}
}

func diskPaths(list []disks.Disk) []string {
	out := make([]string, 0, len(list))
	for _, d := range list {
		out = append(out, d.DevPath)
	}

	return out
}
