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

var (
	geoIPMap  map[netip.Prefix]string
	geoIPOnce sync.Once
	geoIPErr  error
	geoIPPath string
)

const geoIPDownloadURL = "https://github.com/DanielLavrushin/b4geoip/releases/latest/download/geoip.dat"

func LoadGeoIP(path string) error {
	geoIPPath = path
	geoIPOnce.Do(func() {
		geoIPErr = loadGeoIPInternal()
	})
	return geoIPErr
}

func findGeoIPFile() (string, error) {
	name := "geoip.dat"

	var exeDir string
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}

	dest := geoIPPath
	if dest == "" {
		destDir := "."
		if exeDir != "" {
			destDir = exeDir
		}
		dest = filepath.Join(destDir, name)
	}

	log.Printf("GeoIP: downloading geoip.dat to %s ...", dest)
	if err := downloadFile(geoIPDownloadURL, dest); err != nil {
		if _, statErr := os.Stat(dest); statErr == nil {
			log.Printf("GeoIP: download failed (%v), using existing file", err)
			return dest, nil
		}
		searchPaths := []string{"."}
		if exeDir != "" {
			searchPaths = append(searchPaths, exeDir)
		}
		for _, dir := range searchPaths {
			p := filepath.Join(dir, name)
			if _, statErr := os.Stat(p); statErr == nil {
				log.Printf("GeoIP: download failed (%v), using existing file at %s", err, p)
				return p, nil
			}
		}
		return "", fmt.Errorf("geoip.dat download failed and no existing file found: %w", err)
	}
	log.Printf("GeoIP: download complete")
	return dest, nil
}

func loadGeoIPInternal() error {
	path, err := findGeoIPFile()
	if err != nil {
		return err
	}

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
