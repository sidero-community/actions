package factory

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestBuildDedupesSortsAndMarshals(t *testing.T) {
	s := Build([]string{"siderolabs/nvme-cli", "siderolabs/amd-ucode", "siderolabs/nvme-cli"}, "sd-boot", &Overlay{Name: "rpi_generic", Image: "ghcr.io/siderolabs/sbc-raspberrypi"})
	if strings.Join(s.Extensions(), ",") != "siderolabs/amd-ucode,siderolabs/nvme-cli" {
		t.Fatalf("Extensions() = %v", s.Extensions())
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	want := "overlay:\n    image: ghcr.io/siderolabs/sbc-raspberrypi\n    name: rpi_generic\ncustomization:\n    systemExtensions:\n        officialExtensions:\n            - siderolabs/amd-ucode\n            - siderolabs/nvme-cli\n    bootloader: sd-boot\n"
	if string(out) != want {
		t.Fatalf("Marshal() =\n%s\nwant\n%s", out, want)
	}

	empty, err := Build(nil, "", nil).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != "customization: {}\n" {
		t.Fatalf("empty schematic = %q", empty)
	}
}

func TestParseOverlay(t *testing.T) {
	if o, err := ParseOverlay(""); err != nil || o != nil {
		t.Fatalf("ParseOverlay('') = %+v, %v", o, err)
	}
	o, err := ParseOverlay(" rpi_generic @ ghcr.io/siderolabs/sbc-raspberrypi ")
	if err != nil || o.Name != "rpi_generic" || o.Image != "ghcr.io/siderolabs/sbc-raspberrypi" {
		t.Fatalf("ParseOverlay() = %+v, %v", o, err)
	}
	for _, bad := range []string{"rpi_generic", "@image", "name@"} {
		if _, err := ParseOverlay(bad); err == nil {
			t.Errorf("ParseOverlay(%q) accepted", bad)
		}
	}
}

func TestParseSchematicAndMissingExtensions(t *testing.T) {
	have, err := ParseSchematic([]byte("customization:\n    systemExtensions:\n        officialExtensions:\n            - siderolabs/util-linux-tools\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := Build([]string{"siderolabs/util-linux-tools", "siderolabs/intel-ucode", "siderolabs/nvme-cli"}, "", nil)
	if got := MissingExtensions(want, have); strings.Join(got, ",") != "siderolabs/intel-ucode,siderolabs/nvme-cli" {
		t.Fatalf("MissingExtensions() = %v", got)
	}
	if got := MissingExtensions(have, want); len(got) != 0 {
		t.Fatalf("expected nothing missing, got %v", got)
	}
}

func newFactoryServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/schematics":
			if ct := r.Header.Get("Content-Type"); ct != "application/yaml" {
				http.Error(w, "content type "+ct, http.StatusUnsupportedMediaType)
				return
			}
			body, _ := io.ReadAll(r.Body)
			bodies = append(bodies, string(body))
			if strings.Contains(string(body), "siderolabs/does-not-exist") {
				http.Error(w, `{"error":"unknown extension"}`, http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"` + testID + `","schematic":"customization:\n    systemExtensions:\n        officialExtensions:\n            - siderolabs/nvme-cli\n"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/schematics/"+testID:
			_, _ = w.Write([]byte("customization:\n    systemExtensions:\n        officialExtensions:\n            - siderolabs/nvme-cli\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/versions":
			_, _ = w.Write([]byte(`["v1.13.9","v1.14.0","v1.14.2","v1.14.1","v1.15.0-alpha.1"]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestClientCreateGetVersionsAndURLs(t *testing.T) {
	srv, bodies := newFactoryServer(t)
	c, err := NewClient(srv.URL+"/", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	reg, err := c.CreateSchematic(ctx, Build([]string{"siderolabs/nvme-cli"}, "", nil))
	if err != nil {
		t.Fatalf("CreateSchematic() error: %v", err)
	}
	if reg.ID != testID || !strings.Contains(reg.Schematic, "siderolabs/nvme-cli") {
		t.Fatalf("registration = %+v", reg)
	}
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], "officialExtensions:") {
		t.Fatalf("posted bodies = %q", *bodies)
	}

	if _, err := c.CreateSchematic(ctx, Build([]string{"siderolabs/does-not-exist"}, "", nil)); err == nil || !strings.Contains(err.Error(), "unknown extension") {
		t.Fatalf("expected the Factory error to surface, got %v", err)
	}

	s, err := c.GetSchematic(ctx, testID)
	if err != nil || strings.Join(s.Extensions(), ",") != "siderolabs/nvme-cli" {
		t.Fatalf("GetSchematic() = %+v, %v", s, err)
	}
	if _, err := c.GetSchematic(ctx, "missing"); err == nil {
		t.Fatal("expected an error for an unknown schematic")
	}

	versions, err := c.Versions(ctx)
	if err != nil || len(versions) != 5 {
		t.Fatalf("Versions() = %v, %v", versions, err)
	}

	if got := c.ImageURL(testID, "v1.14.2", "amd64"); got != srv.URL+"/image/"+testID+"/v1.14.2/metal-amd64.raw.zst" {
		t.Fatalf("ImageURL() = %q", got)
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	if got := c.InstallerImage(testID, "v1.14.2"); got != host+"/metal-installer/"+testID+":v1.14.2" {
		t.Fatalf("InstallerImage() = %q", got)
	}
}

func TestNewClientRejectsBadURL(t *testing.T) {
	for _, bad := range []string{"", "factory.talos.dev", "ftp://x"} {
		if _, err := NewClient(bad, nil); err == nil {
			t.Errorf("NewClient(%q) accepted", bad)
		}
	}
}

func TestParseInstallerReference(t *testing.T) {
	for ref, want := range map[string]string{
		"factory.talos.dev/metal-installer/" + testID + ":v1.14.1": testID,
		"factory.talos.dev/installer/" + testID + ":v1.14.1":       testID,
		"factory.example.test/metal-installer/" + testID:           testID,
		"ghcr.io/siderolabs/installer:v1.14.1":                     "",
		"factory.talos.dev/metal-installer/short:v1.14.1":          "",
		"":                                                          "",
	} {
		id, ok := ParseInstallerReference(ref)
		if id != want || ok != (want != "") {
			t.Errorf("ParseInstallerReference(%q) = %q, %v", ref, id, ok)
		}
	}
}

func TestResolveVersion(t *testing.T) {
	versions := []string{"v1.13.9", "v1.14.0", "v1.14.2", "v1.14.1", "v1.15.0-alpha.1", "v1.14.10"}
	tests := []struct {
		in, want string
		hasErr   bool
	}{
		{"v1.14.1", "v1.14.1", false},
		{"v1.14.3-beta.0", "v1.14.3-beta.0", false},
		{"v1.14", "v1.14.10", false},
		{"v1.13", "v1.13.9", false},
		{"v1.15", "", true},
		{"1.14", "", true},
		{"latest", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := ResolveVersion(tt.in, versions)
		if (err != nil) != tt.hasErr || got != tt.want {
			t.Errorf("ResolveVersion(%q) = %q, %v; want %q, err=%v", tt.in, got, err, tt.want, tt.hasErr)
		}
	}
	if !IsFullVersion("v1.14.2") || IsFullVersion("v1.14") || !IsBareMinor("v1.14") || IsBareMinor("v1.14.2") {
		t.Fatal("version classifiers mismatch")
	}
}
