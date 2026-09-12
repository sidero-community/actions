package main

import (
	"log/slog"
	"net/http"
	"runtime"

	"github.com/sidero-community/actions/pkg/cmdline"
	"github.com/sidero-community/actions/pkg/disks"
	"github.com/sidero-community/actions/pkg/partition"
	"github.com/sidero-community/actions/pkg/talosnet"
)

// Deps are the environment-facing collaborators of the pipeline. Production
// values come from defaultDeps; tests substitute fakes.
type Deps struct {
	Logger     *slog.Logger
	HTTP       *http.Client
	SysRoot    string
	DevRoot    string
	Prober     disks.Prober
	Mounter    cmdline.Mounter
	MountPoint string
	Nodes      partition.NodeOptions
	Arch       string
	Namer      func(kernelNames bool) talosnet.Namer
}

func defaultDeps(logger *slog.Logger) Deps {
	return Deps{
		Logger:     logger,
		HTTP:       &http.Client{},
		SysRoot:    "/sys",
		DevRoot:    "/dev",
		Prober:     disks.BlockdeviceProber{},
		Mounter:    cmdline.VFAT{},
		MountPoint: cmdline.DefaultMountPoint,
		Nodes:      partition.NodeOptions{},
		Arch:       runtime.GOARCH,
		Namer: func(kernelNames bool) talosnet.Namer {
			return &talosnet.SysfsNamer{Predictable: !kernelNames, Logger: logger}
		},
	}
}
