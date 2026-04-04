package scanner

import (
	"errors"
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

type geoIPEntry struct {
	Prefix   netip.Prefix
	Category string
}

var (
	geoIPEntries []geoIPEntry
	geoIPOnce    sync.Once
	geoIPError   error
	geoIPPath    string
)

const geoIPDownloadURL = "https://github.com/DanielLavrushin/b4geoip/releases/latest/download/geoip.dat"

// LoadGeoIP loads the geoip.dat file and builds the IP-to-provider lookup table.
// Only non-country categories (len > 2) are loaded as provider/service categories.
func LoadGeoIP(path string) error {
	geoIPPath = path
	geoIPOnce.Do(func() {
		geoIPError = loadGeoIPInternal()
	})
	return geoIPError
}

func findGeoIPFile() (string, error) {
	if geoIPPath != "" {
		if _, err := os.Stat(geoIPPath); err == nil {
			return geoIPPath, nil
		}
	}

	name := "geoip.dat"

	// Prefer the directory next to the binary
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

	// Download next to the binary, fall back to current directory
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

	// List all categories to find service/provider ones (not 2-letter country codes)
	allCategories, err := geodat.ListGeoIPCategories(path)
	if err != nil {
		return fmt.Errorf("failed to list geoip categories: %w", err)
	}

	var serviceCategories []string
	for _, cat := range allCategories {
		if len(cat) > 2 {
			serviceCategories = append(serviceCategories, cat)
		}
	}

	if len(serviceCategories) == 0 {
		return errors.New("no service categories found in geoip.dat")
	}

	log.Printf("GeoIP: loading %d service categories: %s", len(serviceCategories), strings.Join(serviceCategories, ", "))

	prefixMap, err := geodat.LoadIPPrefixes(path, serviceCategories)
	if err != nil {
		return fmt.Errorf("failed to load geoip prefixes: %w", err)
	}

	// Build flat lookup table
	var total int
	for _, prefixes := range prefixMap {
		total += len(prefixes)
	}
	geoIPEntries = make([]geoIPEntry, 0, total)
	for category, prefixes := range prefixMap {
		for _, prefix := range prefixes {
			geoIPEntries = append(geoIPEntries, geoIPEntry{
				Prefix:   prefix,
				Category: category,
			})
		}
	}

	log.Printf("GeoIP: loaded %d IP prefixes across %d categories", len(geoIPEntries), len(prefixMap))
	return nil
}

// detectProviderByGeoIP checks the given IPs against the geoip.dat service categories.
// Returns the category name of the first match, or empty string if no match.
func detectProviderByGeoIP(ips []string) string {
	for _, ipStr := range ips {
		addr, err := netip.ParseAddr(ipStr)
		if err != nil {
			continue
		}
		for i := range geoIPEntries {
			if geoIPEntries[i].Prefix.Contains(addr) {
				return geoIPEntries[i].Category
			}
		}
	}
	return ""
}

// GetGeoIPCategoryCount returns the number of loaded geoip entries.
func GetGeoIPCategoryCount() int {
	return len(geoIPEntries)
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
