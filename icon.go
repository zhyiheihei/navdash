// Card icons for the service portal.
//
// Every card gets an icon from one of three sources, tried in order:
//
//  1. the DuckDuckGo icons API (https://icons.duckduckgo.com/ip3/<host>.ico),
//     which covers real favicons for virtually every public site;
//  2. the service's own https://<host>/favicon.ico, so self-hosted services
//     (e.g. zhyi.* vhosts that serve a favicon) can use their real icon;
//  3. a deterministic letter glyph generated here, which guarantees that even
//     hosts unknown to the icon API still get an icon.
//
// Fetches run once in a background prefetch at startup plus on demand for
// cache misses; results are cached in memory for the process lifetime.
// Hosts that cannot be public web hosts (IP literals, private TLDs) skip
// network fetching and go straight to the letter glyph.

package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	iconFetchTimeout     = 4 * time.Second
	iconMaxSize          = 256 << 10 // 256 KiB, far beyond any favicon
	iconFetchConcurrency = 4
	iconUserAgent        = "navdash-icon-fetcher/1"
)

// cachedIcon is one host's resolved icon. neg marks a "no favicon found"
// result so we don't hammer a host that yields nothing; the letter glyph is
// deterministic so it is cached like a success.
type cachedIcon struct {
	data []byte
	ct   string
	neg  bool
}

// iconStore resolves and caches card icons. All methods are safe for
// concurrent use; the HTTP client has a short timeout since every fetch
// happens inside a request path or the startup prefetch.
type iconStore struct {
	mu     sync.RWMutex
	cache  map[string]cachedIcon
	client *http.Client
}

func newIconStore() *iconStore {
	return &iconStore{
		cache:  map[string]cachedIcon{},
		client: &http.Client{Timeout: iconFetchTimeout},
	}
}

// normalizeHost validates and canonicalizes a ?host= query value: lowercase,
// strip a leading scheme if any, drop a :port suffix, and keep only
// hostname-safe characters. Returns false for anything empty or weird.
func normalizeHost(raw string) (string, bool) {
	h := strings.TrimSpace(strings.ToLower(raw))
	if h == "" {
		return "", false
	}
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return "", false
		}
	}
	if h == "" {
		return "", false
	}
	return h, true
}

// skipFetch reports whether host is clearly not a public web host and should
// not be hit over the network (IP literals, private/local TLDs).
func skipFetch(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	l := strings.ToLower(host)
	for _, suf := range []string{".localhost", ".local", ".lan", ".dn42", ".internal", ".home"} {
		if strings.HasSuffix(l, suf) {
			return true
		}
	}
	return false
}

// iconPalette is a small set of brand-leaning colors; the hash picks one so
// the same host always gets the same glyph color.
var iconPalette = []string{
	"#4d6bfe", "#7c5cff", "#d75fff", "#ec4899",
	"#f97316", "#eab308", "#22c55e", "#14b8a6",
	"#0ea5e9", "#64748b",
}

// letterIcon builds a deterministic letter glyph for host: the first letter
// of the leftmost label, colored by hashing the full host.
func letterIcon(host string) cachedIcon {
	label := host
	if i := strings.IndexByte(label, '.'); i >= 0 {
		label = label[:i]
	}
	ch := "?"
	if label != "" {
		ch = strings.ToUpper(label[:1])
	}
	sum := sha256.Sum256([]byte(host))
	color := iconPalette[int(sum[0])%len(iconPalette)]
	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32">`+
			`<rect width="32" height="32" rx="8" fill="%s"/>`+
			`<text x="16" y="21.5" font-family="system-ui,-apple-system,sans-serif" `+
			`font-size="16" font-weight="600" fill="rgba(255,255,255,0.94)" text-anchor="middle">%s</text>`+
			`</svg>`, color, ch)
	data := []byte(svg)
	return cachedIcon{data: data, ct: "image/svg+xml; charset=utf-8"}
}

// fetchURL downloads u, returning the body only for a 200 image response
// within size limits. Never blocks longer than iconFetchTimeout.
func (s *iconStore) fetchURL(u string) (cachedIcon, bool) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return cachedIcon{}, false
	}
	req.Header.Set("User-Agent", iconUserAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/svg+xml,image/x-icon,image/vnd.microsoft.icon,*/*")
	resp, err := s.client.Do(req)
	if err != nil {
		return cachedIcon{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return cachedIcon{}, false
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "image/") {
		return cachedIcon{}, false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, iconMaxSize+1))
	if err != nil || len(data) == 0 || len(data) > iconMaxSize {
		return cachedIcon{}, false
	}
	return cachedIcon{data: data, ct: ct, neg: false}, true
}

// resolve walks the fetch chain for host. Sources that fail are skipped
// silently; the letter glyph is the guaranteed final fallback.
func (s *iconStore) resolve(host string) cachedIcon {
	if !skipFetch(host) {
		if ic, ok := s.fetchURL("https://icons.duckduckgo.com/ip3/" + host + ".ico"); ok {
			return ic
		}
		if ic, ok := s.fetchURL("https://" + host + "/favicon.ico"); ok {
			return ic
		}
	}
	return letterIcon(host)
}

// get returns host's icon, resolving on a cache miss. Resolution may touch
// the network, so this is normally driven by the background prefetch.
func (s *iconStore) get(host string) cachedIcon {
	s.mu.RLock()
	ic, ok := s.cache[host]
	s.mu.RUnlock()
	if ok {
		return ic
	}
	ic = s.resolve(host)
	s.mu.Lock()
	s.cache[host] = ic
	s.mu.Unlock()
	return ic
}

// prefetch warms the cache for all entry hosts in the background; a few
// workers keep the DDG API and slow private hosts from serializing. Logs a
// summary so a deployment can confirm how many cards got real favicons.
func (s *iconStore) prefetch(hosts []string) {
	seen := map[string]bool{}
	var unique []string
	for _, h := range hosts {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		unique = append(unique, h)
	}
	if len(unique) == 0 {
		return
	}

	ch := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < iconFetchConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for h := range ch {
				s.get(h)
			}
		}()
	}
	start := time.Now()
	for _, h := range unique {
		ch <- h
	}
	close(ch)
	wg.Wait()

	real, letter := 0, 0
	s.mu.RLock()
	for _, ic := range s.cache {
		if strings.HasPrefix(ic.ct, "image/svg+xml") && ic.neg == false && len(ic.data) < 512 {
			// only our own glyphs are tiny inline SVGs; anything else is a
			// real favicon (DDG/self) or an API placeholder we refused
			letter++
		} else {
			real++
		}
	}
	s.mu.RUnlock()
	log.Printf("icon: prefetched %d hosts (%d real favicons, %d letter glyphs) in %s",
		len(unique), real, letter, time.Since(start).Round(time.Millisecond))
}

// handleIcon serves a card icon by host. The response is cached client-side
// for an hour; the letter glyphs are deterministic, real favicons change
// rarely, so one hour is a good refresh balance.
//
// Network fetches are restricted to hosts that actually appear in the Nix
// generated entries; anything else gets the offline letter glyph. Without
// this, anyone on the internet could make this server probe arbitrary
// hosts' /favicon.ico.
func (a *app) handleIcon(w http.ResponseWriter, r *http.Request) {
	host, ok := normalizeHost(r.URL.Query().Get("host"))
	if !ok {
		http.Error(w, "bad host", http.StatusBadRequest)
		return
	}
	ic := letterIcon(host)
	if a.iconHosts[host] {
		ic = a.icons.get(host)
	}
	w.Header().Set("Content-Type", ic.ct)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(ic.data)
}

// entryHosts extracts the unique normalized hostnames from entries, for the
// icon prefetch. Private/local hosts are included; prefetch skips fetching
// and resolves them straight to letter glyphs.
func entryHosts(entries []entry) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range entries {
		h := hostOf(e.URL)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// hostOf returns the normalized hostname of a URL (scheme and path dropped,
// port stripped). Empty string for malformed URLs.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	h, _ := normalizeHost(u.Hostname())
	return h
}
