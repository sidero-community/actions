// Package detect derives Image Factory extensions and machine configuration
// overrides from what the action can see on the machine, falling back to the
// out-of-band inventory on the Hardware object.
package detect

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/sidero-community/actions/pkg/hardware"
)

// Official extension names added by detection.
const (
	ExtNVMeCLI    = "siderolabs/nvme-cli"
	ExtIntelUcode = "siderolabs/intel-ucode"
	ExtAMDUcode   = "siderolabs/amd-ucode"
	ExtAMDGPU     = "siderolabs/amdgpu"
	ExtI915       = "siderolabs/i915"

	// DefaultNVIDIAExtensions is the NVIDIA_EXTENSIONS default: the open kernel
	// modules Talos documents for Turing and newer GPUs, plus the container toolkit.
	DefaultNVIDIAExtensions = "siderolabs/nvidia-open-gpu-kernel-modules-production,siderolabs/nvidia-container-toolkit-production"
)

// NVIDIAKernelModules are the machine.kernel.modules Talos needs for the NVIDIA extensions.
var NVIDIAKernelModules = []string{"nvidia", "nvidia_uvm", "nvidia_drm", "nvidia_modeset"}

// Normalized vendor names.
const (
	VendorIntel  = "intel"
	VendorAMD    = "amd"
	VendorNVIDIA = "nvidia"
)

// Arch is the Talos architecture name of the machine the action runs on.
func Arch() string {
	return runtime.GOARCH
}

// Bootloader returns the Image Factory bootloader for arch: sd-boot on
// arm64, the Factory default otherwise.
func Bootloader(arch string) string {
	if arch == "arm64" {
		return "sd-boot"
	}

	return ""
}

// NormalizeVendor maps vendor strings from cpuinfo, sysfs PCI IDs and BMC
// inventories onto intel, amd or nvidia; anything else yields "".
func NormalizeVendor(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))

	switch {
	case v == "":
		return ""
	case v == "0x8086", strings.Contains(v, "intel"):
		return VendorIntel
	case v == "0x1002", v == "0x1022", strings.Contains(v, "amd"), strings.Contains(v, "advanced micro devices"):
		return VendorAMD
	case v == "0x10de", strings.Contains(v, "nvidia"):
		return VendorNVIDIA
	}

	return ""
}

// CPUVendor reads the vendor_id line of /proc/cpuinfo content. arm64 has no
// vendor_id and yields "".
func CPUVendor(cpuinfo io.Reader) string {
	sc := bufio.NewScanner(cpuinfo)

	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		if ok && strings.TrimSpace(key) == "vendor_id" {
			return NormalizeVendor(value)
		}
	}

	return ""
}

// GPUVendors lists the normalized vendors of display-class PCI devices
// (class 0x03xxxx) under <sysRoot>/bus/pci/devices, sorted and deduplicated.
// A missing PCI tree yields nothing.
func GPUVendors(sysRoot string) ([]string, error) {
	devices := filepath.Join(sysRoot, "bus", "pci", "devices")

	entries, err := os.ReadDir(devices)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, err
	}

	var out []string

	for _, entry := range entries {
		class := readTrim(filepath.Join(devices, entry.Name(), "class"))
		if !strings.HasPrefix(class, "0x03") {
			continue
		}

		out = appendUnique(out, NormalizeVendor(readTrim(filepath.Join(devices, entry.Name(), "vendor"))))
	}

	sort.Strings(out)

	return out, nil
}

// HasNVMe reports whether "nvme" is among the disk transports.
func HasNVMe(transports []string) bool {
	for _, t := range transports {
		if t == "nvme" {
			return true
		}
	}

	return false
}

// Local are the facts probed on the machine itself.
type Local struct {
	Arch       string
	CPUVendor  string
	GPUVendors []string
	NVMe       bool
}

// OutOfBand are the same facts as the BMC inventory reports them.
type OutOfBand struct {
	CPUVendor  string
	GPUVendors []string
	NVMe       bool
}

// FromInventory extracts OutOfBand facts from the Hardware status inventory.
func FromInventory(inv *hardware.Inventory) OutOfBand {
	var o OutOfBand

	if inv == nil {
		return o
	}

	if inv.CPU != nil {
		for _, s := range inv.CPU.Sockets {
			if v := NormalizeVendor(s.Vendor); v != "" {
				o.CPUVendor = v

				break
			}
		}
	}

	for _, g := range inv.GPUDevices {
		o.GPUVendors = appendUnique(o.GPUVendors, NormalizeVendor(g.Vendor))
	}

	sort.Strings(o.GPUVendors)

	for _, b := range inv.BlockDevices {
		if strings.Contains(strings.ToLower(b.ControllerType+" "+b.DriveType), "nvme") {
			o.NVMe = true
		}
	}

	return o
}

// Facts are the merged detection results.
type Facts struct {
	Arch       string
	CPUVendor  string
	GPUVendors []string
	NVMe       bool
	// Sources records where each signal came from: "local", "out-of-band" or "none".
	Sources map[string]string
}

// Gather merges local facts with the out-of-band fallback: the fallback is
// used only where the local probe reported nothing.
func Gather(local Local, oob OutOfBand) Facts {
	f := Facts{Arch: local.Arch, Sources: map[string]string{}}

	switch {
	case local.CPUVendor != "":
		f.CPUVendor, f.Sources["cpu"] = local.CPUVendor, "local"
	case oob.CPUVendor != "":
		f.CPUVendor, f.Sources["cpu"] = oob.CPUVendor, "out-of-band"
	default:
		f.Sources["cpu"] = "none"
	}

	switch {
	case len(local.GPUVendors) > 0:
		f.GPUVendors, f.Sources["gpu"] = local.GPUVendors, "local"
	case len(oob.GPUVendors) > 0:
		f.GPUVendors, f.Sources["gpu"] = oob.GPUVendors, "out-of-band"
	default:
		f.Sources["gpu"] = "none"
	}

	switch {
	case local.NVMe:
		f.NVMe, f.Sources["nvme"] = true, "local"
	case oob.NVMe:
		f.NVMe, f.Sources["nvme"] = true, "out-of-band"
	default:
		f.Sources["nvme"] = "none"
	}

	return f
}

// Extensions returns the sorted, deduplicated extensions the facts imply.
// nvidia is the list added for an NVIDIA GPU (NVIDIA_EXTENSIONS); empty adds none.
func Extensions(f Facts, nvidia []string) []string {
	var out []string

	if f.NVMe {
		out = appendUnique(out, ExtNVMeCLI)
	}

	switch f.CPUVendor {
	case VendorIntel:
		out = appendUnique(out, ExtIntelUcode)
	case VendorAMD:
		out = appendUnique(out, ExtAMDUcode)
	}

	for _, g := range f.GPUVendors {
		switch g {
		case VendorAMD:
			out = appendUnique(out, ExtAMDGPU)
		case VendorIntel:
			out = appendUnique(out, ExtI915)
		case VendorNVIDIA:
			for _, ext := range nvidia {
				out = appendUnique(out, ext)
			}
		}
	}

	sort.Strings(out)

	return out
}

// KernelModules returns the machine.kernel.modules the facts require: the
// NVIDIA modules when an NVIDIA GPU is present, nothing otherwise.
func KernelModules(f Facts) []string {
	for _, g := range f.GPUVendors {
		if g == VendorNVIDIA {
			return append([]string(nil), NVIDIAKernelModules...)
		}
	}

	return nil
}

// SplitList splits a comma-separated list, trimming blanks.
func SplitList(csv string) []string {
	var out []string

	for _, part := range strings.Split(csv, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}

	return out
}

func readTrim(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(raw))
}

func appendUnique(list []string, value string) []string {
	if value == "" {
		return list
	}

	for _, existing := range list {
		if existing == value {
			return list
		}
	}

	return append(list, value)
}
