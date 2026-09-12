// Package cmdline merges kernel arguments into the Talos unified kernel
// images stored on an EFI partition. It is the shared core of the
// taloscmdline and talos2disk actions.
package cmdline

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/sidero-community/actions/pkg/kargs"
	"github.com/sidero-community/actions/pkg/uki"
)

// DefaultPattern is the glob, relative to the EFI partition root, of the UKIs
// Talos installs.
const DefaultPattern = "EFI/Linux/Talos-*.efi"

// DefaultMountPoint is where the EFI partition is mounted while editing.
const DefaultMountPoint = "/mountAction"

// Mounter mounts and unmounts the EFI filesystem. VFAT is the production
// implementation; tests substitute a directory.
type Mounter interface {
	Mount(device, target string) error
	Unmount(target string) error
}

// VFAT mounts a device as a vfat filesystem with mount(2).
type VFAT struct{}

// Mount implements Mounter.
func (VFAT) Mount(device, target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("creating mountpoint %s: %w", target, err)
	}

	if err := syscall.Mount(device, target, "vfat", 0, ""); err != nil {
		return fmt.Errorf("mounting %s on %s as vfat: %w", device, target, err)
	}

	return nil
}

// Unmount implements Mounter.
func (VFAT) Unmount(target string) error {
	return syscall.Unmount(target, 0)
}

// Options control Apply.
type Options struct {
	// Pattern is the UKI glob relative to the partition root; DefaultPattern when empty.
	Pattern string
	// StripSignature allows rewriting an Authenticode-signed UKI.
	StripSignature bool
	// MountPoint defaults to DefaultMountPoint.
	MountPoint string
	// Mounter defaults to VFAT.
	Mounter Mounter
	// Logger defaults to a discarding logger.
	Logger *slog.Logger
}

// Result reports what was done to the UKIs.
type Result struct {
	Updated   int
	Unchanged int
	// Cmdlines holds the final command lines of every matched UKI, keyed by
	// the path relative to the partition root.
	Cmdlines map[string][]string
}

// ValidatePattern rejects patterns that could escape the partition root.
func ValidatePattern(pattern string) error {
	if filepath.IsAbs(pattern) || strings.Contains(pattern, "..") {
		return fmt.Errorf("UKI pattern must be relative to the EFI partition, got %q", pattern)
	}

	return nil
}

// Apply mounts device, merges args into every UKI matching opts.Pattern and
// unmounts again. Unmount failures are logged, not returned.
func Apply(device, args string, opts Options) (Result, error) {
	if strings.TrimSpace(args) == "" {
		return Result{}, errors.New("no kernel arguments to apply")
	}

	pattern := opts.Pattern
	if pattern == "" {
		pattern = DefaultPattern
	}

	if err := ValidatePattern(pattern); err != nil {
		return Result{}, err
	}

	mountPoint := opts.MountPoint
	if mountPoint == "" {
		mountPoint = DefaultMountPoint
	}

	mounter := opts.Mounter
	if mounter == nil {
		mounter = VFAT{}
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	if err := mounter.Mount(device, mountPoint); err != nil {
		return Result{}, err
	}

	logger.Info("Mounted device", "source", device, "destination", mountPoint)

	defer func() {
		if err := mounter.Unmount(mountPoint); err != nil {
			logger.Error("Error unmounting device", "source", device, "destination", mountPoint, "error", err)
		} else {
			logger.Info("Unmounted device", "source", device, "destination", mountPoint)
		}
	}()

	return EditTree(mountPoint, pattern, args, opts.StripSignature, logger)
}

// EditTree merges args into the command line of every UKI under root that
// matches pattern. Files whose command line already contains the arguments
// are left untouched but still reported in Result.Cmdlines.
func EditTree(root, pattern, args string, strip bool, logger *slog.Logger) (Result, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	matches, err := filepath.Glob(filepath.Join(root, pattern))
	if err != nil {
		return Result{}, fmt.Errorf("invalid UKI pattern %q: %w", pattern, err)
	}

	if len(matches) == 0 {
		return Result{}, fmt.Errorf("no UKI matching %q found on the EFI partition", pattern)
	}

	sort.Strings(matches)

	res := Result{Cmdlines: make(map[string][]string, len(matches))}

	for _, path := range matches {
		rel, _ := filepath.Rel(root, path)

		info, err := uki.Inspect(path)
		if err != nil {
			return res, err
		}

		merged := make([]string, len(info.Cmdlines))
		changed := false

		for i, current := range info.Cmdlines {
			merged[i] = kargs.Merge(current, args)
			if merged[i] != current {
				changed = true
			}
		}

		for i, loc := range info.Locations {
			logger.Info("Found kernel command line", "uki", rel, "cmdline", i, "section", loc.Section, "offset", fmt.Sprintf("%#x", loc.Offset), "bytes", loc.Size)
		}

		res.Cmdlines[rel] = merged

		if !changed {
			logger.Info("UKI already has the requested arguments", "uki", rel, "cmdline", info.Cmdlines[0])
			res.Unchanged++

			continue
		}

		if info.Signed {
			if !strip {
				return res, fmt.Errorf("%s: %w; set STRIP_SIGNATURE=true to remove it (the UKI will then only boot with Secure Boot disabled)", rel, uki.ErrSigned)
			}

			logger.Warn("Removing Authenticode signature; the UKI will not boot with Secure Boot enabled", "uki", rel)
		}

		if err := uki.Rewrite(path, merged, uki.Options{StripSignature: strip}); err != nil {
			return res, err
		}

		for i := range merged {
			logger.Info("Updated UKI", "uki", rel, "cmdline", i, "old", info.Cmdlines[i], "new", merged[i])
		}

		res.Updated++
	}

	return res, nil
}
