package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/sidero-community/actions/pkg/cmdline"
	"github.com/sidero-community/actions/pkg/detect"
	"github.com/sidero-community/actions/pkg/disks"
	"github.com/sidero-community/actions/pkg/kube"
	"github.com/sidero-community/actions/pkg/partition"
	"github.com/sidero-community/actions/pkg/talosnet"
)

// HardwarePatcher writes the install results back to the Hardware object.
type HardwarePatcher interface {
	PatchHardware(ctx context.Context, namespace, name string, patch []byte) error
}

// Deps are the environment-facing collaborators of the pipeline. Production
// values come from defaultDeps; tests substitute fakes.
type Deps struct {
	Logger     *slog.Logger
	HTTP       *http.Client
	SysRoot    string
	DevRoot    string
	ProcRoot   string
	Prober     disks.Prober
	Mounter    cmdline.Mounter
	MountPoint string
	Nodes      partition.NodeOptions
	Arch       string
	Namer      func(kernelNames bool) talosnet.Namer
	Kube       func(kubeconfig string) (HardwarePatcher, error)
}

func defaultDeps(logger *slog.Logger) Deps {
	return Deps{
		Logger:     logger,
		HTTP:       &http.Client{},
		SysRoot:    "/sys",
		DevRoot:    "/dev",
		ProcRoot:   "/proc",
		Prober:     disks.BlockdeviceProber{},
		Mounter:    cmdline.VFAT{},
		MountPoint: cmdline.DefaultMountPoint,
		Nodes:      partition.NodeOptions{},
		Arch:       detect.Arch(),
		Namer: func(kernelNames bool) talosnet.Namer {
			return &talosnet.SysfsNamer{Predictable: !kernelNames, Logger: logger}
		},
		Kube: func(path string) (HardwarePatcher, error) {
			return kube.NewClientFromKubeconfig(path)
		},
	}
}
