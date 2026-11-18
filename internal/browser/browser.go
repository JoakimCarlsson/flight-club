// Package browser is the shared scraping harness. It wraps bonk (a
// stealth-first Chrome DevTools Protocol driver) so every program that sits
// behind bot protection scrapes through one managed browser: isolated
// contexts per job, and capture of the response bodies a page loads, such as
// a site's own search API JSON.
package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joakimcarlsson/bonk"
)

// Options configures the managed browser.
type Options struct {
	// ChromePath is an explicit Chrome/Chromium binary; empty auto-detects.
	ChromePath string
	// Headless runs without a visible window.
	Headless bool
	// Concurrency caps how many Collect calls run at once (browsers are heavy).
	Concurrency int
	// NoSandbox launches Chrome with --no-sandbox and --disable-dev-shm-usage.
	// Required when running as a non-root user inside a container (where Chrome's
	// sandbox namespaces are unavailable and /dev/shm is tiny); harmless on a
	// normal desktop.
	NoSandbox bool
}

// Browser is a launched Chrome instance that hands out isolated scrape jobs. It
// is safe for concurrent use; Collect calls are bounded by Options.Concurrency.
type Browser struct {
	b       *bonk.Browser
	sem     chan struct{}
	profile string
}

// New launches Chrome via bonk with stealth enabled. It returns an error rather
// than panicking when the browser cannot start (e.g. Chrome is missing).
func New(opts Options) (*Browser, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("browser: cache directory: %w", err)
	}
	root := filepath.Join(cache, "flight-club", "browser")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	profile, err := os.MkdirTemp(root, "chrome-")
	if err != nil {
		return nil, err
	}
	launch := []bonk.LaunchOption{
		bonk.UserDataDir(profile),
		bonk.Stealth(true),
		bonk.Headless(opts.Headless),
	}
	if opts.ChromePath != "" {
		launch = append(launch, bonk.ChromePath(opts.ChromePath))
	}
	if opts.NoSandbox {
		launch = append(
			launch,
			bonk.Args("--no-sandbox", "--disable-dev-shm-usage"),
		)
	}
	b, err := bonk.Launch(launch...)
	if opts.ChromePath == "" && errors.Is(err, bonk.ErrChromeNotFound) {
		if cached := cachedChromium(cache); cached != "" {
			b, err = bonk.Launch(append(launch, bonk.ChromePath(cached))...)
		}
	}
	if err != nil {
		_ = os.RemoveAll(profile)
		return nil, fmt.Errorf("browser: launch: %w", err)
	}
	n := opts.Concurrency
	if n < 1 {
		n = 2
	}
	return &Browser{b: b, sem: make(chan struct{}, n), profile: profile}, nil
}

// Close shuts down the browser.
func (b *Browser) Close() error {
	err := b.b.Close()
	_ = os.RemoveAll(b.profile)
	return err
}

// Resource is a media URL the page requested, with its content type.
type Resource struct {
	URL         string
	ContentType string
}

// Body is a captured response body (e.g. a listing XHR's JSON).
type Body struct {
	URL         string
	ContentType string
	Data        []byte
}

// Captured is the result of one Collect: the rendered HTML, the media resource
// URLs the page loaded, and any response bodies that matched CaptureBody.
type Captured struct {
	HTML string
	// URL is the address the page ended on, after any redirects.
	URL       string
	Resources []Resource
	Bodies    []Body
}

// CollectOpts tunes a single Collect.
type CollectOpts struct {
	// Timeout bounds the navigation; defaults to 30s.
	Timeout time.Duration
	// Scrolls drives the page to the bottom this many times to load lazy feeds.
	Scrolls int
	// ScrollDelay waits between scrolls for content to load; defaults to 1s.
	ScrollDelay time.Duration
	// UserAgent overrides the context user agent when set.
	UserAgent string
	// Accept decides which response URLs are recorded as media Resources.
	// Nil accepts video/image/HLS/DASH responses.
	Accept func(url, contentType string) bool
	// CaptureBody decides which response bodies to read into Captured.Bodies.
	// Nil captures nothing.
	CaptureBody func(url, contentType string) bool
	// WarmUp is a page opened in the same context before url, such as the
	// site's home page, so its bot protection scripts can earn the cookies
	// a deep link is refused without. Empty skips it.
	WarmUp string
	// WarmUpDelay is how long the WarmUp page is given to run its scripts
	// before url is opened; defaults to 6s.
	WarmUpDelay time.Duration
	// Prepare is JavaScript evaluated on the WarmUp page once it has had
	// its delay, before url is opened, such as to leave the search a site's
	// own form would store for its results page. Empty skips it; it is
	// ignored without WarmUp.
	Prepare string
}

// Collect navigates url in an isolated context, optionally scrolling to trigger
// lazy loading, and returns the captured media URLs, matched response bodies,
// and final HTML. It honors ctx for deadlines and cancellation, and always
// tears down the page and context.
func (b *Browser) Collect(
	ctx context.Context,
	url string,
	opts CollectOpts,
) (Captured, error) {
	if err := ctx.Err(); err != nil {
		return Captured{}, err
	}
	select {
	case b.sem <- struct{}{}:
	case <-ctx.Done():
		return Captured{}, ctx.Err()
	}
	defer func() { <-b.sem }()

	var ctxOpts []bonk.ContextOption
	if opts.UserAgent != "" {
		ctxOpts = append(ctxOpts, bonk.WithUserAgent(opts.UserAgent))
	}
	bctx, err := b.b.NewContext(ctxOpts...)
	if err != nil {
		return Captured{}, fmt.Errorf("browser: new context: %w", err)
	}
	defer func() { _ = bctx.Close() }()

	page, err := bctx.NewPage()
	if err != nil {
		return Captured{}, fmt.Errorf("browser: new page: %w", err)
	}
	defer func() { _ = page.Close() }()
	page = page.WithContext(ctx)

	if err := warmUp(ctx, page, opts); err != nil {
		return Captured{}, err
	}

	accept := opts.Accept
	if accept == nil {
		accept = defaultAccept
	}

	var mu sync.Mutex
	seen := make(map[string]bool)
	var resources []Resource
	var bodies []Body

	unsub := page.OnResponse(func(r *bonk.Response) {
		ct := headerLookup(r.Headers, "content-type")
		if opts.CaptureBody != nil && opts.CaptureBody(r.URL, ct) {
			if data, err := r.Body(); err == nil {
				mu.Lock()
				bodies = append(
					bodies,
					Body{URL: r.URL, ContentType: ct, Data: data},
				)
				mu.Unlock()
			}
		}
		if accept(r.URL, ct) {
			mu.Lock()
			if !seen[r.URL] {
				seen[r.URL] = true
				resources = append(
					resources,
					Resource{URL: r.URL, ContentType: ct},
				)
			}
			mu.Unlock()
		}
	})
	defer unsub()

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if err := page.Navigate(url, bonk.WithTimeout(timeout)); err != nil {
		return Captured{}, fmt.Errorf("browser: navigate %s: %w", url, err)
	}

	delay := opts.ScrollDelay
	if delay <= 0 {
		delay = time.Second
	}
	for i := 0; i < opts.Scrolls; i++ {
		if _, err := page.Evaluate(
			"window.scrollTo(0, document.body.scrollHeight)",
		); err != nil {
			break
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return Captured{}, ctx.Err()
		}
	}

	html, err := page.Content()
	if err != nil {
		html = ""
	}

	final, err := page.URL()
	if err != nil {
		final = ""
	}

	mu.Lock()
	out := Captured{
		HTML:      html,
		URL:       final,
		Resources: append([]Resource(nil), resources...),
		Bodies:    append([]Body(nil), bodies...),
	}
	mu.Unlock()
	return out, nil
}

// warmUp opens opts.WarmUp in page and waits opts.WarmUpDelay, moving the
// pointer about meanwhile the way a visitor would, so the site's bot
// protection sees an ordinary visit before the deep link, then evaluates
// opts.Prepare there. It does nothing when opts.WarmUp is empty, and
// returns an error when the navigation or the script fails or ctx ends.
func warmUp(ctx context.Context, page *bonk.Page, opts CollectOpts) error {
	if opts.WarmUp == "" {
		return nil
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if err := page.Navigate(opts.WarmUp, bonk.WithTimeout(timeout)); err != nil {
		return fmt.Errorf("browser: warm up %s: %w", opts.WarmUp, err)
	}
	delay := opts.WarmUpDelay
	if delay <= 0 {
		delay = 6 * time.Second
	}
	const steps = 6
	for i := range steps {
		_ = page.Mouse().Move(
			float64(200+i*137%900),
			float64(150+i*89%500),
		)
		select {
		case <-time.After(delay / steps):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if opts.Prepare != "" {
		if _, err := page.Evaluate(opts.Prepare); err != nil {
			return fmt.Errorf("browser: prepare on %s: %w", opts.WarmUp, err)
		}
	}
	return nil
}

// defaultAccept records responses that look like playable media or images.
func defaultAccept(_ string, contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.HasPrefix(ct, "video/") ||
		strings.HasPrefix(ct, "image/") ||
		strings.Contains(ct, "mpegurl") ||
		strings.Contains(ct, "dash+xml")
}

// headerLookup returns the first header value matching key, case-insensitively.
func headerLookup(h map[string]string, key string) string {
	for k, v := range h {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// cachedChromium finds the newest Chromium installed in Playwright's user cache
// when Chrome is absent from the host's usual executable locations.
func cachedChromium(cache string) string {
	matches, _ := filepath.Glob(
		filepath.Join(
			cache,
			"ms-playwright",
			"chromium-*",
			"chrome-linux64",
			"chrome",
		),
	)
	var selected string
	var newest time.Time
	for _, candidate := range matches {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 &&
			(selected == "" || info.ModTime().After(newest)) {
			selected, newest = candidate, info.ModTime()
		}
	}
	return selected
}
