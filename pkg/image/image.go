// Package image streams a disk image from an HTTP URL onto a block device.
package image

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

// ErrNotFound is returned when the image URL answers 404.
var ErrNotFound = errors.New("image not found")

// Options control Write.
type Options struct {
	// ProgressInterval is how often progress is logged; 5 s when zero.
	ProgressInterval time.Duration
	Logger           *slog.Logger
}

// Write downloads url and writes it to device, decoding zstd when the URL
// ends in .zst. On success the device is synced and its partition table
// re-read. It returns the number of decompressed bytes written.
func Write(ctx context.Context, client *http.Client, url, device string, opts Options) (int64, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	interval := opts.ProgressInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}

	res, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return 0, fmt.Errorf("%s: %w", url, ErrNotFound)
	}

	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("downloading %s: %s", url, res.Status)
	}

	out, err := os.OpenFile(device, os.O_WRONLY, 0)
	if err != nil {
		return 0, fmt.Errorf("opening %s for writing: %w", device, err)
	}
	defer out.Close()

	read := &counter{r: res.Body}

	var src io.Reader = read

	if strings.HasSuffix(url, ".zst") {
		dec, err := zstd.NewReader(read)
		if err != nil {
			return 0, fmt.Errorf("starting zstd decoder: %w", err)
		}
		defer dec.Close()

		src = dec
	}

	written := &counter{w: out}
	done := make(chan struct{})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				logger.Info("Write progress", "downloaded", pretty(read.n.Load()), "written", pretty(written.n.Load()), "compressedSize", pretty(res.ContentLength))
			}
		}
	}()

	n, err := io.Copy(written, src)
	close(done)

	logger.Info("Write finished", "downloaded", pretty(read.n.Load()), "written", pretty(n))

	if err != nil {
		return n, fmt.Errorf("writing %s to %s after %s: %w", url, device, pretty(n), err)
	}

	if err := out.Sync(); err != nil {
		return n, fmt.Errorf("syncing %s: %w", device, err)
	}

	// Ask the kernel to re-read the partition table. Regular files and
	// partitions refuse; that is not an error for the write itself.
	if err := unix.IoctlSetInt(int(out.Fd()), unix.BLKRRPART, 0); err != nil {
		logger.Info("Partition table re-read not performed", "device", device, "error", err)
	}

	return n, nil
}

// Retry parameters; variables so tests can shorten them.
var (
	RetryBase = time.Second
	RetryCap  = 30 * time.Second
)

// Retry runs fn until it succeeds or window elapses, doubling the delay from
// RetryBase up to RetryCap. A zero window runs fn exactly once.
func Retry(ctx context.Context, window time.Duration, logger *slog.Logger, fn func() error) error {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	deadline := time.Now().Add(window)
	delay := RetryBase

	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		if window <= 0 || time.Now().Add(delay).After(deadline) {
			return err
		}

		logger.Warn("Retrying after failure", "attempt", attempt, "delay", delay, "error", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		delay *= 2
		if delay > RetryCap {
			delay = RetryCap
		}
	}
}

// counter counts bytes through a reader or a writer.
type counter struct {
	r io.Reader
	w io.Writer
	n atomic.Int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))

	return n, err
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))

	return n, err
}

func pretty(b int64) string {
	const unit = 1024

	if b < unit {
		return fmt.Sprintf("%d B", b)
	}

	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
