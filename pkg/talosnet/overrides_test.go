package talosnet

import (
	"strings"
	"testing"

	"github.com/sidero-community/actions/pkg/hardware"
)

func overrideSpec() *hardware.Spec {
	return &hardware.Spec{
		Interfaces: []hardware.Interface{{DHCP: &hardware.DHCP{
			MAC: "52:54:00:12:34:01", IfaceName: "eth0", Hostname: "dhcp-name", DomainName: "dhcp.example",
			NameServers: []string{"10.0.0.2"}, TimeServers: []string{"10.0.0.3"},
			IP:          &hardware.IP{Address: "10.0.80.10", Netmask: "255.255.255.0", Gateway: "10.0.80.1", Family: 4},
		}}},
		Metadata: &hardware.Metadata{Instance: &hardware.Instance{Hostname: "inst.example.com"}},
	}
}

func TestOverridesWinOverHardwareData(t *testing.T) {
	cfg, err := FromHardwareWithOverrides(overrideSpec(), nil, Overrides{
		Hostname:    "cfg-name.corp.example",
		Nameservers: []string{"1.1.1.1", "1.0.0.1"},
		TimeServers: []string{"time.example"},
	})
	if err != nil {
		t.Fatalf("FromHardwareWithOverrides() error: %v", err)
	}
	if len(cfg.Hostnames) != 1 || cfg.Hostnames[0].Hostname != "cfg-name" || cfg.Hostnames[0].Domainname != "corp.example" {
		t.Errorf("hostnames = %+v", cfg.Hostnames)
	}
	if len(cfg.Resolvers) != 1 || strings.Join(cfg.Resolvers[0].DNSServers, ",") != "1.1.1.1,1.0.0.1" {
		t.Errorf("resolvers = %+v", cfg.Resolvers)
	}
	if len(cfg.TimeServers) != 1 || strings.Join(cfg.TimeServers[0].Servers, ",") != "time.example" {
		t.Errorf("timeServers = %+v", cfg.TimeServers)
	}
	if len(cfg.Addresses) != 1 || cfg.Addresses[0].LinkName != "eth0" {
		t.Errorf("addresses = %+v", cfg.Addresses)
	}
}

func TestEmptyOverridesKeepHardwarePrecedence(t *testing.T) {
	cfg, err := FromHardware(overrideSpec(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hostnames[0].Hostname != "inst" || cfg.Hostnames[0].Domainname != "dhcp.example" {
		t.Errorf("hostnames = %+v", cfg.Hostnames)
	}
	if strings.Join(cfg.Resolvers[0].DNSServers, ",") != "10.0.0.2" || strings.Join(cfg.TimeServers[0].Servers, ",") != "10.0.0.3" {
		t.Errorf("resolvers/timeServers = %+v / %+v", cfg.Resolvers, cfg.TimeServers)
	}
}

func TestHasStaticAddress(t *testing.T) {
	if !HasStaticAddress(overrideSpec()) {
		t.Fatal("expected a static address")
	}
	dhcpOnly := &hardware.Spec{Interfaces: []hardware.Interface{{DHCP: &hardware.DHCP{MAC: "52:54:00:12:34:02"}}}}
	if HasStaticAddress(dhcpOnly) || HasStaticAddress(&hardware.Spec{}) || HasStaticAddress(nil) {
		t.Fatal("expected no static address")
	}
}
