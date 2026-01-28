package scanner

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var (
	ErrPrivateIP       = errors.New("target resolves to a private or reserved IP address")
	ErrInvalidScheme   = errors.New("only http and https schemes are allowed")
	ErrInvalidURL      = errors.New("invalid URL")
	ErrEmptyHost       = errors.New("URL must have a valid hostname")
	ErrBlockedHost     = errors.New("this host is not allowed")
	ErrURLTooLong      = errors.New("URL exceeds maximum length")
)

const (
	MaxURLLength = 2048
)

// blockedHosts contains hostnames that should never be scanned
var blockedHosts = map[string]bool{
	"localhost":              true,
	"metadata.google.internal": true,
}

// ValidateURL performs comprehensive URL validation including SSRF protection.
// It validates the scheme, resolves the hostname, and checks that the target
// IP is not private/reserved.
func ValidateURL(targetURL string) (string, error) {
	// Check URL length
	if len(targetURL) > MaxURLLength {
		return "", ErrURLTooLong
	}

	// Normalize and parse URL
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return "", ErrInvalidURL
	}

	// Add scheme if missing
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "https://" + targetURL
	}

	parsed, err := url.Parse(targetURL)
	if err != nil {
		return "", ErrInvalidURL
	}

	// Validate scheme - only allow http/https
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", ErrInvalidScheme
	}

	// Validate hostname exists
	host := parsed.Hostname()
	if host == "" {
		return "", ErrEmptyHost
	}

	// Check against blocked hosts
	hostLower := strings.ToLower(host)
	if blockedHosts[hostLower] {
		return "", ErrBlockedHost
	}

	// Check if host is an IP address
	if ip := net.ParseIP(host); ip != nil {
		if err := validateIP(ip); err != nil {
			return "", err
		}
		return targetURL, nil
	}

	// Resolve hostname and validate all IPs
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("failed to resolve hostname: %w", err)
	}

	if len(ips) == 0 {
		return "", fmt.Errorf("hostname resolved to no addresses")
	}

	// Check ALL resolved IPs - attacker could have multiple A records
	for _, ip := range ips {
		if err := validateIP(ip); err != nil {
			return "", err
		}
	}

	return targetURL, nil
}

// validateIP checks if an IP address is safe to connect to.
// Returns an error if the IP is private, loopback, or otherwise reserved.
func validateIP(ip net.IP) error {
	// Check for loopback (127.0.0.0/8, ::1)
	if ip.IsLoopback() {
		return ErrPrivateIP
	}

	// Check for private networks (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7)
	if ip.IsPrivate() {
		return ErrPrivateIP
	}

	// Check for link-local addresses (169.254.0.0/16, fe80::/10)
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return ErrPrivateIP
	}

	// Check for unspecified address (0.0.0.0, ::)
	if ip.IsUnspecified() {
		return ErrPrivateIP
	}

	// Check for multicast
	if ip.IsMulticast() {
		return ErrPrivateIP
	}

	// Check for AWS/cloud metadata service IPs
	// 169.254.169.254 is used by AWS, GCP, Azure for instance metadata
	metadataIPs := []string{
		"169.254.169.254",
		"fd00:ec2::254",  // AWS IPv6 metadata
	}
	for _, metaIP := range metadataIPs {
		if ip.Equal(net.ParseIP(metaIP)) {
			return ErrPrivateIP
		}
	}

	// Additional check for IPv4-mapped IPv6 addresses
	// These could be used to bypass IPv4 checks
	if ip4 := ip.To4(); ip4 != nil {
		// Re-validate the IPv4 portion
		if ip4.IsLoopback() || ip4.IsPrivate() || ip4.IsLinkLocalUnicast() {
			return ErrPrivateIP
		}
	}

	return nil
}

// IsPrivateIP is exported for use in other packages
func IsPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return validateIP(ip) != nil
}
