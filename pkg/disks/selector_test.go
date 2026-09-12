package disks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	nvme = Disk{Name: "nvme0n1", DevPath: "/dev/nvme0n1", Size: 1_000_204_886_016, Transport: "nvme", Model: "Samsung SSD 980 PRO 1TB", Serial: "S5GXNX0T", WWID: "eui.0025", BusPath: "/pci0000:00/0000:00:1d.0/0000:03:00.0/nvme/nvme0"}
	sda  = Disk{Name: "sda", DevPath: "/dev/sda", Size: 4_000_787_030_016, Transport: "sata", Rotational: true, Model: "WDC WD40EFRX", Serial: "WD-1"}
	sdb  = Disk{Name: "sdb", DevPath: "/dev/sdb", Size: 32_000_000_000, Transport: "usb", Model: "USB Flash", Readonly: false}
	sr0  = Disk{Name: "sr0", DevPath: "/dev/sr0", Size: 700_000_000, Transport: "sata", CDROM: true}
	loop = Disk{Name: "loop0", DevPath: "/dev/loop0", Size: 5_000_000_000_000, Transport: ""}
	mmc  = Disk{Name: "mmcblk0", DevPath: "/dev/mmcblk0", Size: 128_000_000_000, Transport: "mmc"}
	all  = []Disk{sda, sr0, loop, sdb, nvme, mmc}
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		in     string
		op     string
		size   uint64
		hasErr bool
	}{
		{">= 100GB", ">=", 100_000_000_000, false},
		{">=100GiB", ">=", 107_374_182_400, false},
		{"< 1TB", "<", 1_000_000_000_000, false},
		{"== 32 GB", "==", 32_000_000_000, false},
		{"500GB", "==", 500_000_000_000, false},
		{"> 2 TiB", ">", 2_199_023_255_552, false},
		{"=> 1GB", "", 0, true},
		{"lots", "", 0, true},
		{"", "", 0, true},
	}
	for _, tt := range tests {
		m, err := ParseSize(tt.in)
		if tt.hasErr {
			if err == nil {
				t.Errorf("ParseSize(%q) accepted", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSize(%q) error: %v", tt.in, err)
			continue
		}
		if m.Op != tt.op || m.Size != tt.size {
			t.Errorf("ParseSize(%q) = %+v, want op %q size %d", tt.in, m, tt.op, tt.size)
		}
	}
}

func TestSizeMatcherOps(t *testing.T) {
	for _, tt := range []struct {
		op   string
		size uint64
		want bool
	}{
		{">=", 100, true},
		{">=", 99, false},
		{"<=", 100, true},
		{"<=", 101, false},
		{">", 100, false},
		{">", 101, true},
		{"<", 100, false},
		{"<", 99, true},
		{"==", 100, true},
		{"==", 101, false},
	} {
		if got := (SizeMatcher{Op: tt.op, Size: 100}).Match(tt.size); got != tt.want {
			t.Errorf("%s 100 with %d = %v", tt.op, tt.size, got)
		}
	}
}

func TestParseSelectorAcceptsYAMLAndJSON(t *testing.T) {
	for _, doc := range []string{`{ "size": ">= 100GB", "type": "nvme" }`, "size: '>= 100GB'\ntype: nvme\n"} {
		sel, err := ParseSelector([]byte(doc))
		if err != nil {
			t.Fatalf("ParseSelector(%q) error: %v", doc, err)
		}
		if sel.Size == nil || sel.Size.Op != ">=" || sel.Size.Size != 100_000_000_000 || sel.Type != "nvme" {
			t.Fatalf("ParseSelector(%q) = %+v", doc, sel)
		}
	}
}

func TestParseSelectorRejectsNameUnknownFieldsAndBadType(t *testing.T) {
	for _, doc := range []string{`{"name": "sda"}`, `{"colour": "red"}`, `{"type": "floppy"}`, `{"size": "big"}`} {
		if _, err := ParseSelector([]byte(doc)); err == nil {
			t.Errorf("ParseSelector(%q) accepted", doc)
		}
	}
	if _, err := ParseSelector([]byte(`{"name": "sda"}`)); err == nil || !strings.Contains(err.Error(), "selector on name is not supported") {
		t.Errorf("name rejection message: %v", err)
	}
}

func TestParseSelectorEmptyMatchesEverythingEligible(t *testing.T) {
	for _, doc := range []string{"", "{}", "  \n"} {
		sel, err := ParseSelector([]byte(doc))
		if err != nil {
			t.Fatalf("ParseSelector(%q) error: %v", doc, err)
		}
		if !sel.Match(nvme) || !sel.Match(sda) || sel.Match(sr0) || sel.Match(loop) {
			t.Fatalf("empty selector should apply only the implicit filters")
		}
	}
}

func TestSelectorMatch(t *testing.T) {
	tests := []struct {
		doc  string
		want []string
	}{
		{`{"size": ">= 100GB"}`, []string{"mmcblk0", "nvme0n1", "sda"}},
		{`{"size": ">= 100GB", "type": "nvme"}`, []string{"nvme0n1"}},
		{`{"type": "hdd"}`, []string{"sda"}},
		{`{"type": "ssd"}`, []string{"mmcblk0", "nvme0n1", "sdb"}},
		{`{"type": "sd"}`, []string{"mmcblk0"}},
		{`{"model": "Samsung*"}`, []string{"nvme0n1"}},
		{`{"model": "WDC*", "size": "> 3TB"}`, []string{"sda"}},
		{`{"serial": "WD-1"}`, []string{"sda"}},
		{`{"wwid": "eui.*"}`, []string{"nvme0n1"}},
		{`{"busPath": "/pci0000:00/*"}`, []string{"nvme0n1"}},
		{`{"size": "< 1GB"}`, nil},
	}
	for _, tt := range tests {
		sel, err := ParseSelector([]byte(tt.doc))
		if err != nil {
			t.Fatalf("ParseSelector(%q) error: %v", tt.doc, err)
		}
		var got []string
		for _, d := range all {
			if sel.Match(d) {
				got = append(got, d.Name)
			}
		}
		sortStrings(got)
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%s matched %v, want %v", tt.doc, got, tt.want)
		}
	}
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func TestSelectPicksFirstByNameAndReportsCandidates(t *testing.T) {
	chosen, candidates, err := Select(all, DefaultSelector())
	if err != nil {
		t.Fatalf("Select() error: %v", err)
	}
	if chosen.Name != "mmcblk0" {
		t.Fatalf("chosen = %s, want mmcblk0 (first by name)", chosen.Name)
	}
	if len(candidates) != 3 || candidates[0].Name != "mmcblk0" || candidates[1].Name != "nvme0n1" || candidates[2].Name != "sda" {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestSelectFailsWithoutMatch(t *testing.T) {
	sel, _ := ParseSelector([]byte(`{"model": "nothing*"}`))
	if _, _, err := Select(all, sel); err == nil {
		t.Fatal("expected an error when nothing matches")
	}
}

func TestDefaultSelectorDocument(t *testing.T) {
	if DefaultSelectorDocument != `{ "size": ">= 100GB" }` {
		t.Fatalf("DefaultSelectorDocument = %q", DefaultSelectorDocument)
	}
	if DefaultSelector().String() != DefaultSelectorDocument {
		t.Fatalf("String() = %q", DefaultSelector().String())
	}
}

func TestSelectPathFollowsSymlinksAndChecksEligibility(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nvme0n1")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "by-id-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	list := []Disk{{Name: "nvme0n1", DevPath: target, Transport: "nvme"}, {Name: "sr0", DevPath: filepath.Join(dir, "sr0"), Transport: "sata", CDROM: true}}

	d, err := SelectPath(list, link)
	if err != nil || d.Name != "nvme0n1" {
		t.Fatalf("SelectPath(link) = %+v, %v", d, err)
	}
	if _, err := SelectPath(list, filepath.Join(dir, "sr0")); err == nil {
		t.Fatal("a CD-ROM must not be selectable by path")
	}
	if _, err := SelectPath(list, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected an error for an unknown path")
	}
}
