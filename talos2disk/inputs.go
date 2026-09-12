package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Inputs are the action's environment variables, parsed.
type Inputs struct {
	Hardware       string
	SchematicID    string
	TalosVersion   string
	DiskSelector   string
	KernelArgs     string
	FactoryURL     string
	NetworkConfig  string
	LinkNaming     string
	StripSignature bool
	RetryWindow    time.Duration
	DryRun         bool
}

const defaultRetryMinutes = 10

func inputsFromEnv() (Inputs, error) {
	in := Inputs{
		Hardware:      os.Getenv("HARDWARE"),
		SchematicID:   strings.TrimSpace(os.Getenv("SCHEMATIC_ID")),
		TalosVersion:  strings.TrimSpace(os.Getenv("TALOS_VERSION")),
		DiskSelector:  strings.TrimSpace(os.Getenv("DISK_SELECTOR")),
		KernelArgs:    strings.TrimSpace(os.Getenv("KERNEL_ARGS")),
		FactoryURL:    strings.TrimSpace(os.Getenv("FACTORY_URL")),
		NetworkConfig: os.Getenv("NETWORK_CONFIG"),
		LinkNaming:    strings.TrimSpace(os.Getenv("LINK_NAMING")),
	}

	// Unparsable booleans mean false, as in the other actions.
	in.StripSignature, _ = strconv.ParseBool(os.Getenv("STRIP_SIGNATURE"))
	in.DryRun, _ = strconv.ParseBool(os.Getenv("DRY_RUN"))

	minutes := defaultRetryMinutes

	if raw := strings.TrimSpace(os.Getenv("RETRY_DURATION_MINUTES")); raw != "" {
		m, err := strconv.Atoi(raw)
		if err != nil || m < 0 {
			return Inputs{}, fmt.Errorf("RETRY_DURATION_MINUTES must be a non-negative integer, got %q", raw)
		}

		minutes = m
	}

	in.RetryWindow = time.Duration(minutes) * time.Minute

	switch in.LinkNaming {
	case "", "kernel", "predictable":
	default:
		return Inputs{}, fmt.Errorf("LINK_NAMING must be \"kernel\" or \"predictable\", got %q", in.LinkNaming)
	}

	return in, nil
}
