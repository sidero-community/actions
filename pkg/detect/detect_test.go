package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sidero-community/actions/pkg/hardware"
)

const intelCPUInfo = `processor	: 0
vendor_id	: GenuineIntel
cpu family	: 6
model name	: Intel(R) Xeon(R) CPU E-2288G
`

const amdCPUInfo = "processor\t: 0\nvendor_id\t: AuthenticAMD\nmodel name\t: AMD EPYC 7302P\n"

const armCPUInfo = "processor\t: 0\nBogoMIPS\t: 50.00\nCPU implementer\t: 0x41\n"

func TestCPUVendor(t *testing.T) {
	if got := CPUVendor(strings.NewReader(intelCPUInfo)); got != VendorIntel {
		t.Errorf("intel = %q", got)
	}
	if got := CPUVendor(strings.NewReader(amdCPUInfo)); got != VendorAMD {
		t.Errorf("amd = %q", got)
	}
	if got := CPUVendor(strings.NewReader(armCPUInfo)); got != "" {
		t.Errorf("arm = %q, want empty", got)
	}
}

func TestNormalizeVendor(t *testing.T) {
	for in, want := range map[string]string{
		"GenuineIntel": VendorIntel, "Intel(R) Corporation": VendorIntel, "0x8086": VendorIntel,
		"AuthenticAMD": VendorAMD, "Advanced Micro Devices, Inc. [AMD/ATI]": VendorAMD, "0x1002": VendorAMD, "0x1022": VendorAMD,
		"NVIDIA Corporation": VendorNVIDIA, "0x10de": VendorNVIDIA,
		"Ampere(R)": "", "": "",
	} {
		if got := NormalizeVendor(in); got != want {
			t.Errorf("NormalizeVendor(%q) = %q, want %q", in, got, want)
		}
	}
}

func writePCI(t *testing.T, sysRoot, addr, class, vendor string) {
	t.Helper()
	dir := filepath.Join(sysRoot, "bus", "pci", "devices", addr)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "class"), []byte(class+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vendor"), []byte(vendor+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGPUVendors(t *testing.T) {
	sysRoot := t.TempDir()
	writePCI(t, sysRoot, "0000:00:02.0", "0x030000", "0x8086") // Intel VGA
	writePCI(t, sysRoot, "0000:03:00.0", "0x030200", "0x10de") // NVIDIA 3D controller
	writePCI(t, sysRoot, "0000:04:00.0", "0x030000", "0x10de") // second NVIDIA, deduplicated
	writePCI(t, sysRoot, "0000:00:1f.2", "0x010601", "0x8086") // SATA controller, ignored
	writePCI(t, sysRoot, "0000:05:00.0", "0x030000", "0x1002") // AMD

	got, err := GPUVendors(sysRoot)
	if err != nil {
		t.Fatalf("GPUVendors() error: %v", err)
	}
	if strings.Join(got, ",") != "amd,intel,nvidia" {
		t.Fatalf("GPUVendors() = %v", got)
	}

	if got, err := GPUVendors(t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("missing pci tree should yield nothing, got %v, %v", got, err)
	}
}

func TestHasNVMe(t *testing.T) {
	if !HasNVMe([]string{"sata", "nvme"}) || HasNVMe([]string{"sata", "usb"}) || HasNVMe(nil) {
		t.Fatal("HasNVMe mismatch")
	}
}

func TestFromInventory(t *testing.T) {
	if got := FromInventory(nil); got.CPUVendor != "" || got.NVMe || len(got.GPUVendors) != 0 {
		t.Fatalf("nil inventory = %+v", got)
	}
	inv := &hardware.Inventory{
		CPU:          &hardware.CPU{Sockets: []hardware.CPUSocket{{Vendor: "Advanced Micro Devices, Inc."}}},
		GPUDevices:   []hardware.GPUDevice{{Vendor: "NVIDIA Corporation"}, {Vendor: "NVIDIA"}},
		BlockDevices: []hardware.BlockDevice{{ControllerType: "NVMe"}},
	}
	got := FromInventory(inv)
	if got.CPUVendor != VendorAMD || !got.NVMe || strings.Join(got.GPUVendors, ",") != "nvidia" {
		t.Fatalf("FromInventory() = %+v", got)
	}
}

func TestGatherPrefersLocalAndFallsBack(t *testing.T) {
	f := Gather(Local{Arch: "amd64", CPUVendor: VendorIntel, NVMe: false}, OutOfBand{CPUVendor: VendorAMD, GPUVendors: []string{VendorNVIDIA}, NVMe: true})
	if f.CPUVendor != VendorIntel || f.Sources["cpu"] != "local" {
		t.Errorf("cpu = %q from %q", f.CPUVendor, f.Sources["cpu"])
	}
	if strings.Join(f.GPUVendors, ",") != "nvidia" || f.Sources["gpu"] != "out-of-band" {
		t.Errorf("gpu = %v from %q", f.GPUVendors, f.Sources["gpu"])
	}
	if !f.NVMe || f.Sources["nvme"] != "out-of-band" {
		t.Errorf("nvme = %v from %q", f.NVMe, f.Sources["nvme"])
	}
	if f.Arch != "amd64" {
		t.Errorf("arch = %q", f.Arch)
	}
}

func TestExtensionsAndModules(t *testing.T) {
	f := Facts{Arch: "amd64", CPUVendor: VendorIntel, GPUVendors: []string{VendorNVIDIA, VendorAMD, VendorIntel}, NVMe: true}
	got := Extensions(f, SplitList(DefaultNVIDIAExtensions))
	want := "siderolabs/amdgpu,siderolabs/i915,siderolabs/intel-ucode,siderolabs/nvidia-container-toolkit-production,siderolabs/nvidia-open-gpu-kernel-modules-production,siderolabs/nvme-cli"
	if strings.Join(got, ",") != want {
		t.Fatalf("Extensions() = %v", got)
	}
	if mods := KernelModules(f); strings.Join(mods, ",") != "nvidia,nvidia_uvm,nvidia_drm,nvidia_modeset" {
		t.Fatalf("KernelModules() = %v", mods)
	}

	plain := Facts{Arch: "arm64", CPUVendor: "", NVMe: false}
	if got := Extensions(plain, nil); len(got) != 0 {
		t.Fatalf("plain arm64 should add nothing, got %v", got)
	}
	if mods := KernelModules(plain); len(mods) != 0 {
		t.Fatalf("no NVIDIA, no modules, got %v", mods)
	}

	amd := Facts{Arch: "amd64", CPUVendor: VendorAMD, GPUVendors: []string{VendorNVIDIA}}
	if got := Extensions(amd, nil); strings.Join(got, ",") != "siderolabs/amd-ucode" {
		t.Fatalf("empty NVIDIA list must add no NVIDIA extension, got %v", got)
	}
}

func TestBootloaderAndSplitList(t *testing.T) {
	if Bootloader("arm64") != "sd-boot" || Bootloader("amd64") != "" {
		t.Fatal("Bootloader mismatch")
	}
	if got := SplitList(" a, b ,,c "); strings.Join(got, "|") != "a|b|c" {
		t.Fatalf("SplitList = %v", got)
	}
	if got := SplitList(""); len(got) != 0 {
		t.Fatalf("SplitList('') = %v", got)
	}
}
