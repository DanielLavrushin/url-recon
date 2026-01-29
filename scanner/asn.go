package scanner

import (
	"bufio"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed asn.json
var asnProvidersJSON []byte

var asnProviders map[string]string

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

func LoadIPRanges() error {
	rangesOnce.Do(func() {
		rangesError = loadIPRangesInternal()
	})
	return rangesError
}

const ip2asnURL = "https://iptoasn.com/data/ip2asn-combined.tsv.gz"

func findDataFile() (string, error) {
	names := []string{"ip2asn-combined.tsv", "ip2asn-combined.tsv.gz"}

	paths := []string{"."}

	var reconDir string
	if home, err := os.UserHomeDir(); err == nil {
		reconDir = filepath.Join(home, ".recon")
		paths = append(paths, home, reconDir)
	}

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

	if reconDir == "" {
		return "", errors.New("IP-to-ASN database not found and cannot determine home directory")
	}

	dest := filepath.Join(reconDir, "ip2asn-combined.tsv.gz")
	if err := downloadFile(ip2asnURL, dest); err != nil {
		return "", fmt.Errorf("IP-to-ASN database not found and download failed: %w", err)
	}
	return dest, nil
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

	sort.Slice(ipRanges, func(i, j int) bool {
		return ipRanges[i].StartIP < ipRanges[j].StartIP
	})

	return nil
}

func detectCDNByIP(ipStr string) string {
	if err := LoadIPRanges(); err != nil {
		return ""
	}

	ip, err := ipToUint32(ipStr)
	if err != nil {
		return ""
	}

	idx := sort.Search(len(ipRanges), func(i int) bool {
		return ipRanges[i].StartIP > ip
	})

	if idx > 0 {
		r := ipRanges[idx-1]
		if ip >= r.StartIP && ip <= r.EndIP {
			return r.Provider
		}
	}

	return ""
}

func detectCDNByASN(ips []string) string {
	for _, ip := range ips {

		if strings.Contains(ip, ":") {
			continue
		}
		if provider := detectCDNByIP(ip); provider != "" {
			return provider
		}
	}
	return ""
}

func GetLoadedRangeCount() int {
	if err := LoadIPRanges(); err != nil {
		return 0
	}
	return len(ipRanges)
}

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

func ReloadIPRanges() error {
	rangesOnce = sync.Once{}
	ipRanges = nil
	return LoadIPRanges()
}

func GetASNProviders() map[string]string {
	result := make(map[string]string, len(asnProviders))
	for k, v := range asnProviders {
		result[k] = v
	}
	return result
}
