package scanner

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type ProgressFunc func(stage string, current, total int)

func Scan(ctx context.Context, targetURL string, onProgress ProgressFunc) (*Result, error) {
	if onProgress == nil {
		onProgress = func(string, int, int) {
			// No-op
		}
	}

	validatedURL, err := ValidateURL(targetURL)
	if err != nil {
		return nil, fmt.Errorf("URL validation failed: %w", err)
	}
	targetURL = validatedURL

	// Check cache before launching a browser
	if scanCache != nil {
		if cached, ok := scanCache.Get(targetURL); ok {
			log.Printf("Cache hit for %s", targetURL)
			onProgress("Complete", len(cached.Domains), len(cached.Domains))
			return cached, nil
		}
	}

	parsed, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}
	targetDomain := parsed.Hostname()

	onProgress("Starting browser", 0, 0)

	pool := GetPool()
	browser, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire browser: %w", err)
	}
	defer pool.Release(browser)

	tabCtx, tabCancel := browser.NewTab(ctx)
	defer tabCancel()

	tabCtx, cancel := context.WithTimeout(tabCtx, 15*time.Second)
	defer cancel()

	onProgress("Loading page", 0, 0)

	networkDomains := make(map[string]map[string]bool)
	var netMu sync.Mutex
	chromedp.ListenTarget(tabCtx, func(ev interface{}) {
		if req, ok := ev.(*network.EventRequestWillBeSent); ok {
			if parsed, err := url.Parse(req.Request.URL); err == nil {
				host := parsed.Hostname()
				if host != "" && !isIP(host) && host != "localhost" && strings.Contains(host, ".") {
					resType := strings.ToLower(req.Type.String())
					if resType == "" {
						resType = "other"
					}
					netMu.Lock()
					if networkDomains[host] == nil {
						networkDomains[host] = make(map[string]bool)
					}
					networkDomains[host][resType] = true
					netMu.Unlock()
				}
			}
		}
	})

	err = chromedp.Run(tabCtx,
		network.Enable(),
		chromedp.EmulateViewport(1920, 2080),
		chromedp.Navigate(targetURL),
		WaitWithTimeout(tabCtx, 5*time.Second),
		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil),
		chromedp.Sleep(2*time.Second),
	)
	if err != nil {
		return nil, err
	}

	onProgress("Extracting domains", 0, 0)

	netMu.Lock()
	var domains []DomainInfo
	for host, sources := range networkDomains {
		sourceList := make([]string, 0, len(sources))
		for s := range sources {
			sourceList = append(sourceList, s)
		}
		sort.Strings(sourceList)

		isExternal := !strings.HasSuffix(host, targetDomain) &&
			!strings.HasSuffix(targetDomain, host) &&
			host != targetDomain

		domains = append(domains, DomainInfo{
			Domain:   host,
			Count:    len(sourceList),
			Sources:  sourceList,
			External: isExternal,
		})
	}
	netMu.Unlock()

	sort.Slice(domains, func(i, j int) bool {
		if domains[i].External != domains[j].External {
			return domains[i].External
		}
		if domains[i].Count != domains[j].Count {
			return domains[i].Count > domains[j].Count
		}
		return domains[i].Domain < domains[j].Domain
	})

	onProgress("Resolving DNS", 0, len(domains))
	resolveDomainInfo(domains, onProgress)

	onProgress("Complete", len(domains), len(domains))

	result := &Result{
		TargetURL:    targetURL,
		TargetDomain: targetDomain,
		Domains:      domains,
		Stats:        calculateStats(domains),
		ScannedAt:    time.Now(),
	}

	// Store result in cache for future lookups
	if scanCache != nil {
		scanCache.Set(targetURL, result)
	}

	return result, nil
}

func resolveDomainInfo(domains []DomainInfo, onProgress ProgressFunc) {
	var wg sync.WaitGroup
	var resolved int
	var mu sync.Mutex
	sem := make(chan struct{}, 20)
	total := len(domains)

	for i := range domains {
		wg.Add(1)
		go func(d *DomainInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ips, err := net.LookupHost(d.Domain)
			if err == nil {
				d.IPs = ips
			}

			if len(d.IPs) > 0 {
				d.CDN = detectProviderByGeoIP(d.IPs)
			}

			mu.Lock()
			resolved++
			onProgress("Resolving DNS", resolved, total)
			mu.Unlock()
		}(&domains[i])
	}
	wg.Wait()
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
