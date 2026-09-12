package hardware

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Annotation keys the actions read from and write to the Hardware object.
const (
	// AnnotationExtensions lists extra official extensions, comma separated.
	AnnotationExtensions = "talos.tinkerbell.org/system-extensions"
	// AnnotationOverlay carries the Image Factory overlay as name@image.
	AnnotationOverlay = "talos.tinkerbell.org/overlay"
	// AnnotationContract pins the Talos minor a machine tracks, e.g. v1.14.
	AnnotationContract = "talos.tinkerbell.org/contract"
	// AnnotationInstallerImage is the installer reference matching the installed schematic.
	AnnotationInstallerImage = "talos.tinkerbell.org/installer-image"
	// AnnotationConfigPatch holds the machine configuration overrides talos2disk applied, as YAML.
	AnnotationConfigPatch = "talos.tinkerbell.org/config-patch"
	// AnnotationUserDataOwner marks spec.userData as written by talos2disk so CAPT leaves it alone.
	AnnotationUserDataOwner = "talos.tinkerbell.org/userdata-owner"
	// UserDataOwnerTalos2disk is the value talos2disk writes to AnnotationUserDataOwner.
	UserDataOwnerTalos2disk = "talos2disk"
)

// Hardware is the Tinkerbell Hardware object as a Workflow Template renders
// it with `.hardware | toJson`: JSON field names, object metadata, spec and
// status.
type Hardware struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     Spec       `json:"spec"`
	Status   *Status    `json:"status,omitempty"`
}

// ObjectMeta is the subset of Kubernetes object metadata the actions use.
type ObjectMeta struct {
	Name        string            `json:"name,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Status is the Hardware status.
type Status struct {
	Attributes *Attributes `json:"attributes,omitempty"`
}

// Attributes holds the hardware attribute subtrees.
type Attributes struct {
	OutOfBand *Inventory `json:"outOfBand,omitempty"`
}

// Inventory is the machine description collected through the BMC.
type Inventory struct {
	CollectionMethod string        `json:"collectionMethod,omitempty"`
	CPU              *CPU          `json:"cpu,omitempty"`
	GPUDevices       []GPUDevice   `json:"gpuDevices,omitempty"`
	BlockDevices     []BlockDevice `json:"blockDevices,omitempty"`
}

// CPU holds the per-socket CPU detail.
type CPU struct {
	Sockets []CPUSocket `json:"sockets,omitempty"`
}

// CPUSocket describes one physical CPU.
type CPUSocket struct {
	Slot   string `json:"slot,omitempty"`
	Vendor string `json:"vendor,omitempty"`
	Model  string `json:"model,omitempty"`
}

// GPUDevice describes a GPU or accelerator.
type GPUDevice struct {
	Vendor      string `json:"vendor,omitempty"`
	Model       string `json:"model,omitempty"`
	Description string `json:"description,omitempty"`
}

// BlockDevice describes a storage drive as the BMC reports it.
type BlockDevice struct {
	Name           string `json:"name,omitempty"`
	ControllerType string `json:"controllerType,omitempty"`
	DriveType      string `json:"driveType,omitempty"`
	Vendor         string `json:"vendor,omitempty"`
	Model          string `json:"model,omitempty"`
	SerialNumber   string `json:"serialNumber,omitempty"`
	WWN            string `json:"wwn,omitempty"`
	SizeBytes      int64  `json:"sizeBytes,omitempty"`
}

// Parse decodes a Hardware object rendered as JSON.
func Parse(raw []byte) (*Hardware, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("hardware document is empty")
	}

	var hw Hardware
	if err := json.Unmarshal(raw, &hw); err != nil {
		return nil, fmt.Errorf("decoding hardware document: %w", err)
	}

	return &hw, nil
}

// Annotation returns the trimmed value of an annotation, or "".
func (h *Hardware) Annotation(key string) string {
	return strings.TrimSpace(h.Metadata.Annotations[key])
}

// UserData returns spec.userData, or "".
func (h *Hardware) UserData() string {
	if h.Spec.UserData == nil {
		return ""
	}

	return *h.Spec.UserData
}

// OutOfBand returns the BMC-collected inventory, or nil.
func (h *Hardware) OutOfBand() *Inventory {
	if h.Status == nil || h.Status.Attributes == nil {
		return nil
	}

	return h.Status.Attributes.OutOfBand
}

// OperatingSystemVersion returns metadata.instance.operating_system.version, or "".
func (h *Hardware) OperatingSystemVersion() string {
	if h.Spec.Metadata == nil || h.Spec.Metadata.Instance == nil || h.Spec.Metadata.Instance.OperatingSystem == nil {
		return ""
	}

	return strings.TrimSpace(h.Spec.Metadata.Instance.OperatingSystem.Version)
}

// FirstDisk returns spec.disks[0].device, or "".
func (h *Hardware) FirstDisk() string {
	for _, d := range h.Spec.Disks {
		if d.Device != "" {
			return d.Device
		}
	}

	return ""
}

// OperatingSystemSlug returns metadata.instance.operating_system.slug, the schematic ID a
// resolver may have written onto the Hardware, or "".
func (h *Hardware) OperatingSystemSlug() string {
	if h.Spec.Metadata == nil || h.Spec.Metadata.Instance == nil || h.Spec.Metadata.Instance.OperatingSystem == nil {
		return ""
	}

	return strings.TrimSpace(h.Spec.Metadata.Instance.OperatingSystem.Slug)
}
