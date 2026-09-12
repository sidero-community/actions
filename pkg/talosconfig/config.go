// Package talosconfig reads the few fields of a Talos v1alpha1 machine
// configuration that the install needs, and applies the overrides talos2disk
// writes back. It deliberately does not depend on the Talos machinery module.
package talosconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config is the subset of the machine configuration the actions use.
type Config struct {
	// Found reports whether a v1alpha1 document with a machine key was present.
	// A Config with Found false carries no values and cannot be patched.
	Found   bool
	Install Install
	Network Network
	Time    Time
}

// Install mirrors machine.install.
type Install struct {
	// Disk is machine.install.disk verbatim; it may be a placeholder such as DISK_ID.
	Disk string
	// DiskSelector is machine.install.diskSelector re-encoded as YAML, nil when absent.
	DiskSelector []byte
	// Image is machine.install.image verbatim.
	Image string
	// ExtraKernelArgs is machine.install.extraKernelArgs.
	ExtraKernelArgs []string
}

// Network mirrors the parts of machine.network used for META.
type Network struct {
	Hostname    string
	Nameservers []string
}

// Time mirrors machine.time.
type Time struct {
	Servers []string
}

// document is the shape decoded from each YAML document in userData.
type document struct {
	Version string `yaml:"version"`
	Machine *struct {
		Install *struct {
			Disk            string    `yaml:"disk"`
			DiskSelector    yaml.Node `yaml:"diskSelector"`
			Image           string    `yaml:"image"`
			ExtraKernelArgs []string  `yaml:"extraKernelArgs"`
		} `yaml:"install"`
		Network *struct {
			Hostname    string   `yaml:"hostname"`
			Nameservers []string `yaml:"nameservers"`
		} `yaml:"network"`
		Time *struct {
			Servers []string `yaml:"servers"`
		} `yaml:"time"`
	} `yaml:"machine"`
}

// Parse decodes userData. Blank input returns (nil, nil): there is no
// configuration layer. Invalid YAML is an error. Valid YAML without a
// v1alpha1 machine document yields an empty Config.
func Parse(userData string) (*Config, error) {
	if strings.TrimSpace(userData) == "" {
		return nil, nil
	}

	dec := yaml.NewDecoder(strings.NewReader(userData))

	for {
		var doc document

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("parsing machine configuration: %w", err)
		}

		if doc.Version != "v1alpha1" || doc.Machine == nil {
			continue
		}

		cfg := &Config{Found: true}

		if in := doc.Machine.Install; in != nil {
			cfg.Install.Disk = strings.TrimSpace(in.Disk)
			cfg.Install.Image = strings.TrimSpace(in.Image)
			cfg.Install.ExtraKernelArgs = in.ExtraKernelArgs

			if in.DiskSelector.Kind != 0 {
				encoded, err := yaml.Marshal(&in.DiskSelector)
				if err != nil {
					return nil, fmt.Errorf("re-encoding machine.install.diskSelector: %w", err)
				}

				cfg.Install.DiskSelector = bytes.TrimSpace(encoded)
			}
		}

		if n := doc.Machine.Network; n != nil {
			cfg.Network.Hostname = strings.TrimSpace(n.Hostname)
			cfg.Network.Nameservers = n.Nameservers
		}

		if tm := doc.Machine.Time; tm != nil {
			cfg.Time.Servers = tm.Servers
		}

		return cfg, nil
	}

	return &Config{}, nil
}

// InstallDiskPath returns machine.install.disk when it is a device path.
// Placeholders such as DISK_ID are not paths and yield "".
func (c *Config) InstallDiskPath() string {
	if c == nil || !strings.HasPrefix(c.Install.Disk, "/dev/") {
		return ""
	}

	return c.Install.Disk
}

// talosVersionTag matches a full Talos version or a bare minor used as an image tag.
var talosVersionTag = regexp.MustCompile(`^v\d+\.\d+(\.\d+(-[0-9A-Za-z.\-]+)?)?$`)

// ImageTag returns the tag of machine.install.image when it looks like a
// Talos version (v1.14 or v1.14.2, optionally with a prerelease suffix).
func (c *Config) ImageTag() string {
	if c == nil || c.Install.Image == "" {
		return ""
	}

	ref := c.Install.Image
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}

	name := ref[strings.LastIndex(ref, "/")+1:]

	_, tag, ok := strings.Cut(name, ":")
	if !ok || !talosVersionTag.MatchString(tag) {
		return ""
	}

	return tag
}
