package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/joakimcarlsson/bonk"
)

// renderTimeout bounds one rendered page load.
const renderTimeout = 45 * time.Second

// Render loads target in an isolated browser context and returns its DOM, first
// waiting for the CSS selector waitFor to appear when one is given. It honours
// ctx, is bounded by renderTimeout, counts against Options.Concurrency, and
// always tears down the page and context. Use Collect instead when the caller
// needs the media URLs the page loaded rather than its markup.
func (b *Browser) Render(
	ctx context.Context,
	target, waitFor string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case b.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-b.sem }()

	bctx, err := b.newContext()
	if err != nil {
		return "", fmt.Errorf("browser: new context: %w", err)
	}
	defer func() { _ = bctx.Close() }()

	page, err := bctx.NewPage()
	if err != nil {
		return "", fmt.Errorf("browser: new page: %w", err)
	}
	defer func() { _ = page.Close() }()
	page = page.WithContext(ctx)

	err = page.Navigate(target, bonk.WithTimeout(renderTimeout))
	if err != nil {
		return "", fmt.Errorf("browser: navigate %s: %w", target, err)
	}
	if waitFor != "" {
		err = page.Locator(waitFor).WaitFor(bonk.WaitTimeout(renderTimeout))
		if err != nil {
			return "", fmt.Errorf(
				"browser: %s: waiting for %q: %w", target, waitFor, err,
			)
		}
	}
	html, err := page.Content()
	if err != nil {
		return "", fmt.Errorf("browser: read %s: %w", target, err)
	}
	return html, nil
}
