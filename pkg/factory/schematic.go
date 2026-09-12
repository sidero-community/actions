// Package factory talks to the Talos Image Factory: schematic registration,
// version listing and image URLs.
package factory

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Schematic is the Image Factory schematic document. Field order follows the
// Factory's own type so the marshalled body reads like the Factory's.
type Schematic struct {
	Overlay       *Overlay      `yaml:"overlay,omitempty"`
	Customization Customization `yaml:"customization"`
}

// Overlay is the Image Factory overlay block.
type Overlay struct {
	Image string `yaml:"image"`
	Name  string `yaml:"name"`
}

// Customization is the schematic customization block. Only the fields this
// action sets are modelled; the Factory canonicalizes the rest.
type Customization struct {
	SystemExtensions SystemExtensions `yaml:"systemExtensions,omitempty"`
	Bootloader       string           `yaml:"bootloader,omitempty"`
}

// SystemExtensions lists official extensions.
type SystemExtensions struct {
	OfficialExtensions []string `yaml:"officialExtensions,omitempty"`
}

// Build assembles a schematic from deduplicated, sorted extensions.
func Build(extensions []string, bootloader string, overlay *Overlay) Schematic {
	seen := map[string]bool{}

	var names []string

	for _, e := range extensions {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}

		seen[e] = true
		names = append(names, e)
	}

	sort.Strings(names)

	return Schematic{
		Overlay: overlay,
		Customization: Customization{
			SystemExtensions: SystemExtensions{OfficialExtensions: names},
			Bootloader:       bootloader,
		},
	}
}

// Extensions returns the official extensions of the schematic.
func (s Schematic) Extensions() []string {
	return s.Customization.SystemExtensions.OfficialExtensions
}

// Marshal renders the schematic as YAML for POST /schematics.
func (s Schematic) Marshal() ([]byte, error) {
	out, err := yaml.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshalling schematic: %w", err)
	}

	return out, nil
}

// ParseSchematic decodes a schematic document as the Factory returns it.
// Fields this package does not model are ignored.
func ParseSchematic(doc []byte) (Schematic, error) {
	var s Schematic
	if err := yaml.Unmarshal(doc, &s); err != nil {
		return Schematic{}, fmt.Errorf("parsing schematic: %w", err)
	}

	return s, nil
}

// ParseOverlay parses "name@image". An empty value yields nil; a value
// missing either part is an error, because the Factory requires both.
func ParseOverlay(value string) (*Overlay, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	name, image, ok := strings.Cut(value, "@")
	name, image = strings.TrimSpace(name), strings.TrimSpace(image)

	if !ok || name == "" || image == "" {
		return nil, errors.New("overlay must be given as name@image")
	}

	return &Overlay{Name: name, Image: image}, nil
}

// MissingExtensions returns the extensions of from that in lacks, sorted.
func MissingExtensions(from, in Schematic) []string {
	present := map[string]bool{}
	for _, e := range in.Extensions() {
		present[e] = true
	}

	var out []string

	for _, e := range from.Extensions() {
		if !present[e] {
			out = append(out, e)
		}
	}

	sort.Strings(out)

	return out
}
