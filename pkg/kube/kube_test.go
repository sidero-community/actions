package kube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sidero-community/actions/pkg/hardware"
)

func TestBuildPatch(t *testing.T) {
	userData := "version: v1alpha1\nmachine: {}\n"
	body, err := BuildPatch(PatchInput{InstallerImage: "factory.talos.dev/metal-installer/abc:v1.14.1", ConfigPatch: "machine:\n  install:\n    image: x\n", UserData: &userData})
	if err != nil {
		t.Fatalf("BuildPatch() error: %v", err)
	}
	var got struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec *struct {
			UserData string `json:"userData"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	ann := got.Metadata.Annotations
	if ann[hardware.AnnotationInstallerImage] != "factory.talos.dev/metal-installer/abc:v1.14.1" || ann[hardware.AnnotationConfigPatch] != "machine:\n  install:\n    image: x\n" || ann[hardware.AnnotationUserDataOwner] != hardware.UserDataOwnerTalos2disk {
		t.Fatalf("annotations = %v", ann)
	}
	if got.Spec == nil || got.Spec.UserData != userData {
		t.Fatalf("spec = %+v", got.Spec)
	}

	body, err = BuildPatch(PatchInput{InstallerImage: "x/metal-installer/abc:v1"})
	if err != nil {
		t.Fatal(err)
	}
	var bare struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec *json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(body, &bare); err != nil {
		t.Fatal(err)
	}
	if bare.Spec != nil {
		t.Fatal("spec must be absent without userData")
	}
	if _, ok := bare.Metadata.Annotations[hardware.AnnotationUserDataOwner]; ok {
		t.Fatal("owner annotation must only be written together with userData")
	}
	if _, ok := bare.Metadata.Annotations[hardware.AnnotationConfigPatch]; ok {
		t.Fatal("empty config patch must not be written")
	}

	if _, err := BuildPatch(PatchInput{}); err == nil {
		t.Fatal("expected an error without an installer image")
	}
}

func writeKubeconfig(t *testing.T, server string) string {
	t.Helper()
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	content := "apiVersion: v1\nkind: Config\nclusters:\n- name: t\n  cluster:\n    server: " + server + "\ncontexts:\n- name: t\n  context:\n    cluster: t\n    user: t\ncurrent-context: t\nusers:\n- name: t\n  user:\n    token: secret\n"
	if err := os.WriteFile(kubeconfig, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return kubeconfig
}

func TestPatchHardwareSendsMergePatch(t *testing.T) {
	var (
		gotMethod, gotPath, gotContentType, gotManager string
		gotBody                                          []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotContentType, gotManager = r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.URL.Query().Get("fieldManager")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"tinkerbell.org/v1alpha1","kind":"Hardware","metadata":{"name":"node1","namespace":"tinkerbell"}}`))
	}))
	defer srv.Close()

	c, err := NewClientFromKubeconfig(writeKubeconfig(t, srv.URL))
	if err != nil {
		t.Fatalf("NewClientFromKubeconfig() error: %v", err)
	}
	patch := []byte(`{"metadata":{"annotations":{"a":"b"}}}`)
	if err := c.PatchHardware(context.Background(), "tinkerbell", "node1", patch); err != nil {
		t.Fatalf("PatchHardware() error: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/apis/tinkerbell.org/v1alpha1/namespaces/tinkerbell/hardware/node1" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if gotContentType != "application/merge-patch+json" || gotManager != FieldManager {
		t.Fatalf("content type %q, field manager %q", gotContentType, gotManager)
	}
	if string(gotBody) != string(patch) {
		t.Fatalf("body = %s", gotBody)
	}

	if err := c.PatchHardware(context.Background(), "", "node1", patch); err == nil {
		t.Fatal("expected an error without a namespace")
	}
}

func TestPatchHardwareSurfacesAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","message":"hardware.tinkerbell.org \"node1\" is forbidden","reason":"Forbidden","code":403}`))
	}))
	defer srv.Close()

	c, err := NewClientFromKubeconfig(writeKubeconfig(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PatchHardware(context.Background(), "tinkerbell", "node1", []byte(`{}`)); err == nil {
		t.Fatal("expected the 403 to surface")
	}
}

func TestNewClientFromKubeconfigRejectsMissingFile(t *testing.T) {
	if _, err := NewClientFromKubeconfig(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected an error for a missing kubeconfig")
	}
}
