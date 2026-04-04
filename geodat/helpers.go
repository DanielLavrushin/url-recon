package geodat

import (
	"net/netip"
	"strings"

	"github.com/urlesistiana/v2dat/v2data"
)

func LoadDomainsFromCategories(geodataPath string, categories []string) ([]Entry, error) {
	if geodataPath == "" || len(categories) == 0 {
		return nil, nil
	}

	var allEntries []Entry

	save := func(tag string, domainList []*v2data.Domain) error {
		for _, d := range domainList {
			entry := Entry{Value: d.Value}

			if strings.HasPrefix(d.Value, "include:") {
				entry.Type = "include"
				entry.Value = strings.TrimPrefix(d.Value, "include:")
			} else {
				switch d.Type {
				case v2data.Domain_Plain:
					entry.Type = "keyword"
				case v2data.Domain_Regex:
					entry.Type = "regexp"
				case v2data.Domain_Full:
					entry.Type = "full"
				case v2data.Domain_Domain:
					entry.Type = "domain"
				default:
					entry.Type = "domain"
				}
			}
			allEntries = append(allEntries, entry)
		}
		return nil
	}

	if err := streamGeoSite(geodataPath, categories, save); err != nil {
		return nil, err
	}

	return allEntries, nil
}

// LoadIPPrefixes loads IP prefixes from the specified categories, grouped by category name.
// This is more efficient than LoadIpsFromCategories as it returns parsed netip.Prefix values
// instead of string representations.
func LoadIPPrefixes(geodataPath string, categories []string) (map[string][]netip.Prefix, error) {
	if geodataPath == "" || len(categories) == 0 {
		return nil, nil
	}

	result := make(map[string][]netip.Prefix)

	save := func(tag string, geo *v2data.GeoIP) error {
		for _, cidr := range geo.GetCidr() {
			ip, ok := netip.AddrFromSlice(cidr.Ip)
			if !ok {
				continue
			}
			prefix, err := ip.Prefix(int(cidr.Prefix))
			if err != nil {
				continue
			}
			result[tag] = append(result[tag], prefix)
		}
		return nil
	}

	if err := streamGeoIP(geodataPath, categories, save); err != nil {
		return nil, err
	}

	return result, nil
}

func LoadIpsFromCategories(geodataPath string, categories []string) ([]Entry, error) {
	if geodataPath == "" || len(categories) == 0 {
		return nil, nil
	}

	var allEntries []Entry

	save := func(tag string, geo *v2data.GeoIP) error {
		for _, cidr := range geo.GetCidr() {
			ip, ok := netip.AddrFromSlice(cidr.Ip)
			if !ok {
				continue
			}
			prefix, err := ip.Prefix(int(cidr.Prefix))
			if err != nil {
				continue
			}
			allEntries = append(allEntries, Entry{
				Type:  "cidr",
				Value: prefix.String(),
			})
		}
		return nil
	}

	if err := streamGeoIP(geodataPath, categories, save); err != nil {
		return nil, err
	}

	return allEntries, nil
}
