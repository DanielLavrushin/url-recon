package scanner

import (
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CacheEntry holds a scan result and when it was cached.
type CacheEntry struct {
	Result   *Result   `json:"result"`
	CachedAt time.Time `json:"cached_at"`
}

// Cache provides file-backed caching for scan results.
type Cache struct {
	mu      sync.Mutex
	path    string
	ttl     time.Duration
	entries map[string]CacheEntry
}

// scanCache is the package-level cache singleton.
var scanCache *Cache

// InitCache initializes the scan result cache with the given TTL.
// The cache file is stored next to the executable as "cache.json".
func InitCache(ttl time.Duration) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("Cache: failed to locate executable path, caching disabled: %v", err)
		return
	}
	dir := filepath.Dir(exe)
	path := filepath.Join(dir, "cache.json")

	c := &Cache{
		path:    path,
		ttl:     ttl,
		entries: make(map[string]CacheEntry),
	}
	c.load()
	scanCache = c
	log.Printf("Cache: initialized at %s (TTL: %s)", path, ttl)
}

// Get returns a cached result if it exists and hasn't expired.
func (c *Cache) Get(rawURL string) (*Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := normalizeURL(rawURL)
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Since(entry.CachedAt) > c.ttl {
		return nil, false
	}
	return entry.Result, true
}

// Set stores a scan result in the cache and persists to disk.
func (c *Cache) Set(rawURL string, result *Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := normalizeURL(rawURL)
	c.entries[key] = CacheEntry{
		Result:   result,
		CachedAt: time.Now(),
	}

	// Lazy cleanup: remove expired entries before saving
	now := time.Now()
	for k, e := range c.entries {
		if now.Sub(e.CachedAt) > c.ttl {
			delete(c.entries, k)
		}
	}

	c.save()
}

// load reads the cache file from disk.
func (c *Cache) load() {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Cache: failed to read %s: %v", c.path, err)
		}
		return
	}
	if err := json.Unmarshal(data, &c.entries); err != nil {
		log.Printf("Cache: failed to parse %s: %v", c.path, err)
		c.entries = make(map[string]CacheEntry)
	}
}

// save writes the cache to disk atomically (write tmp then rename).
func (c *Cache) save() {
	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		log.Printf("Cache: failed to marshal: %v", err)
		return
	}

	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		log.Printf("Cache: failed to write %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, c.path); err != nil {
		log.Printf("Cache: failed to rename %s -> %s: %v", tmp, c.path, err)
	}
}

// normalizeURL produces a consistent cache key from a URL.
func normalizeURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	// Lowercase scheme and host
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)

	// Remove default ports
	host := parsed.Hostname()
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") ||
		(parsed.Scheme == "http" && port == "80") {
		parsed.Host = host
	}

	// Remove trailing slash if path is just "/"
	if parsed.Path == "/" {
		parsed.Path = ""
	}

	// Strip fragment
	parsed.Fragment = ""

	return parsed.String()
}
