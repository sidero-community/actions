// Package kube writes talos2disk's results back to the Hardware object.
package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sidero-community/actions/pkg/hardware"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

// FieldManager attributes the patch in managedFields.
const FieldManager = "talos2disk"

// HardwareGVR addresses Tinkerbell Hardware objects.
var HardwareGVR = schema.GroupVersionResource{Group: "tinkerbell.org", Version: "v1alpha1", Resource: "hardware"}

// PatchInput is what talos2disk records on the Hardware.
type PatchInput struct {
	// InstallerImage is the Factory installer reference of the installed schematic.
	InstallerImage string
	// ConfigPatch is the overrides document for the config-patch annotation; empty omits it.
	ConfigPatch string
	// UserData is the patched machine configuration; nil leaves spec.userData alone
	// and omits the owner annotation.
	UserData *string
}

// BuildPatch renders a JSON merge patch for the Hardware.
func BuildPatch(in PatchInput) ([]byte, error) {
	if in.InstallerImage == "" {
		return nil, errors.New("installer image is required to patch the Hardware")
	}

	annotations := map[string]string{hardware.AnnotationInstallerImage: in.InstallerImage}

	if in.ConfigPatch != "" {
		annotations[hardware.AnnotationConfigPatch] = in.ConfigPatch
	}

	body := map[string]any{"metadata": map[string]any{"annotations": annotations}}

	if in.UserData != nil {
		annotations[hardware.AnnotationUserDataOwner] = hardware.UserDataOwnerTalos2disk
		body["spec"] = map[string]any{"userData": *in.UserData}
	}

	out, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encoding hardware patch: %w", err)
	}

	return out, nil
}

// Client patches Hardware objects through the dynamic client.
type Client struct {
	dyn dynamic.Interface
}

// NewClientFromKubeconfig loads the kubeconfig at path and returns a client.
func NewClientFromKubeconfig(path string) (*Client, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig %s: %w", path, err)
	}

	cfg.UserAgent = FieldManager

	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	return &Client{dyn: dyn}, nil
}

// PatchHardware applies a JSON merge patch to the named Hardware.
func (c *Client) PatchHardware(ctx context.Context, namespace, name string, patch []byte) error {
	if namespace == "" || name == "" {
		return errors.New("hardware namespace and name are required for the write-back")
	}

	_, err := c.dyn.Resource(HardwareGVR).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: FieldManager})
	if err != nil {
		return fmt.Errorf("patching hardware %s/%s: %w", namespace, name, err)
	}

	return nil
}
