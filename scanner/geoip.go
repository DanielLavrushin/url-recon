package scanner

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/DanielLavrushin/url-recon/geodat"
)

// geoIPMap maps each CIDR prefix to its category name.
// Using netip.Prefix as key allows O(1) lookups per prefix length,
// so total lookup cost is O(32) for IPv4 or O(128) for IPv6.
var (
	geoIPMap  map[netip.Prefix]string
	geoIPOnce sync.Once
	geoIPErr  error
	geoIPPath string
)

const geoIPDownloadURL = "https://github.com/DanielLavrushin/b4geoip/releases/latest/download/geoip.dat"

// LoadGeoIP loads the geoip.dat file and builds the IP-to-provider lookup map.
// Only non-country categories (len > 2) are loaded as provider/service categories.
func LoadGeoIP(path string) error {
	geoIPPath = path
	geoIPOnce.Do(func() {
		geoIPErr = loadGeoIPInternal()
	})
	return geoIPErr
}

func findGeoIPFile() (string, error) {
	if geoIPPath != "" {
		if _, err := os.Stat(geoIPPath); err == nil {
			return geoIPPath, nil
		}
	}

	name := "geoip.dat"

	var exeDir string
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}

	searchPaths := []string{"."}
	if exeDir != "" {
		searchPaths = append(searchPaths, exeDir)
	}

	for _, dir := range searchPaths {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	destDir := "."
	if exeDir != "" {
		destDir = exeDir
	}
	dest := filepath.Join(destDir, name)
	log.Printf("GeoIP: downloading geoip.dat to %s ...", dest)
	if err := downloadFile(geoIPDownloadURL, dest); err != nil {
		return "", fmt.Errorf("geoip.dat not found and download failed: %w", err)
	}
	log.Printf("GeoIP: download complete")
	return dest, nil
}

func loadGeoIPInternal() error {
	path, err := findGeoIPFile()
	if err != nil {
		return err
	}

	// Single pass: load only service categories (not 2-letter country codes)
	prefixMap, err := geodat.LoadServiceIPPrefixes(path, func(category string) bool {
		return len(category) > 2
	})
	if err != nil {
		return fmt.Errorf("failed to load geoip data: %w", err)
	}
	if len(prefixMap) == 0 {
		return fmt.Errorf("no service categories found in geoip.dat")
	}

	categories := make([]string, 0, len(prefixMap))
	var total int
	for cat, prefixes := range prefixMap {
		categories = append(categories, cat)
		total += len(prefixes)
	}

	// Build prefix→category map for O(1) lookups per prefix length
	geoIPMap = make(map[netip.Prefix]string, total)
	for category, prefixes := range prefixMap {
		for _, prefix := range prefixes {
			geoIPMap[prefix] = category
		}
	}

	log.Printf("GeoIP: loaded %d prefixes across %d categories: %s",
		len(geoIPMap), len(prefixMap), strings.Join(categories, ", "))
	return nil
}

// detectProviderByGeoIP checks the given IPs against the geoip.dat service categories.
// Uses map lookups across prefix lengths: O(32) for IPv4, O(128) for IPv6.
func detectProviderByGeoIP(ips []string) string {
	for _, ipStr := range ips {
		addr, err := netip.ParseAddr(ipStr)
		if err != nil {
			continue
		}

		maxBits := 32
		if addr.Is6() {
			maxBits = 128
		}

		for bits := maxBits; bits >= 1; bits-- {
			prefix, err := addr.Prefix(bits)
			if err != nil {
				continue
			}
			if cat, ok := geoIPMap[prefix]; ok {
				return cat
			}
		}
	}
	return ""
}

func downloadFile(url, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(dest)
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}
