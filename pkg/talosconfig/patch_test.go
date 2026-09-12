package talosconfig

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const patchInput = `version: v1alpha1
machine:
  type: worker
  install:
    disk: /dev/sda
    image: ghcr.io/siderolabs/installer:v1.14.0
  kernel:
    modules:
      - name: br_netfilter
      - name: nvidia
cluster:
  clusterName: demo
---
apiVersion: v1alpha1
kind: HostnameConfig
hostname: keep-me
`

func TestPatchSetsImageAndAppendsModules(t *testing.T) {
	patched, patch, err := Patch(patchInput, Overrides{
		InstallImage:  "factory.talos.dev/metal-installer/abc:v1.14.1",
		KernelModules: []string{"nvidia", "nvidia_uvm"},
	})
	if err != nil {
		t.Fatalf("Patch() error: %v", err)
	}

	docs := strings.Split(patched, "\n---\n")
	if len(docs) != 2 {
		t.Fatalf("expected two documents, got %d:\n%s", len(docs), patched)
	}
	if !strings.Contains(docs[1], "hostname: keep-me") {
		t.Fatalf("second document must be preserved:\n%s", docs[1])
	}

	var doc struct {
		Version string `yaml:"version"`
		Machine struct {
			Type    string `yaml:"type"`
			Install struct {
				Disk  string `yaml:"disk"`
				Image string `yaml:"image"`
			} `yaml:"install"`
			Kernel struct {
				Modules []struct {
					Name string `yaml:"name"`
				} `yaml:"modules"`
			} `yaml:"kernel"`
		} `yaml:"machine"`
		Cluster struct {
			ClusterName string `yaml:"clusterName"`
		} `yaml:"cluster"`
	}
	if err := yaml.Unmarshal([]byte(docs[0]), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Machine.Install.Image != "factory.talos.dev/metal-installer/abc:v1.14.1" {
		t.Errorf("install.image = %q", doc.Machine.Install.Image)
	}
	if doc.Machine.Install.Disk != "/dev/sda" || doc.Machine.Type != "worker" || doc.Cluster.ClusterName != "demo" || doc.Version != "v1alpha1" {
		t.Errorf("unrelated fields changed: %+v", doc)
	}
	var names []string
	for _, m := range doc.Machine.Kernel.Modules {
		names = append(names, m.Name)
	}
	if strings.Join(names, ",") != "br_netfilter,nvidia,nvidia_uvm" {
		t.Errorf("kernel.modules = %v", names)
	}

	if !strings.Contains(patch, "image: factory.talos.dev/metal-installer/abc:v1.14.1") || !strings.Contains(patch, "- name: nvidia_uvm") {
		t.Errorf("standalone patch:\n%s", patch)
	}
	if strings.Contains(patch, "br_netfilter") {
		t.Errorf("standalone patch must only carry the overrides:\n%s", patch)
	}
}

func TestPatchCreatesMissingSections(t *testing.T) {
	patched, _, err := Patch("version: v1alpha1\nmachine:\n  type: worker\n", Overrides{InstallImage: "x/metal-installer/a:v1", KernelModules: []string{"nvidia"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"install:", "image: x/metal-installer/a:v1", "kernel:", "modules:", "- name: nvidia"} {
		if !strings.Contains(patched, want) {
			t.Errorf("expected %q in:\n%s", want, patched)
		}
	}
}

func TestPatchWithoutModulesLeavesKernelAlone(t *testing.T) {
	patched, patch, err := Patch("version: v1alpha1\nmachine:\n  type: worker\n", Overrides{InstallImage: "x/metal-installer/a:v1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(patched, "kernel:") || strings.Contains(patch, "kernel:") {
		t.Fatalf("no kernel section expected:\n%s\n%s", patched, patch)
	}
}

func TestPatchRequiresV1alpha1Document(t *testing.T) {
	if _, _, err := Patch("apiVersion: v1alpha1\nkind: HostnameConfig\n", Overrides{InstallImage: "x"}); err == nil {
		t.Fatal("expected an error without a machine configuration document")
	}
}
