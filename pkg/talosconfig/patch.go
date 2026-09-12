package talosconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Overrides are the machine configuration edits talos2disk applies and writes back.
type Overrides struct {
	// InstallImage replaces machine.install.image.
	InstallImage string
	// KernelModules are appended to machine.kernel.modules by name, without duplicates.
	KernelModules []string
}

// Patch applies o to the v1alpha1 document inside userData and returns the
// full document set with the edit applied, plus the standalone patch document
// (only the overrides) for the config-patch annotation. Other documents and
// fields are preserved.
func Patch(userData string, o Overrides) (patched, patchDoc string, err error) {
	dec := yaml.NewDecoder(strings.NewReader(userData))

	var docs []*yaml.Node

	found := false

	for {
		var node yaml.Node

		err := dec.Decode(&node)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return "", "", fmt.Errorf("parsing machine configuration: %w", err)
		}

		if isMachineConfig(&node) {
			if err := apply(&node, o); err != nil {
				return "", "", err
			}

			found = true
		}

		docs = append(docs, &node)
	}

	if !found {
		return "", "", errors.New("no v1alpha1 machine configuration document to patch")
	}

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return "", "", fmt.Errorf("encoding machine configuration: %w", err)
		}
	}

	if err := enc.Close(); err != nil {
		return "", "", fmt.Errorf("encoding machine configuration: %w", err)
	}

	patchDoc, err = RenderPatch(o)
	if err != nil {
		return "", "", err
	}

	return buf.String(), patchDoc, nil
}

// RenderPatch renders only the overrides as a machine configuration fragment.
func RenderPatch(o Overrides) (string, error) {
	type module struct {
		Name string `yaml:"name"`
	}

	type kernel struct {
		Modules []module `yaml:"modules"`
	}

	type install struct {
		Image string `yaml:"image"`
	}

	type machine struct {
		Install *install `yaml:"install,omitempty"`
		Kernel  *kernel  `yaml:"kernel,omitempty"`
	}

	var m machine

	if o.InstallImage != "" {
		m.Install = &install{Image: o.InstallImage}
	}

	if len(o.KernelModules) > 0 {
		k := &kernel{}
		for _, name := range o.KernelModules {
			k.Modules = append(k.Modules, module{Name: name})
		}

		m.Kernel = k
	}

	out, err := yaml.Marshal(struct {
		Machine machine `yaml:"machine"`
	}{Machine: m})
	if err != nil {
		return "", fmt.Errorf("rendering config patch: %w", err)
	}

	return string(out), nil
}

// isMachineConfig reports whether a document node is a v1alpha1 document with a machine key.
func isMachineConfig(doc *yaml.Node) bool {
	root := mappingRoot(doc)
	if root == nil {
		return false
	}

	version := mappingGet(root, "version")

	return version != nil && version.Value == "v1alpha1" && mappingGet(root, "machine") != nil
}

func apply(doc *yaml.Node, o Overrides) error {
	root := mappingRoot(doc)
	machine := mappingEnsure(root, "machine")

	if o.InstallImage != "" {
		install := mappingEnsure(machine, "install")
		setScalar(install, "image", o.InstallImage)
	}

	if len(o.KernelModules) > 0 {
		kernel := mappingEnsure(machine, "kernel")
		modules := mappingGet(kernel, "modules")

		if modules == nil || modules.Kind != yaml.SequenceNode {
			modules = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			kernel.Content = append(kernel.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "modules"}, modules)
		}

		present := map[string]bool{}

		for _, entry := range modules.Content {
			if name := mappingGet(entry, "name"); name != nil {
				present[name.Value] = true
			}
		}

		for _, name := range o.KernelModules {
			if present[name] {
				continue
			}

			present[name] = true
			modules.Content = append(modules.Content, &yaml.Node{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
				Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
				},
			})
		}
	}

	return nil
}

// mappingRoot returns the mapping node of a document, or nil.
func mappingRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = doc.Content[0]
	}

	if doc.Kind != yaml.MappingNode {
		return nil
	}

	return doc
}

// mappingGet returns the value node for key in a mapping, or nil.
func mappingGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}

	return nil
}

// mappingEnsure returns the mapping under key, creating it when absent or
// when the existing value is not a mapping (an empty install: {} is replaced in place).
func mappingEnsure(m *yaml.Node, key string) *yaml.Node {
	if v := mappingGet(m, key); v != nil {
		if v.Kind == yaml.MappingNode {
			return v
		}

		v.Kind = yaml.MappingNode
		v.Tag = "!!map"
		v.Value = ""
		v.Content = nil

		return v
	}

	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)

	return v
}

func setScalar(m *yaml.Node, key, value string) {
	if v := mappingGet(m, key); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!str"
		v.Value = value
		v.Content = nil

		return
	}

	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}
