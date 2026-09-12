package image

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func compressed(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newDevice(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sda")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWriteDecompressesZstdAndFollowsRedirects(t *testing.T) {
	raw := make([]byte, 3<<20)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	zst := compressed(t, raw)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect/metal-amd64.raw.zst":
			http.Redirect(w, r, "/image/metal-amd64.raw.zst", http.StatusFound)
		case "/image/metal-amd64.raw.zst":
			_, _ = w.Write(zst)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	device := newDevice(t)
	n, err := Write(context.Background(), srv.Client(), srv.URL+"/redirect/metal-amd64.raw.zst", device, Options{Logger: quietLogger(), ProgressInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if n != int64(len(raw)) {
		t.Fatalf("wrote %d bytes, want %d", n, len(raw))
	}
	got, err := os.ReadFile(device)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("device content differs from the decompressed image")
	}
}

func TestWriteRawWithoutZstSuffix(t *testing.T) {
	raw := []byte("plain image bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(raw) }))
	defer srv.Close()

	device := newDevice(t)
	if _, err := Write(context.Background(), srv.Client(), srv.URL+"/metal-amd64.raw", device, Options{Logger: quietLogger()}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(device); !bytes.Equal(got, raw) {
		t.Fatalf("device = %q", got)
	}
}

func TestWriteReportsHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer srv.Close()

	_, err := Write(context.Background(), srv.Client(), srv.URL+"/missing.raw.zst", newDevice(t), Options{Logger: quietLogger()})
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRetryEventuallySucceedsAndRespectsWindow(t *testing.T) {
	RetryBase = time.Millisecond
	RetryCap = 2 * time.Millisecond
	t.Cleanup(func() { RetryBase, RetryCap = time.Second, 30*time.Second })

	var calls atomic.Int32
	err := Retry(context.Background(), time.Second, quietLogger(), func() error {
		if calls.Add(1) < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil || calls.Load() != 3 {
		t.Fatalf("Retry() = %v after %d calls", err, calls.Load())
	}

	calls.Store(0)
	err = Retry(context.Background(), 0, quietLogger(), func() error {
		calls.Add(1)
		return errors.New("permanent")
	})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("window 0 must try once, got %v after %d calls", err, calls.Load())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = Retry(ctx, time.Minute, quietLogger(), func() error { return errors.New("always") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
