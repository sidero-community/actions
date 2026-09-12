package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunRequiresInputs(t *testing.T) {
	t.Setenv("DEST_DISK", "")
	t.Setenv("KERNEL_ARGS", "a=1")
	if err := run(quietLogger()); err == nil || !strings.Contains(err.Error(), "DEST_DISK") {
		t.Fatalf("expected a DEST_DISK error, got %v", err)
	}

	t.Setenv("DEST_DISK", "/dev/null")
	t.Setenv("KERNEL_ARGS", "")
	if err := run(quietLogger()); err == nil || !strings.Contains(err.Error(), "KERNEL_ARGS") {
		t.Fatalf("expected a KERNEL_ARGS error, got %v", err)
	}
}

func TestRunRejectsUnsafePattern(t *testing.T) {
	t.Setenv("DEST_DISK", "/dev/null")
	t.Setenv("KERNEL_ARGS", "a=1")
	t.Setenv("UKI_PATTERN", "../escape")
	if err := run(quietLogger()); err == nil || !strings.Contains(err.Error(), "UKI_PATTERN") {
		t.Fatalf("expected a UKI_PATTERN error, got %v", err)
	}
}
