package scanner

import "time"

type Result struct {
	TargetURL    string       `json:"target_url"`
	TargetDomain string       `json:"target_domain"`
	Domains      []DomainInfo `json:"domains"`
	Stats        Stats        `json:"stats"`
	ScannedAt    time.Time    `json:"scanned_at"`
}

type DomainInfo struct {
	Domain   string   `json:"domain"`
	IPs      []string `json:"ips"`
	CDN      string   `json:"cdn,omitempty"`
	Count    int      `json:"count"`
	Sources  []string `json:"sources"`
	External bool     `json:"external"`
}

type Stats struct {
	TotalDomains    int `json:"total_domains"`
	ExternalDomains int `json:"external_domains"`
	InternalDomains int `json:"internal_domains"`
}
