// Hero background image proxy.
//
// Serves a random picture behind the hero section, proxied from the
// self-hosted 樱雨社 random-image endpoint https://t.alcy.cc/ysz/ so the
// browser only talks to navdash (no third-party hotlinking, and the fetch
// happens on the server where the endpoint is reachable). One picture is
// cached in memory for bgTTL, then the next request refetches a fresh one.
// The canvas particle layer stays on top, so this is a soft backdrop.

package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	bgURL          = "https://t.alcy.cc/ysz/"
	bgTTL          = 30 * time.Minute
	bgMaxSize      = 8 << 20 // 8 MiB ceiling for a random picture
	bgFetchTimeout = 20 * time.Second
)

type bgStore struct {
	mu     sync.Mutex
	client *http.Client
	url    string
	data   []byte
	ct     string
	at     time.Time
}

func newBGStore() *bgStore {
	return &bgStore{client: &http.Client{Timeout: bgFetchTimeout}, url: bgURL}
}

// get returns the current background picture, refetching when the cached one
// is older than bgTTL or missing. Concurrent requests share a single fetch
// (the mutex is held across it) so a burst of page loads only pulls once.
func (s *bgStore) get() (data []byte, ct string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data) > 0 && time.Since(s.at) < bgTTL {
		return s.data, s.ct
	}
	data, ct, err := s.fetch()
	if err != nil {
		log.Printf("bg: fetch failed, keeping previous: %v", err)
		if len(s.data) > 0 {
			return s.data, s.ct
		}
		return nil, ""
	}
	s.data, s.ct, s.at = data, ct, time.Now()
	log.Printf("bg: refreshed (%d bytes, %s)", len(data), ct)
	return data, ct
}

func (s *bgStore) fetch() ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, s.url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "navdash-bg/1")
	req.Header.Set("Accept", "image/webp,image/jpeg,image/png,*/*")
	resp, err := s.client.Do(req) // client follows the 301 to /ysz/
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, bgMaxSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > bgMaxSize {
		return nil, "", fmt.Errorf("oversized (%d bytes)", len(data))
	}
	// Trust the bytes, not the header: a missing Content-Type used to become
	// a literal "image/*" which X-Content-Type-Options: nosniff rejects.
	// Sniff the magic bytes; the upstream header is only believed for SVG,
	// which has no magic bytes and sniffs as text/xml.
	ct := http.DetectContentType(data)
	if !strings.HasPrefix(ct, "image/") {
		up := resp.Header.Get("Content-Type")
		textish := strings.HasPrefix(ct, "text/xml") || strings.HasPrefix(ct, "text/plain")
		if strings.HasPrefix(up, "image/svg+xml") && textish {
			ct = up
		} else {
			return nil, "", fmt.Errorf("not a recognized image (detected %q)", ct)
		}
	}
	return data, ct, nil
}

// handleBG serves the cached hero background picture. The response is
// revalidated by the store's TTL, so the browser may cache briefly.
func (a *app) handleBG(w http.ResponseWriter, r *http.Request) {
	data, ct := a.bg.get()
	if data == nil {
		http.Error(w, "background unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "max-age=600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}
