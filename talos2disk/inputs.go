package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sidero-community/actions/pkg/detect"
	"github.com/sidero-community/actions/pkg/factory"
)

// Inputs are the action's environment variables, parsed.
type Inputs struct {
	Hardware         string
	TalosVersion     string
	DiskSelector     string
	KernelArgs       string
	Extensions       string
	Overlay          string
	NVIDIAExtensions string
	FactoryURL       string
	NetworkConfig    string
	LinkNaming       string
	Kubeconfig       string
	StripSignature   bool
	RetryWindow      time.Duration
	DryRun           bool
}

const defaultRetryMinutes = 10

func inputsFromEnv() (Inputs, error) {
	in := Inputs{
		Hardware:      os.Getenv("HARDWARE"),
		TalosVersion:  strings.TrimSpace(os.Getenv("TALOS_VERSION")),
		DiskSelector:  strings.TrimSpace(os.Getenv("DISK_SELECTOR")),
		KernelArgs:    strings.TrimSpace(os.Getenv("KERNEL_ARGS")),
		Extensions:    os.Getenv("EXTENSIONS"),
		Overlay:       strings.TrimSpace(os.Getenv("OVERLAY")),
		FactoryURL:    strings.TrimSpace(os.Getenv("FACTORY_URL")),
		NetworkConfig: os.Getenv("NETWORK_CONFIG"),
		LinkNaming:    strings.TrimSpace(os.Getenv("LINK_NAMING")),
		Kubeconfig:    strings.TrimSpace(os.Getenv("KUBECONFIG")),
	}

	if in.FactoryURL == "" {
		in.FactoryURL = factory.DefaultURL
	}

	if v, ok := os.LookupEnv("NVIDIA_EXTENSIONS"); ok {
		in.NVIDIAExtensions = v
	} else {
		in.NVIDIAExtensions = detect.DefaultNVIDIAExtensions
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
