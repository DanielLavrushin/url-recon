package scanner

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// BrowserPool manages a pool of reusable Chrome browser contexts
type BrowserPool struct {
	mu          sync.Mutex
	allocCtx    context.Context
	allocCancel context.CancelFunc
	available   chan *BrowserInstance
	maxSize     int
	created     int
}

// BrowserInstance represents a reusable browser tab
type BrowserInstance struct {
	ctx    context.Context
	cancel context.CancelFunc
	pool   *BrowserPool
	inUse  bool
}

var (
	globalPool *BrowserPool
	poolOnce   sync.Once
)

// GetPool returns the global browser pool (singleton)
func GetPool() *BrowserPool {
	poolOnce.Do(func() {
		globalPool = NewBrowserPool(4) // Max 4 concurrent browsers (matches job queue)
	})
	return globalPool
}

// NewBrowserPool creates a new browser pool with the specified max size
func NewBrowserPool(maxSize int) *BrowserPool {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true), // Prevent /dev/shm issues in containers
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("no-first-run", true),
		// Memory optimization flags
		chromedp.Flag("js-flags", "--max-old-space-size=256"),
		chromedp.Flag("disable-features", "TranslateUI,BlinkGenPropertyTrees"),
		chromedp.Flag("blink-settings", "imagesEnabled=true"), // Can set to false to save bandwidth
	)

	// Only add --no-sandbox if explicitly requested
	if os.Getenv("CHROMEDP_NO_SANDBOX") == "true" {
		opts = append(opts, chromedp.Flag("no-sandbox", true))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

	pool := &BrowserPool{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		available:   make(chan *BrowserInstance, maxSize),
		maxSize:     maxSize,
		created:     0,
	}

	return pool
}

// Acquire gets a browser instance from the pool or creates a new one
func (p *BrowserPool) Acquire(ctx context.Context) (*BrowserInstance, error) {
	// Try to get an available instance
	select {
	case instance := <-p.available:
		instance.inUse = true
		return instance, nil
	default:
		// No available instance, try to create one
	}

	p.mu.Lock()
	if p.created >= p.maxSize {
		p.mu.Unlock()
		// Wait for an instance to become available
		select {
		case instance := <-p.available:
			instance.inUse = true
			return instance, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Create new browser context
	browserCtx, browserCancel := chromedp.NewContext(p.allocCtx)
	p.created++
	p.mu.Unlock()

	// Initialize the browser by running a simple action
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		p.mu.Lock()
		p.created--
		p.mu.Unlock()
		return nil, err
	}

	instance := &BrowserInstance{
		ctx:    browserCtx,
		cancel: browserCancel,
		pool:   p,
		inUse:  true,
	}

	return instance, nil
}

// Release returns a browser instance to the pool
func (p *BrowserPool) Release(instance *BrowserInstance) {
	if instance == nil || !instance.inUse {
		return
	}
	instance.inUse = false

	// Try to return to pool, or discard if pool is full
	select {
	case p.available <- instance:
		// Returned to pool
	default:
		// Pool is full, close this instance
		instance.cancel()
		p.mu.Lock()
		p.created--
		p.mu.Unlock()
	}
}

// NewTab creates a new tab context within this browser instance
func (b *BrowserInstance) NewTab(ctx context.Context) (context.Context, context.CancelFunc) {
	tabCtx, tabCancel := chromedp.NewContext(b.ctx)

	// Combine with parent context for cancellation
	go func() {
		select {
		case <-ctx.Done():
			tabCancel()
		case <-tabCtx.Done():
		}
	}()

	return tabCtx, tabCancel
}

// Shutdown closes all browser instances and cleans up
func (p *BrowserPool) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Close all available instances
	close(p.available)
	for instance := range p.available {
		instance.cancel()
	}

	// Cancel the allocator
	p.allocCancel()
	p.created = 0

	log.Println("Browser pool shut down")
}

// ShutdownPool closes the global pool
func ShutdownPool() {
	if globalPool != nil {
		globalPool.Shutdown()
	}
}

// PoolStats returns current pool statistics
type PoolStats struct {
	MaxSize   int
	Created   int
	Available int
}

// Stats returns the current pool statistics
func (p *BrowserPool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PoolStats{
		MaxSize:   p.maxSize,
		Created:   p.created,
		Available: len(p.available),
	}
}

// WaitWithTimeout waits for page to be ready with smart detection
func WaitWithTimeout(ctx context.Context, timeout time.Duration) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		// Create a timeout context
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		// Wait for body to be ready first
		if err := chromedp.WaitReady("body").Do(timeoutCtx); err != nil {
			return err
		}

		// Wait for network to be mostly idle (no pending requests for 500ms)
		// This is better than a fixed sleep as it adapts to page complexity
		return chromedp.Poll(`
			new Promise(resolve => {
				let lastCount = performance.getEntriesByType('resource').length;
				let stableTime = 0;
				const check = () => {
					const currentCount = performance.getEntriesByType('resource').length;
					if (currentCount === lastCount) {
						stableTime += 100;
						if (stableTime >= 500) {
							resolve(true);
							return;
						}
					} else {
						stableTime = 0;
						lastCount = currentCount;
					}
					setTimeout(check, 100);
				};
				setTimeout(check, 100);
			})
		`, nil, chromedp.WithPollingTimeout(timeout)).Do(timeoutCtx)
	})
}
