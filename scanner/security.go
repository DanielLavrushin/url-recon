package scanner

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var (
	ErrPrivateIP     = errors.New("target resolves to a private or reserved IP address")
	ErrInvalidScheme = errors.New("only http and https schemes are allowed")
	ErrInvalidURL    = errors.New("invalid URL")
	ErrEmptyHost     = errors.New("URL must have a valid hostname")
	ErrBlockedHost   = errors.New("this host is not allowed")
	ErrURLTooLong    = errors.New("URL exceeds maximum length")
)

const (
	MaxURLLength = 2048
)

var blockedHosts = map[string]bool{
	"localhost":                true,
	"metadata.google.internal": true,
}

func ValidateURL(targetURL string) (string, error) {
	if len(targetURL) > MaxURLLength {
		return "", ErrURLTooLong
	}

	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return "", ErrInvalidURL
	}

	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "https://" + targetURL
	}

	parsed, err := url.Parse(targetURL)
	if err != nil {
		return "", ErrInvalidURL
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", ErrInvalidScheme
	}

	host := parsed.Hostname()
	if host == "" {
		return "", ErrEmptyHost
	}

	hostLower := strings.ToLower(host)
	if blockedHosts[hostLower] {
		return "", ErrBlockedHost
	}

	if ip := net.ParseIP(host); ip != nil {
		if err := validateIP(ip); err != nil {
			return "", err
		}
		return targetURL, nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("failed to resolve hostname: %w", err)
	}

	if len(ips) == 0 {
		return "", fmt.Errorf("hostname resolved to no addresses")
	}

	for _, ip := range ips {
		if err := validateIP(ip); err != nil {
			return "", err
		}
	}

	return targetURL, nil
}

func validateIP(ip net.IP) error {
	if ip.IsLoopback() {
		return ErrPrivateIP
	}

	if ip.IsPrivate() {
		return ErrPrivateIP
	}

	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return ErrPrivateIP
	}

	if ip.IsUnspecified() {
		return ErrPrivateIP
	}

	if ip.IsMulticast() {
		return ErrPrivateIP
	}

	metadataIPs := []string{
		"169.254.169.254",
		"fd00:ec2::254",
	}
	for _, metaIP := range metadataIPs {
		if ip.Equal(net.ParseIP(metaIP)) {
			return ErrPrivateIP
		}
	}

	if ip4 := ip.To4(); ip4 != nil {
		if ip4.IsLoopback() || ip4.IsPrivate() || ip4.IsLinkLocalUnicast() {
			return ErrPrivateIP
		}
	}

	return nil
}

func IsPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return validateIP(ip) != nil
}
