package hardware

import "testing"

const sampleObject = `{
  "metadata": {
    "name": "node1", "namespace": "tinkerbell",
    "annotations": {
      "talos.tinkerbell.org/system-extensions": " siderolabs/util-linux-tools ,siderolabs/iscsi-tools",
      "talos.tinkerbell.org/contract": "v1.14"
    }
  },
  "spec": {
    "disks": [{"device": "/dev/nvme0n1"}, {"device": "/dev/sda"}],
    "interfaces": [{"dhcp": {"mac": "52:54:00:12:34:01", "ip": {"address": "10.0.80.10", "netmask": "255.255.255.0", "gateway": "10.0.80.1", "family": 4}}}],
    "metadata": {"instance": {"hostname": "node1", "operating_system": {"slug": "abc", "version": "v1.14.1", "os_slug": "talos-v1.14.1-amd64"}}},
    "userData": "version: v1alpha1\nmachine: {}\n"
  },
  "status": {"attributes": {"outOfBand": {
    "collectionMethod": "redfish",
    "cpu": {"sockets": [{"slot": "CPU1", "vendor": "Intel(R) Corporation", "model": "Xeon"}]},
    "gpuDevices": [{"vendor": "NVIDIA Corporation", "model": "L4"}],
    "blockDevices": [{"name": "Disk 0", "controllerType": "NVMe", "model": "Samsung 980", "serialNumber": "S1", "sizeBytes": 1000204886016}]
  }}}
}`

func TestParseObject(t *testing.T) {
	hw, err := Parse([]byte(sampleObject))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if hw.Metadata.Name != "node1" || hw.Metadata.Namespace != "tinkerbell" {
		t.Fatalf("metadata = %+v", hw.Metadata)
	}
	if got := hw.Annotation(AnnotationExtensions); got != "siderolabs/util-linux-tools ,siderolabs/iscsi-tools" {
		t.Fatalf("Annotation(extensions) = %q", got)
	}
	if got := hw.Annotation(AnnotationOverlay); got != "" {
		t.Fatalf("missing annotation must be empty, got %q", got)
	}
	if hw.FirstDisk() != "/dev/nvme0n1" {
		t.Fatalf("FirstDisk() = %q", hw.FirstDisk())
	}
	if hw.UserData() != "version: v1alpha1\nmachine: {}\n" {
		t.Fatalf("UserData() = %q", hw.UserData())
	}
	if hw.OperatingSystemVersion() != "v1.14.1" {
		t.Fatalf("OperatingSystemVersion() = %q", hw.OperatingSystemVersion())
	}
	inv := hw.OutOfBand()
	if inv == nil || inv.CPU == nil || len(inv.CPU.Sockets) != 1 || inv.CPU.Sockets[0].Vendor != "Intel(R) Corporation" {
		t.Fatalf("unexpected inventory %+v", inv)
	}
	if len(inv.GPUDevices) != 1 || inv.GPUDevices[0].Vendor != "NVIDIA Corporation" {
		t.Fatalf("unexpected gpus %+v", inv.GPUDevices)
	}
	if len(inv.BlockDevices) != 1 || inv.BlockDevices[0].ControllerType != "NVMe" || inv.BlockDevices[0].SizeBytes != 1000204886016 {
		t.Fatalf("unexpected block devices %+v", inv.BlockDevices)
	}
	if len(hw.Spec.Interfaces) != 1 || hw.Spec.Interfaces[0].DHCP.IP.Address != "10.0.80.10" {
		t.Fatalf("unexpected interfaces %+v", hw.Spec.Interfaces)
	}
	if hw.OperatingSystemSlug() != "abc" {
		t.Fatalf("OperatingSystemSlug() = %q", hw.OperatingSystemSlug())
	}
}

func TestParseObjectNilSafety(t *testing.T) {
	hw, err := Parse([]byte(`{"metadata":{"name":"bare"},"spec":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if hw.FirstDisk() != "" || hw.UserData() != "" || hw.OperatingSystemVersion() != "" || hw.OutOfBand() != nil || hw.Annotation("x") != "" || hw.OperatingSystemSlug() != "" {
		t.Fatal("accessors on a bare object must return zero values")
	}
}

func TestParseObjectRejectsEmptyAndInvalid(t *testing.T) {
	if _, err := Parse([]byte("   ")); err == nil {
		t.Fatal("expected an error for an empty document")
	}
	if _, err := Parse([]byte("{not json")); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}
