package disks

import (
	"fmt"

	"github.com/siderolabs/go-blockdevice/v2/block"
)

// BlockdeviceProber reads disk properties with go-blockdevice, the library
// Talos itself uses, so transport, model, serial and the rest match what a
// Talos disk selector would see.
type BlockdeviceProber struct{}

// Probe implements Prober.
func (BlockdeviceProber) Probe(devPath string) (Disk, error) {
	dev, err := block.NewFromPath(devPath)
	if err != nil {
		return Disk{}, fmt.Errorf("opening %s: %w", devPath, err)
	}
	defer dev.Close()

	size, err := dev.GetSize()
	if err != nil {
		return Disk{}, fmt.Errorf("reading size of %s: %w", devPath, err)
	}

	props, err := dev.GetProperties()
	if err != nil {
		return Disk{}, fmt.Errorf("reading properties of %s: %w", devPath, err)
	}

	readonly, err := dev.IsReadOnly()
	if err != nil {
		return Disk{}, fmt.Errorf("reading read-only flag of %s: %w", devPath, err)
	}

	return Disk{
		DevPath:    devPath,
		Size:       size,
		Transport:  props.Transport,
		Rotational: props.Rotational,
		Readonly:   readonly,
		CDROM:      dev.IsCD(),
		Model:      props.Model,
		Serial:     props.Serial,
		Modalias:   props.Modalias,
		UUID:       props.UUID,
		WWID:       props.WWID,
		BusPath:    props.BusPath,
	}, nil
}
