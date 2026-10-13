package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadDomainLinesNormalizesAndSorts(t *testing.T) {
	entries, err := readDomainLines(strings.NewReader("# comment\nExample.COM # inline\nfull:api.example.com\ndomain:example.com\nexample.com\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := normalizeEntries(entries)
	want := []domainEntry{{typ: domainTypeDomain, value: "example.com"}, {typ: domainTypeFull, value: "api.example.com"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %#v, want %#v", got, want)
	}
}

func TestReadDomainLinesRejectsUnsafeSyntax(t *testing.T) {
	for _, input := range []string{"regex:^.*\\.example\\.com$\n", "https://example.com\n", "example..com\n", "localhost\n"} {
		if _, err := readDomainLines(strings.NewReader(input)); err == nil {
			t.Errorf("%q unexpectedly accepted", strings.TrimSpace(input))
		}
	}
}

func TestBuildIsDeterministicAndDecodable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "direct.txt"), []byte("z.example\na.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proxy.txt"), []byte("full:api.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, counts, err := buildGeoSiteList(dir, geoSiteTags)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := buildGeoSiteList(dir, geoSiteTags)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("same input produced different binaries")
	}
	if counts["direct"] != 2 || counts["proxy"] != 1 {
		t.Fatalf("unexpected counts: %#v", counts)
	}
	if err := validateGeoSiteList(first, geoSiteTags); err != nil {
		t.Fatalf("generated file is invalid: %v", err)
	}
}

func TestValidateGeoSiteListRejectsCorruption(t *testing.T) {
	if err := validateGeoSiteList([]byte{0x0a, 0x05, 0x01}, geoSiteTags); err == nil {
		t.Fatal("corrupt data unexpectedly accepted")
	}
}

func TestDecodeDomainAcceptsSupportedTypes(t *testing.T) {
	// Literal protobuf bytes keep these expectations independent of our encoder.
	for _, typ := range []byte{2, 3} {
		data := append([]byte{0x08, typ, 0x12, 0x06}, []byte("vk.com")...)
		got, err := decodeDomain(data)
		if err != nil {
			t.Fatalf("type %d: %v", typ, err)
		}
		want := domainEntry{typ: int(typ), value: "vk.com"}
		if got != want {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
	// Protobuf permits value before type; validation must not depend on order.
	data := append([]byte{0x12, 0x06}, []byte("vk.com")...)
	data = append(data, 0x08, 0x02)
	if _, err := decodeDomain(data); err != nil {
		t.Fatalf("value before type: %v", err)
	}
}

func TestValidateGeoSiteListRejectsInvalidDomains(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"missing type", append([]byte{0x12, 0x06}, []byte("vk.com")...)},
		{"explicit plain", append([]byte{0x08, 0x00, 0x12, 0x06}, []byte("vk.com")...)},
		{"regex", append([]byte{0x08, 0x01, 0x12, 0x06}, []byte("vk.com")...)},
		{"unknown type", append([]byte{0x08, 0x04, 0x12, 0x06}, []byte("vk.com")...)},
		{"duplicate type", append([]byte{0x08, 0x02, 0x08, 0x03, 0x12, 0x06}, []byte("vk.com")...)},
		{"missing value", []byte{0x08, 0x02}},
		{"empty value", []byte{0x08, 0x02, 0x12, 0x00}},
		{"invalid domain", append([]byte{0x08, 0x02, 0x12, 0x07}, []byte("vk..com")...)},
		{"invalid UTF-8", []byte{0x08, 0x02, 0x12, 0x03, 0xff, '.', 'a'}},
		{"duplicate value", append(append([]byte{0x08, 0x02, 0x12, 0x06}, []byte("vk.com")...), 0x12, 0x00)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Exercise the same validation path as the -check CLI command, with
			// both required categories present to isolate the malformed entry.
			data := encodeGeoSiteList([][]byte{
				encodeGeoSite("DIRECT", [][]byte{tt.data}),
				encodeGeoSite("PROXY", [][]byte{encodeDomain(domainTypeFull, "proxy.example")}),
			})
			if err := validateGeoSiteList(data, geoSiteTags); err == nil {
				t.Fatal("invalid domain unexpectedly accepted")
			}
		})
	}
}
