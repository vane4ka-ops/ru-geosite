package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	domainTypePlain  = 0
	domainTypeRegex  = 1
	domainTypeDomain = 2
	domainTypeFull   = 3
)

var geoSiteTags = []string{"direct", "proxy"}

type domainEntry struct {
	typ   int
	value string
}

func main() {
	outPath := flag.String("out", "custom.dat", "path of the generated GeoSite file")
	checkPath := flag.String("check", "", "validate an existing GeoSite file instead of building one")
	flag.Parse()
	if *checkPath != "" {
		if flag.NArg() != 0 {
			fatal("-check cannot be combined with positional arguments")
		}
		data, err := os.ReadFile(*checkPath)
		if err != nil {
			fatal("read %s: %v", *checkPath, err)
		}
		if err := validateGeoSiteList(data, geoSiteTags); err != nil {
			fatal("validate %s: %v", *checkPath, err)
		}
		fmt.Printf("valid: %s\n", *checkPath)
		return
	}
	if flag.NArg() != 0 {
		fatal("unexpected positional arguments")
	}
	data, counts, err := buildGeoSiteList("lists", geoSiteTags)
	if err != nil {
		fatal("build: %v", err)
	}
	if err := validateGeoSiteList(data, geoSiteTags); err != nil {
		fatal("internal validation: %v", err)
	}
	dir := filepath.Dir(*outPath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fatal("create output directory: %v", err)
		}
	}
	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		fatal("write %s: %v", *outPath, err)
	}
	for _, tag := range geoSiteTags {
		fmt.Printf("[%s] %d domains\n", tag, counts[tag])
	}
	fmt.Printf("done: %s (%d bytes)\n", *outPath, len(data))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func buildGeoSiteList(listDir string, tags []string) ([]byte, map[string]int, error) {
	if len(tags) == 0 {
		return nil, nil, errors.New("no GeoSite tags configured")
	}
	geoSites := make([][]byte, 0, len(tags))
	counts := make(map[string]int, len(tags))
	for _, tag := range tags {
		entries, err := readDomains(filepath.Join(listDir, tag+".txt"))
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", tag, err)
		}
		if len(entries) == 0 {
			return nil, nil, fmt.Errorf("%s must contain at least one domain", tag)
		}
		entries = normalizeEntries(entries)
		domains := make([][]byte, 0, len(entries))
		for _, entry := range entries {
			domains = append(domains, encodeDomain(entry.typ, entry.value))
		}
		geoSites = append(geoSites, encodeGeoSite(strings.ToUpper(tag), domains))
		counts[tag] = len(entries)
	}
	return encodeGeoSiteList(geoSites), counts, nil
}

func readDomains(path string) ([]domainEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readDomainLines(f)
}

func readDomainLines(r io.Reader) ([]domainEntry, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var entries []domainEntry
	for lineNo := 1; scanner.Scan(); lineNo++ {
		entry, ok, err := parseLine(scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if ok {
			entries = append(entries, entry)
		}
	}
	return entries, scanner.Err()
}

// parseLine accepts domain: and full:. An unprefixed value means domain:.
// regex: is deliberately rejected because it defeats reviewable routing rules.
func parseLine(raw string) (domainEntry, bool, error) {
	line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
	if line == "" {
		return domainEntry{}, false, nil
	}
	typ := domainTypeDomain
	switch {
	case strings.HasPrefix(line, "domain:"):
		line = strings.TrimSpace(strings.TrimPrefix(line, "domain:"))
	case strings.HasPrefix(line, "full:"):
		typ = domainTypeFull
		line = strings.TrimSpace(strings.TrimPrefix(line, "full:"))
	case strings.HasPrefix(line, "regex:"):
		return domainEntry{}, false, errors.New("regex: entries are not permitted")
	}
	if err := validateDomain(line); err != nil {
		return domainEntry{}, false, err
	}
	return domainEntry{typ: typ, value: strings.ToLower(line)}, true, nil
}

func validateDomain(domain string) error {
	if domain == "" {
		return errors.New("empty domain")
	}
	if len(domain) > 253 || strings.ContainsAny(domain, "/\\:@?* \t") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return fmt.Errorf("invalid domain %q", domain)
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain %q must contain a dot", domain)
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid label in %q", domain)
		}
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return fmt.Errorf("invalid character in %q", domain)
			}
		}
	}
	return nil
}

func normalizeEntries(entries []domainEntry) []domainEntry {
	seen := make(map[domainEntry]struct{}, len(entries))
	unique := make([]domainEntry, 0, len(entries))
	for _, entry := range entries {
		if _, ok := seen[entry]; !ok {
			seen[entry] = struct{}{}
			unique = append(unique, entry)
		}
	}
	sort.Slice(unique, func(i, j int) bool {
		if unique[i].typ != unique[j].typ {
			return unique[i].typ < unique[j].typ
		}
		return unique[i].value < unique[j].value
	})
	return unique
}

// The encoder has no runtime dependencies. Field numbers match the v2ray-core
// GeoSiteList / GeoSite / Domain protobuf schema.
func encodeDomain(typ int, value string) []byte {
	var b []byte
	if typ != domainTypePlain {
		b = appendVarintField(b, 1, uint64(typ))
	}
	return appendStringField(b, 2, value)
}

func encodeGeoSite(tag string, domains [][]byte) []byte {
	b := appendStringField(nil, 1, tag)
	for _, domain := range domains {
		b = appendBytesField(b, 2, domain)
	}
	return b
}

func encodeGeoSiteList(geoSites [][]byte) []byte {
	var b []byte
	for _, geoSite := range geoSites {
		b = appendBytesField(b, 1, geoSite)
	}
	return b
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendVarintField(b []byte, fieldNum int, v uint64) []byte {
	b = appendVarint(b, uint64(fieldNum<<3))
	return appendVarint(b, v)
}

func appendBytesField(b []byte, fieldNum int, data []byte) []byte {
	b = appendVarint(b, uint64(fieldNum<<3|2))
	b = appendVarint(b, uint64(len(data)))
	return append(b, data...)
}

func appendStringField(b []byte, fieldNum int, value string) []byte {
	return appendBytesField(b, fieldNum, []byte(value))
}

// validateGeoSiteList decodes the generated wire data in CI and verifies that
// each required category is present and non-empty.
func validateGeoSiteList(data []byte, requiredTags []string) error {
	geoSites, err := decodeGeoSiteList(data)
	if err != nil {
		return err
	}
	expected := make(map[string]struct{}, len(requiredTags))
	for _, tag := range requiredTags {
		expected[strings.ToUpper(tag)] = struct{}{}
	}
	for tag, domains := range geoSites {
		if _, ok := expected[tag]; !ok {
			return fmt.Errorf("unexpected category %q", tag)
		}
		if len(domains) == 0 {
			return fmt.Errorf("category %q is empty", tag)
		}
		delete(expected, tag)
	}
	if len(expected) != 0 {
		missing := make([]string, 0, len(expected))
		for tag := range expected {
			missing = append(missing, tag)
		}
		sort.Strings(missing)
		return fmt.Errorf("missing categories: %s", strings.Join(missing, ", "))
	}
	return nil
}

func decodeGeoSiteList(data []byte) (map[string][]domainEntry, error) {
	result := make(map[string][]domainEntry)
	for len(data) > 0 {
		field, wire, rest, err := takeField(data)
		if err != nil || field != 1 || wire != 2 {
			return nil, errors.New("invalid GeoSiteList field")
		}
		value, remaining, err := takeBytes(rest)
		if err != nil {
			return nil, err
		}
		tag, domains, err := decodeGeoSite(value)
		if err != nil {
			return nil, err
		}
		if _, exists := result[tag]; exists {
			return nil, fmt.Errorf("duplicate category %q", tag)
		}
		result[tag] = domains
		data = remaining
	}
	return result, nil
}

func decodeGeoSite(data []byte) (string, []domainEntry, error) {
	var tag string
	var domains []domainEntry
	for len(data) > 0 {
		field, wire, rest, err := takeField(data)
		if err != nil || wire != 2 {
			return "", nil, errors.New("invalid GeoSite field")
		}
		value, remaining, err := takeBytes(rest)
		if err != nil {
			return "", nil, err
		}
		switch field {
		case 1:
			if tag != "" {
				return "", nil, errors.New("duplicate GeoSite tag")
			}
			tag = string(value)
		case 2:
			domain, err := decodeDomain(value)
			if err != nil {
				return "", nil, err
			}
			domains = append(domains, domain)
		default:
			return "", nil, fmt.Errorf("unknown GeoSite field %d", field)
		}
		data = remaining
	}
	if tag == "" {
		return "", nil, errors.New("GeoSite without a tag")
	}
	return tag, domains, nil
}

func decodeDomain(data []byte) (domainEntry, error) {
	entry := domainEntry{typ: domainTypePlain}
	seenType := false
	seenValue := false
	for len(data) > 0 {
		field, wire, rest, err := takeField(data)
		if err != nil {
			return domainEntry{}, err
		}
		switch {
		case field == 1 && wire == 0:
			if seenType {
				return domainEntry{}, errors.New("duplicate domain type")
			}
			value, remaining, err := takeVarint(rest)
			if err != nil || (value != domainTypeDomain && value != domainTypeFull) {
				return domainEntry{}, errors.New("invalid domain type")
			}
			entry.typ, seenType, data = int(value), true, remaining
		case field == 2 && wire == 2:
			value, remaining, err := takeBytes(rest)
			if err != nil || seenValue {
				return domainEntry{}, errors.New("invalid domain value")
			}
			entry.value, seenValue, data = string(value), true, remaining
		default:
			return domainEntry{}, errors.New("invalid domain field")
		}
	}
	if !seenType {
		return domainEntry{}, errors.New("domain without a type")
	}
	if !seenValue {
		return domainEntry{}, errors.New("domain without a value")
	}
	if err := validateDomain(entry.value); err != nil {
		return domainEntry{}, fmt.Errorf("invalid domain value: %w", err)
	}
	return entry, nil
}

func takeField(data []byte) (int, int, []byte, error) {
	key, rest, err := takeVarint(data)
	if err != nil || key == 0 {
		return 0, 0, nil, errors.New("invalid protobuf field key")
	}
	return int(key >> 3), int(key & 7), rest, nil
}

func takeBytes(data []byte) ([]byte, []byte, error) {
	length, rest, err := takeVarint(data)
	if err != nil || length > uint64(len(rest)) {
		return nil, nil, errors.New("invalid length-delimited field")
	}
	return rest[:length], rest[length:], nil
}

func takeVarint(data []byte) (uint64, []byte, error) {
	var value uint64
	for i := 0; i < 10; i++ {
		if len(data) == 0 {
			return 0, nil, errors.New("truncated varint")
		}
		b := data[0]
		data = data[1:]
		if i == 9 && b > 1 {
			return 0, nil, errors.New("varint overflow")
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return value, data, nil
		}
	}
	return 0, nil, errors.New("varint overflow")
}
