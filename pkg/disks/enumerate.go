package disks

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Prober reads the properties of one block device. BlockdeviceProber is the
// production implementation; tests supply fakes.
type Prober interface {
	Probe(devPath string) (Disk, error)
}

// Enumeration is the result of listing the machine's disks.
type Enumeration struct {
	// Disks are the probed whole disks in kernel-name order.
	Disks []Disk
	// Skipped names the /sys/block entries whose probe failed.
	Skipped map[string]error
}

// Enumerate lists whole disks from <sysRoot>/block (partitions are not
// listed there) and probes <devRoot>/<name> for each.
func Enumerate(sysRoot, devRoot string, prober Prober) (Enumeration, error) {
	blockDir := filepath.Join(sysRoot, "block")

	entries, err := os.ReadDir(blockDir)
	if err != nil {
		return Enumeration{}, fmt.Errorf("listing block devices in %s: %w", blockDir, err)
	}

	out := Enumeration{Skipped: map[string]error{}}

	for _, entry := range entries {
		name := entry.Name()
		devPath := filepath.Join(devRoot, name)

		d, err := prober.Probe(devPath)
		if err != nil {
			out.Skipped[name] = err

			continue
		}

		d.Name = name
		d.DevPath = devPath
		out.Disks = append(out.Disks, d)
	}

	SortByName(out.Disks)

	return out, nil
}

// Transports returns the sorted set of transports present in list.
func Transports(list []Disk) []string {
	seen := map[string]bool{}

	var out []string

	for _, d := range list {
		if d.Transport == "" || seen[d.Transport] {
			continue
		}

		seen[d.Transport] = true
		out = append(out, d.Transport)
	}

	sort.Strings(out)

	return out
}
