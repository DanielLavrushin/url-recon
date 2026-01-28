package scanner

import (
	"bufio"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed asn.json
var asnProvidersJSON []byte

// asnProviders maps ASN numbers to CDN/provider names
var asnProviders map[string]string

// ipRange represents a range of IPs belonging to a CDN
type ipRange struct {
	StartIP  uint32
	EndIP    uint32
	Provider string
}

var (
	ipRanges    []ipRange
	rangesOnce  sync.Once
	rangesError error
)

func init() {
	if err := json.Unmarshal(asnProvidersJSON, &asnProviders); err != nil {
		panic("failed to load ASN providers: " + err.Error())
	}
}

// ipToUint32 converts an IPv4 address to uint32 for fast comparison
func ipToUint32(ipStr string) (uint32, error) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return 0, fmt.Errorf("invalid IP: %s", ipStr)
	}
	ip = ip.To4()
	if ip == nil {
		return 0, fmt.Errorf("not IPv4: %s", ipStr)
	}
	return binary.BigEndian.Uint32(ip), nil
}

// LoadIPRanges loads and parses the ip2asn TSV file, filtering only CDN ASNs
// Looks for ip2asn-combined.tsv or ip2asn-combined.tsv.gz in:
// 1. Current directory
// 2. Scanner data directory
// 3. Home directory
func LoadIPRanges() error {
	rangesOnce.Do(func() {
		rangesError = loadIPRangesInternal()
	})
	return rangesError
}

func findDataFile() (string, error) {
	names := []string{"ip2asn-combined.tsv", "ip2asn-combined.tsv.gz"}

	// Check paths in order
	paths := []string{"."}

	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, home, filepath.Join(home, ".recon"))
	}

	// Also check next to executable
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Dir(exe))
	}

	for _, dir := range paths {
		for _, name := range names {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}

	return "", errors.New("IP-to-ASN database not found")
}

func loadIPRangesInternal() error {
	path, err := findDataFile()
	if err != nil {
		return err
	}

	file, err := os.Open(path)
	if err != nil {
		return errors.New("failed to open IP-to-ASN database")
	}
	defer file.Close()

	var scanner *bufio.Scanner

	// Handle gzip if needed
	if strings.HasSuffix(path, ".gz") {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		scanner = bufio.NewScanner(gzReader)
	} else {
		scanner = bufio.NewScanner(file)
	}

	// Pre-allocate slice (estimate ~1000 CDN ranges)
	ipRanges = make([]ipRange, 0, 1000)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}

		asn := fields[2]
		provider, isCDN := asnProviders[asn]
		if !isCDN {
			continue
		}

		startIP, err := ipToUint32(fields[0])
		if err != nil {
			continue
		}
		endIP, err := ipToUint32(fields[1])
		if err != nil {
			continue
		}

		ipRanges = append(ipRanges, ipRange{
			StartIP:  startIP,
			EndIP:    endIP,
			Provider: provider,
		})
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}

	// Sort by StartIP for binary search
	sort.Slice(ipRanges, func(i, j int) bool {
		return ipRanges[i].StartIP < ipRanges[j].StartIP
	})

	return nil
}

// detectCDNByIP checks if an IP belongs to a known CDN provider
func detectCDNByIP(ipStr string) string {
	if err := LoadIPRanges(); err != nil {
		return ""
	}

	ip, err := ipToUint32(ipStr)
	if err != nil {
		return ""
	}

	// Binary search to find potential range
	idx := sort.Search(len(ipRanges), func(i int) bool {
		return ipRanges[i].StartIP > ip
	})

	// Check the range before (if exists) - it might contain our IP
	if idx > 0 {
		r := ipRanges[idx-1]
		if ip >= r.StartIP && ip <= r.EndIP {
			return r.Provider
		}
	}

	return ""
}

// detectCDNByASN checks IPs against known CDN IP ranges
func detectCDNByASN(ips []string) string {
	for _, ip := range ips {
		// Skip IPv6
		if strings.Contains(ip, ":") {
			continue
		}
		if provider := detectCDNByIP(ip); provider != "" {
			return provider
		}
	}
	return ""
}

// GetLoadedRangeCount returns the number of IP ranges loaded (for debugging)
func GetLoadedRangeCount() int {
	if err := LoadIPRanges(); err != nil {
		return 0
	}
	return len(ipRanges)
}

// GetASNForIP returns the provider name for an IP (exported for testing)
func GetASNForIP(ip string) (string, error) {
	if err := LoadIPRanges(); err != nil {
		return "", err
	}
	provider := detectCDNByIP(ip)
	if provider == "" {
		return "", fmt.Errorf("no CDN provider found for IP %s", ip)
	}
	return provider, nil
}

// ReloadIPRanges forces a reload of the IP ranges (useful after updating the TSV file)
func ReloadIPRanges() error {
	rangesOnce = sync.Once{}
	ipRanges = nil
	return LoadIPRanges()
}

// GetASNProviders returns the ASN to provider mapping (for debugging/info)
func GetASNProviders() map[string]string {
	result := make(map[string]string, len(asnProviders))
	for k, v := range asnProviders {
		result[k] = v
	}
	return result
}
