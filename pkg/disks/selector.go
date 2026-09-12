package disks

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/ryanuber/go-glob"
	"go.yaml.in/yaml/v3"
)

// Disk types accepted by the selector, as in Talos.
const (
	TypeSSD  = "ssd"
	TypeHDD  = "hdd"
	TypeNVMe = "nvme"
	TypeSD   = "sd"
)

// DefaultSelectorDocument is used when nothing else selects a disk.
const DefaultSelectorDocument = `{ "size": ">= 100GB" }`

// SizeMatcher is a parsed size condition such as ">= 100GB".
type SizeMatcher struct {
	Op   string
	Size uint64
}

var sizeCondition = regexp.MustCompile(`^(>=|<=|>|<|==)?\s*(.+)$`)

// ParseSize parses Talos' size condition grammar: an optional operator
// (>=, <=, >, <, ==; none means ==) followed by a size with units.
// Decimal units (GB, TB) and binary units (GiB, TiB) are both accepted.
func ParseSize(condition string) (SizeMatcher, error) {
	condition = strings.TrimSpace(condition)

	parts := sizeCondition.FindStringSubmatch(condition)
	if parts == nil {
		return SizeMatcher{}, fmt.Errorf("failed to parse the condition: expected [>=|<=|>|<|==]<size>[units], got %q", condition)
	}

	op := parts[1]
	if op == "" {
		op = "=="
	}

	size, err := humanize.ParseBytes(strings.TrimSpace(parts[2]))
	if err != nil {
		return SizeMatcher{}, fmt.Errorf("failed to parse disk size %q: %w", parts[2], err)
	}

	return SizeMatcher{Op: op, Size: size}, nil
}

// Match reports whether size satisfies the condition.
func (m SizeMatcher) Match(size uint64) bool {
	switch m.Op {
	case ">=":
		return size >= m.Size
	case "<=":
		return size <= m.Size
	case ">":
		return size > m.Size
	case "<":
		return size < m.Size
	default:
		return size == m.Size
	}
}

// Selector is a Talos machine.install.diskSelector document.
type Selector struct {
	Size     *SizeMatcher
	Model    string
	Serial   string
	Modalias string
	UUID     string
	WWID     string
	BusPath  string
	Type     string

	raw string
}

type selectorDocument struct {
	Size     string `yaml:"size"`
	Name     string `yaml:"name"`
	Model    string `yaml:"model"`
	Serial   string `yaml:"serial"`
	Modalias string `yaml:"modalias"`
	UUID     string `yaml:"uuid"`
	WWID     string `yaml:"wwid"`
	Type     string `yaml:"type"`
	BusPath  string `yaml:"busPath"`
}

// ParseSelector decodes a selector document (YAML or JSON). Unknown fields
// are rejected, `name` is rejected as Talos rejects it, and an empty document
// matches every eligible disk.
func ParseSelector(doc []byte) (*Selector, error) {
	sel := &Selector{raw: string(bytes.TrimSpace(doc))}

	dec := yaml.NewDecoder(bytes.NewReader(doc))
	dec.KnownFields(true)

	var d selectorDocument

	if err := dec.Decode(&d); err != nil {
		if errors.Is(err, io.EOF) {
			return sel, nil
		}

		return nil, fmt.Errorf("parsing disk selector: %w", err)
	}

	if d.Name != "" {
		return nil, errors.New("selector on name is not supported")
	}

	switch d.Type {
	case "", TypeSSD, TypeHDD, TypeNVMe, TypeSD:
	default:
		return nil, fmt.Errorf("unknown disk type %q, expected one of ssd, hdd, nvme, sd", d.Type)
	}

	if d.Size != "" {
		m, err := ParseSize(d.Size)
		if err != nil {
			return nil, err
		}

		sel.Size = &m
	}

	sel.Model, sel.Serial, sel.Modalias, sel.UUID, sel.WWID, sel.BusPath, sel.Type = d.Model, d.Serial, d.Modalias, d.UUID, d.WWID, d.BusPath, d.Type

	return sel, nil
}

// DefaultSelector returns the parsed DefaultSelectorDocument.
func DefaultSelector() *Selector {
	sel, err := ParseSelector([]byte(DefaultSelectorDocument))
	if err != nil {
		panic(err)
	}

	return sel
}

// String returns the document the selector was parsed from.
func (s *Selector) String() string {
	if s.raw == "" {
		return "{}"
	}

	return s.raw
}

// Match reports whether d satisfies the selector and the implicit filters.
func (s *Selector) Match(d Disk) bool {
	if !Eligible(d) {
		return false
	}

	if s.Size != nil && !s.Size.Match(d.Size) {
		return false
	}

	for _, f := range []struct{ pattern, value string }{
		{s.Model, d.Model}, {s.Serial, d.Serial}, {s.Modalias, d.Modalias},
		{s.UUID, d.UUID}, {s.WWID, d.WWID}, {s.BusPath, d.BusPath},
	} {
		if f.pattern != "" && !glob.Glob(f.pattern, f.value) {
			return false
		}
	}

	switch s.Type {
	case TypeNVMe:
		return d.Transport == "nvme"
	case TypeSD:
		return d.Transport == "mmc"
	case TypeHDD:
		return d.Rotational
	case TypeSSD:
		return !d.Rotational
	}

	return true
}

// Select returns the first disk matching sel in kernel-name order, the way
// Talos takes the first matched disk, along with every candidate.
func Select(all []Disk, sel *Selector) (Disk, []Disk, error) {
	sorted := slices.Clone(all)
	SortByName(sorted)

	var candidates []Disk

	for _, d := range sorted {
		if sel.Match(d) {
			candidates = append(candidates, d)
		}
	}

	if len(candidates) == 0 {
		return Disk{}, nil, fmt.Errorf("no disk matches selector %s", sel)
	}

	return candidates[0], candidates, nil
}

// SelectPath returns the eligible disk at path, following symlinks such as
// /dev/disk/by-id/... to the kernel device node.
func SelectPath(all []Disk, path string) (Disk, error) {
	resolved := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		resolved = r
	}

	for _, d := range all {
		if d.DevPath != resolved && d.DevPath != path {
			continue
		}

		if !Eligible(d) {
			return Disk{}, fmt.Errorf("disk %s is not eligible for installation (transport %q, readonly %v, cdrom %v)", d.DevPath, d.Transport, d.Readonly, d.CDROM)
		}

		return d, nil
	}

	return Disk{}, fmt.Errorf("no disk with device path %s (resolved %s) found", path, resolved)
}
