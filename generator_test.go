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
