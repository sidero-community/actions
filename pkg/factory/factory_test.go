package factory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newFactoryServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/versions" {
			_, _ = w.Write([]byte(`["v1.13.9","v1.14.0","v1.14.2","v1.14.1","v1.15.0-alpha.1"]`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClientVersionsAndImageURL(t *testing.T) {
	srv := newFactoryServer(t)
	c, err := NewClient(srv.URL+"/", srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	versions, err := c.Versions(context.Background())
	if err != nil || len(versions) != 5 {
		t.Fatalf("Versions() = %v, %v", versions, err)
	}

	if got := c.ImageURL(testID, "v1.14.2", "amd64"); got != srv.URL+"/image/"+testID+"/v1.14.2/metal-amd64.raw.zst" {
		t.Fatalf("ImageURL() = %q", got)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusBadGateway) }))
	defer down.Close()
	c2, _ := NewClient(down.URL, down.Client())
	if _, err := c2.Versions(context.Background()); err == nil {
		t.Fatal("expected an error on a non-2xx response")
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
	tests := []struct {
		ref  string
		want InstallerReference
		ok   bool
	}{
		{"factory.talos.dev/metal-installer/" + testID + ":v1.14.1", InstallerReference{Host: "factory.talos.dev", ID: testID, Tag: "v1.14.1"}, true},
		{"factory.talos.dev/installer/" + testID + ":v1.14.1", InstallerReference{Host: "factory.talos.dev", ID: testID, Tag: "v1.14.1"}, true},
		{"factory.example.test:8443/metal-installer/" + testID, InstallerReference{Host: "factory.example.test:8443", ID: testID}, true},
		{"factory.talos.dev/metal-installer/" + testID + ":v1.14.1@sha256:abcd", InstallerReference{Host: "factory.talos.dev", ID: testID, Tag: "v1.14.1"}, true},
		{"ghcr.io/siderolabs/installer:v1.14.1", InstallerReference{}, false},
		{"factory.talos.dev/metal-installer/short:v1.14.1", InstallerReference{}, false},
		{"", InstallerReference{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseInstallerReference(tt.ref)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseInstallerReference(%q) = %+v, %v; want %+v, %v", tt.ref, got, ok, tt.want, tt.ok)
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
