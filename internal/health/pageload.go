package health

import (
	"fmt"
	"io"
	"net/url"
	"time"
)

// pageloadResult captures real page-load timing through the proxy.
type pageloadResult struct {
	TTFB time.Duration // time to first byte
	Full time.Duration // full body download
	Bytes int64
}

// testPageload fetches a real asset through the proxy with connection reuse and
// measures time-to-first-byte and full load — closer to felt UX than a raw
// throughput number. Uses the HTTP target (a real small page) by default.
func testPageload(d dialer, cfg Config) (pageloadResult, error) {
	target := cfg.PageloadURL
	if target == "" {
		// A small, globally reachable real page.
		target = "https://www.cloudflare.com/cdn-cgi/trace"
	}
	u, err := url.Parse(target)
	if err != nil {
		return pageloadResult{}, err
	}
	client := httpClientFor(d, u, cfg.Timeout)

	start := time.Now()
	resp, err := client.Get(target)
	if err != nil {
		return pageloadResult{}, err
	}
	defer resp.Body.Close()
	ttfb := time.Since(start)

	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return pageloadResult{}, err
	}
	return pageloadResult{TTFB: ttfb, Full: time.Since(start), Bytes: n}, nil
}

func (r pageloadResult) extra() string {
	return fmt.Sprintf("ttfb=%s full=%s bytes=%d",
		FormatDuration(r.TTFB), FormatDuration(r.Full), r.Bytes)
}
