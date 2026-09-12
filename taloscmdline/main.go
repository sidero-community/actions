package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/sidero-community/actions/pkg/cmdline"
	"github.com/sidero-community/actions/pkg/partition"
)

const efiLabel = "EFI"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	logger.Info("TALOSCMDLINE - Set kernel arguments in Talos unified kernel images")

	if err := run(logger); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	disk := os.Getenv("DEST_DISK")
	if disk == "" {
		return errors.New("no block device specified with environment variable [DEST_DISK]")
	}

	args := strings.TrimSpace(os.Getenv("KERNEL_ARGS"))
	if args == "" {
		return errors.New("no kernel arguments specified with environment variable [KERNEL_ARGS]")
	}

	pattern := os.Getenv("UKI_PATTERN")
	if pattern == "" {
		pattern = cmdline.DefaultPattern
	}

	if err := cmdline.ValidatePattern(pattern); err != nil {
		return fmt.Errorf("UKI_PATTERN: %w", err)
	}

	// The error can be ignored: anything unparsable means false.
	strip, _ := strconv.ParseBool(os.Getenv("STRIP_SIGNATURE"))

	part, err := partition.Locate(disk, efiLabel)
	if err != nil {
		return err
	}

	device := partition.Device(disk, part.Index)
	logger.Info("Found EFI partition", "disk", disk, "partition", part.Index, "device", device, "offset", part.Offset, "size", part.Size)

	result, err := cmdline.Apply(device, args, cmdline.Options{Pattern: pattern, StripSignature: strip, Logger: logger})
	if err != nil {
		return err
	}

	logger.Info("Finished", "updated", result.Updated, "unchanged", result.Unchanged)

	return nil
}
