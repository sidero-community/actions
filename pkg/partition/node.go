package partition

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// NodeOptions control EnsureNode. Zero values take the defaults noted.
type NodeOptions struct {
	// SysRoot is the sysfs mount point; /sys when empty.
	SysRoot string
	// Wait is how long to wait for the host to create the node; 10 s when zero.
	Wait time.Duration
	// Poll is the wait interval; 200 ms when zero.
	Poll time.Duration
	// PrivateDir receives nodes created by the fallback; /run/partition-nodes when empty.
	PrivateDir string
	// Mknod creates a block device node; MknodBlock when nil.
	Mknod func(path string, major, minor uint32) error
}

// EnsureNode returns a device node for partition index of disk. It waits for
// the kernel-named node (Device) to appear and otherwise creates a private
// node from the major:minor numbers sysfs reports, since a udev daemon may
// not be running while the action holds the disk.
func EnsureNode(disk string, index int, opts NodeOptions) (string, error) {
	sysRoot, wait, poll, privateDir, mknod := opts.SysRoot, opts.Wait, opts.Poll, opts.PrivateDir, opts.Mknod
	if sysRoot == "" {
		sysRoot = "/sys"
	}

	if wait <= 0 {
		wait = 10 * time.Second
	}

	if poll <= 0 {
		poll = 200 * time.Millisecond
	}

	if privateDir == "" {
		privateDir = "/run/partition-nodes"
	}

	if mknod == nil {
		mknod = MknodBlock
	}

	node := Device(disk, index)
	deadline := time.Now().Add(wait)

	for {
		if _, err := os.Stat(node); err == nil {
			return node, nil
		}

		if time.Now().After(deadline) {
			break
		}

		time.Sleep(poll)
	}

	resolved := disk
	if r, err := filepath.EvalSymlinks(disk); err == nil {
		resolved = r
	}

	diskName := filepath.Base(resolved)
	partName := filepath.Base(Device(resolved, index))
	devFile := filepath.Join(sysRoot, "block", diskName, partName, "dev")

	raw, err := os.ReadFile(devFile)
	if err != nil {
		return "", fmt.Errorf("partition node %s did not appear within %s and %s is unreadable: %w", node, wait, devFile, err)
	}

	majorText, minorText, ok := strings.Cut(strings.TrimSpace(string(raw)), ":")
	if !ok {
		return "", fmt.Errorf("unexpected content %q in %s", strings.TrimSpace(string(raw)), devFile)
	}

	major, err := strconv.ParseUint(majorText, 10, 32)
	if err != nil {
		return "", fmt.Errorf("parsing major number in %s: %w", devFile, err)
	}

	minor, err := strconv.ParseUint(minorText, 10, 32)
	if err != nil {
		return "", fmt.Errorf("parsing minor number in %s: %w", devFile, err)
	}

	if err := os.MkdirAll(privateDir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", privateDir, err)
	}

	path := filepath.Join(privateDir, partName)
	_ = os.Remove(path)

	if err := mknod(path, uint32(major), uint32(minor)); err != nil {
		return "", fmt.Errorf("creating device node %s (%d:%d): %w", path, major, minor, err)
	}

	return path, nil
}

// MknodBlock creates a block device node with mknod(2).
func MknodBlock(path string, major, minor uint32) error {
	return unix.Mknod(path, unix.S_IFBLK|0o600, int(unix.Mkdev(major, minor)))
}
