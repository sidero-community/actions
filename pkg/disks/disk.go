// Package disks enumerates the whole disks of the machine and selects the
// install disk with the semantics of Talos' machine.install.diskSelector.
package disks

import "sort"

// Disk describes a whole block device with the properties Talos exposes to
// its disk selector.
type Disk struct {
	// Name is the kernel name, e.g. sda or nvme0n1.
	Name string
	// DevPath is the device node, e.g. /dev/sda.
	DevPath    string
	Size       uint64
	Transport  string
	Rotational bool
	Readonly   bool
	CDROM      bool
	Model      string
	Serial     string
	Modalias   string
	UUID       string
	WWID       string
	BusPath    string
}

// Eligible mirrors the implicit filters Talos adds to every install disk
// match: a real transport (which excludes loop, ram, zram, device-mapper and
// md devices), writable, and not a CD-ROM.
func Eligible(d Disk) bool {
	return d.Transport != "" && !d.Readonly && !d.CDROM
}

// SortByName orders disks by kernel name, the order Talos lists them in.
func SortByName(list []Disk) {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
}
