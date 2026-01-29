package scanner

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

type BrowserPool struct {
	mu          sync.Mutex
	allocCtx    context.Context
	allocCancel context.CancelFunc
	available   chan *BrowserInstance
	maxSize     int
	created     int
}

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

func GetPool() *BrowserPool {
	poolOnce.Do(func() {
		globalPool = NewBrowserPool(4)
	})
	return globalPool
}

func NewBrowserPool(maxSize int) *BrowserPool {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("no-first-run", true),

		chromedp.Flag("js-flags", "--max-old-space-size=256"),
		chromedp.Flag("disable-features", "TranslateUI,BlinkGenPropertyTrees"),
		chromedp.Flag("blink-settings", "imagesEnabled=true"),
	)

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

func (p *BrowserPool) Acquire(ctx context.Context) (*BrowserInstance, error) {

	select {
	case instance := <-p.available:
		instance.inUse = true
		return instance, nil
	default:

	}

	p.mu.Lock()
	if p.created >= p.maxSize {
		p.mu.Unlock()

		select {
		case instance := <-p.available:
			instance.inUse = true
			return instance, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	browserCtx, browserCancel := chromedp.NewContext(p.allocCtx)
	p.created++
	p.mu.Unlock()

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

func (p *BrowserPool) Release(instance *BrowserInstance) {
	if instance == nil || !instance.inUse {
		return
	}
	instance.inUse = false

	select {
	case p.available <- instance:

	default:

		instance.cancel()
		p.mu.Lock()
		p.created--
		p.mu.Unlock()
	}
}

func (b *BrowserInstance) NewTab(ctx context.Context) (context.Context, context.CancelFunc) {
	tabCtx, tabCancel := chromedp.NewContext(b.ctx)

	go func() {
		select {
		case <-ctx.Done():
			tabCancel()
		case <-tabCtx.Done():
		}
	}()

	return tabCtx, tabCancel
}

func (p *BrowserPool) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()

	close(p.available)
	for instance := range p.available {
		instance.cancel()
	}

	p.allocCancel()
	p.created = 0

	log.Println("Browser pool shut down")
}

func ShutdownPool() {
	if globalPool != nil {
		globalPool.Shutdown()
	}
}

type PoolStats struct {
	MaxSize   int
	Created   int
	Available int
}

func (p *BrowserPool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PoolStats{
		MaxSize:   p.maxSize,
		Created:   p.created,
		Available: len(p.available),
	}
}

func WaitWithTimeout(ctx context.Context, timeout time.Duration) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {

		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		if err := chromedp.WaitReady("body").Do(timeoutCtx); err != nil {
			return err
		}

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
