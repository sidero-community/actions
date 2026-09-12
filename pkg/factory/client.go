package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// DefaultURL is the public Image Factory.
const DefaultURL = "https://factory.talos.dev"

const maxBody = 1 << 20

// Client calls the Image Factory HTTP API.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient validates baseURL (http or https) and returns a client. A nil
// httpClient uses http.DefaultClient.
func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("factory URL %q must be an http(s) URL", baseURL)
	}

	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Client{baseURL: strings.TrimRight(u.String(), "/"), http: httpClient}, nil
}

// Registration is the response of POST /schematics.
type Registration struct {
	ID string `json:"id"`
	// Schematic is the canonical body the Factory stored; it is authoritative.
	Schematic string `json:"schematic"`
}

// CreateSchematic registers s. The Factory computes the content-addressed
// ID, so registering the same schematic twice returns the same ID.
func (c *Client) CreateSchematic(ctx context.Context, s Schematic) (Registration, error) {
	body, err := s.Marshal()
	if err != nil {
		return Registration{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/schematics", bytes.NewReader(body))
	if err != nil {
		return Registration{}, err
	}

	req.Header.Set("Content-Type", "application/yaml")
	req.Header.Set("Accept", "application/json")

	raw, err := c.do(req)
	if err != nil {
		return Registration{}, fmt.Errorf("registering schematic: %w", err)
	}

	var reg Registration
	if err := json.Unmarshal(raw, &reg); err != nil {
		return Registration{}, fmt.Errorf("decoding schematic registration: %w", err)
	}

	if reg.ID == "" {
		return Registration{}, fmt.Errorf("factory returned no schematic ID: %s", strings.TrimSpace(string(raw)))
	}

	return reg, nil
}

// GetSchematic fetches a stored schematic by ID.
func (c *Client) GetSchematic(ctx context.Context, id string) (Schematic, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/schematics/"+url.PathEscape(id), nil)
	if err != nil {
		return Schematic{}, err
	}

	raw, err := c.do(req)
	if err != nil {
		return Schematic{}, fmt.Errorf("fetching schematic %s: %w", id, err)
	}

	return ParseSchematic(raw)
}

// Versions lists the Talos versions the Factory serves, excluding those it
// marks broken.
func (c *Client) Versions(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/versions", nil)
	if err != nil {
		return nil, err
	}

	raw, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("listing factory versions: %w", err)
	}

	var versions []string
	if err := json.Unmarshal(raw, &versions); err != nil {
		return nil, fmt.Errorf("decoding factory versions: %w", err)
	}

	return versions, nil
}

// ImageURL is the download URL of the raw metal disk image.
func (c *Client) ImageURL(id, version, arch string) string {
	return fmt.Sprintf("%s/image/%s/%s/metal-%s.raw.zst", c.baseURL, id, version, arch)
}

// InstallerImage is the installer reference for machine.install.image.
func (c *Client) InstallerImage(id, version string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(c.baseURL, "https://"), "http://")

	return fmt.Sprintf("%s/metal-installer/%s:%s", host, id, version)
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("%s %s returned %s: %s", req.Method, req.URL, res.Status, strings.TrimSpace(string(raw)))
	}

	return raw, nil
}

var installerReference = regexp.MustCompile(`^([^/]+)/(?:metal-installer|installer)/([0-9a-f]{64})(?::([^@/]+))?(?:@.*)?$`)

// InstallerReference is a parsed Image Factory installer reference such as
// factory.talos.dev/metal-installer/<schematic id>:<version>.
type InstallerReference struct {
	// Host is the registry host, which is also the Factory's host.
	Host string
	// ID is the 64-hex-character schematic ID.
	ID string
	// Tag is the image tag, normally a Talos version; empty when the reference has none.
	Tag string
}

// ParseInstallerReference recognises Factory installer references and returns their parts.
func ParseInstallerReference(ref string) (InstallerReference, bool) {
	m := installerReference.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return InstallerReference{}, false
	}

	return InstallerReference{Host: m[1], ID: m[2], Tag: m[3]}, true
}
