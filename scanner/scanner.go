package scanner

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// ProgressFunc is called with progress updates during scanning
type ProgressFunc func(stage string, current, total int)

func Scan(ctx context.Context, targetURL string, onProgress ProgressFunc) (*Result, error) {
	if onProgress == nil {
		onProgress = func(string, int, int) {
			// no-op
		}
	}

	// Validate URL and check for SSRF before proceeding
	validatedURL, err := ValidateURL(targetURL)
	if err != nil {
		return nil, fmt.Errorf("URL validation failed: %w", err)
	}
	targetURL = validatedURL

	parsed, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}
	targetDomain := parsed.Hostname()

	onProgress("Starting browser", 0, 0)

	// Build chromedp options
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
	)

	// Only add --no-sandbox if explicitly requested via environment variable
	// WARNING: This should only be used in containerized environments where
	// the sandbox cannot run (e.g., Docker without --cap-add=SYS_ADMIN)
	if os.Getenv("CHROMEDP_NO_SANDBOX") == "true" {
		opts = append(opts, chromedp.Flag("no-sandbox", true))
	}

	// Create chromedp context
	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	chromedpCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Set timeout
	chromedpCtx, cancel = context.WithTimeout(chromedpCtx, 30*time.Second)
	defer cancel()

	onProgress("Loading page", 0, 0)

	var html string
	err = chromedp.Run(chromedpCtx,
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second), // Allow JS to execute
		chromedp.OuterHTML("html", &html),
	)
	if err != nil {
		return nil, err
	}

	onProgress("Extracting domains", 0, 0)
	domains := extractDomains(html, targetDomain)

	onProgress("Resolving DNS", 0, len(domains))
	// Resolve IPs and detect CDNs in parallel
	resolveDomainInfo(domains, onProgress)

	onProgress("Complete", len(domains), len(domains))

	return &Result{
		TargetURL:    targetURL,
		TargetDomain: targetDomain,
		Domains:      domains,
		Stats:        calculateStats(domains),
		ScannedAt:    time.Now(),
	}, nil
}

func resolveDomainInfo(domains []DomainInfo, onProgress ProgressFunc) {
	var wg sync.WaitGroup
	var resolved int
	var mu sync.Mutex
	sem := make(chan struct{}, 20) // Limit concurrent lookups
	total := len(domains)

	for i := range domains {
		wg.Add(1)
		go func(d *DomainInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Resolve IPs
			ips, err := net.LookupHost(d.Domain)
			if err == nil {
				d.IPs = ips
			}

			// Detect CDN via IP-to-ASN lookup
			if len(d.IPs) > 0 {
				d.CDN = detectCDNByASN(d.IPs)
			}

			mu.Lock()
			resolved++
			onProgress("Resolving DNS", resolved, total)
			mu.Unlock()
		}(&domains[i])
	}
	wg.Wait()
}

func extractDomains(html string, targetDomain string) []DomainInfo {
	domainMap := make(map[string]map[string]bool) // domain -> set of sources

	// Patterns to extract URLs from various attributes
	patterns := []struct {
		regex  *regexp.Regexp
		source string
	}{
		{regexp.MustCompile(`href=["']([^"']+)["']`), "href"},
		{regexp.MustCompile(`src=["']([^"']+)["']`), "src"},
		{regexp.MustCompile(`srcset=["']([^"']+)["']`), "srcset"},
		{regexp.MustCompile(`url\(["']?([^"')]+)["']?\)`), "css-url"},
		{regexp.MustCompile(`action=["']([^"']+)["']`), "form-action"},
		{regexp.MustCompile(`data-[a-z-]+=["'](https?://[^"']+)["']`), "data-attr"},
		{regexp.MustCompile(`content=["'](https?://[^"']+)["']`), "meta"},
		{regexp.MustCompile(`https?://[a-zA-Z0-9][-a-zA-Z0-9]*(\.[a-zA-Z0-9][-a-zA-Z0-9]*)+[^\s"'<>]*`), "inline"},
	}

	for _, p := range patterns {
		matches := p.regex.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			urlStr := match[1]

			// Handle srcset (comma-separated URLs with sizes)
			if p.source == "srcset" {
				parts := strings.Split(urlStr, ",")
				for _, part := range parts {
					part = strings.TrimSpace(part)
					fields := strings.Fields(part)
					if len(fields) > 0 {
						addDomain(domainMap, fields[0], p.source)
					}
				}
				continue
			}

			addDomain(domainMap, urlStr, p.source)
		}
	}

	// Convert map to slice
	var result []DomainInfo
	for domain, sources := range domainMap {
		sourceList := make([]string, 0, len(sources))
		for s := range sources {
			sourceList = append(sourceList, s)
		}
		sort.Strings(sourceList)

		isExternal := !strings.HasSuffix(domain, targetDomain) &&
			!strings.HasSuffix(targetDomain, domain) &&
			domain != targetDomain

		result = append(result, DomainInfo{
			Domain:   domain,
			Count:    len(sources),
			Sources:  sourceList,
			External: isExternal,
		})
	}

	// Sort by count descending, then by domain name
	sort.Slice(result, func(i, j int) bool {
		if result[i].External != result[j].External {
			return result[i].External // External first
		}
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Domain < result[j].Domain
	})

	return result
}

func addDomain(domainMap map[string]map[string]bool, urlStr string, source string) {
	// Try to parse as URL
	parsed, err := url.Parse(strings.TrimSpace(urlStr))
	if err != nil {
		return
	}

	host := parsed.Hostname()
	if host == "" {
		return
	}

	// Skip IP addresses and localhost
	if isIP(host) || host == "localhost" {
		return
	}

	// Skip common data URIs and invalid domains
	if !strings.Contains(host, ".") {
		return
	}

	if domainMap[host] == nil {
		domainMap[host] = make(map[string]bool)
	}
	domainMap[host][source] = true
}

func isIP(host string) bool {
	return net.ParseIP(host) != nil
}

func calculateStats(domains []DomainInfo) Stats {
	stats := Stats{TotalDomains: len(domains)}
	for _, d := range domains {
		if d.External {
			stats.ExternalDomains++
		} else {
			stats.InternalDomains++
		}
	}
	return stats
}
