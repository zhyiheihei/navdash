package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Health probing for the service grid (P2: latency & reachability, matching
// the status widget semantics of gethomepage/homepage-dashboard).
//
// Each public entry is HEAD-probed from this host and the result cached;
// private, localhost and LAN-only entries are reported as "unknown", because
// the portal runs on a public cloud host that cannot reach them. Reporting
// those as "down" would be a false alarm, so unknown is the honest state.
// Latency colouring (green/amber/red thresholds) is done on the frontend.

type probeStatus struct {
	URL    string `json:"url"`
	Status string `json:"status"` // up | down | unknown
	// LatencyMS keeps its zero value in JSON on purpose: a sub-millisecond
	// probe truncates to 0, and omitempty used to drop the field entirely,
	// which the frontend then rendered as "undefinedms".
	LatencyMS int   `json:"latency_ms"`
	Code      int   `json:"code,omitempty"`
	UpdatedAt int64 `json:"updated_at"`
}

type prober struct {
	interval time.Duration
	client   *http.Client

	mu sync.RWMutex
	m  map[string]*probeStatus
}

func probeInterval() time.Duration {
	if v := os.Getenv("NAVDASH_PROBE_INTERVAL"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 60 * time.Second
}

func newProber(interval time.Duration) *prober {
	// Reachability probe, not a TLS audit: certificates are not validated
	// (LAN services use short-lived/self-signed names like zhyi.dn42) and
	// redirects are NOT followed — a 3xx response already proves the
	// service is answering (OAuth apps redirect to their login page by
	// design and would otherwise report "too many redirects").
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &prober{
		interval: interval,
		client: &http.Client{
			Transport: tr,
			Timeout:   6 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		m: map[string]*probeStatus{},
	}
}

func (p *prober) snapshot() map[string]*probeStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]*probeStatus, len(p.m))
	for k, v := range p.m {
		out[k] = v
	}
	return out
}

func (p *prober) set(st *probeStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[st.URL] = st
}

// loop runs probe rounds forever; the first round runs immediately.
func (p *prober) loop(entries []entry) {
	p.round(entries)
	for range time.Tick(p.interval) {
		p.round(entries)
	}
}

func (p *prober) round(entries []entry) {
	now := time.Now().Unix()
	var jobs []entry
	for _, e := range entries {
		st := classify(e, now)
		p.set(st)
		if st.Status == "unknown" {
			continue
		}
		jobs = append(jobs, e)
	}
	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var downList []string
	for _, e := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(e entry) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := p.probeOne(e.URL, now); err != nil {
				mu.Lock()
				downList = append(downList, e.URL+": "+err.Error())
				mu.Unlock()
			}
		}(e)
	}
	wg.Wait()
	var up, down int
	for _, st := range p.snapshot() {
		switch st.Status {
		case "up":
			up++
		case "down":
			down++
		}
	}
	log.Printf("probe: round done (%d probed, %d up, %d down, %d unknown)",
		len(jobs), up, down, len(p.snapshot())-up-down)
	for _, d := range downList {
		log.Printf("probe: down - %s", d)
	}
}

// classify decides probing eligibility for one entry.
func classify(e entry, now int64) *probeStatus {
	st := &probeStatus{URL: e.URL, Status: "unknown", UpdatedAt: now}
	if e.Access != "public" {
		return st
	}
	u, err := url.Parse(e.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return st
	}
	host := u.Hostname()
	if strings.Contains(host, "localhost") {
		return st
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		// DNS failing for a public entry usually means the name only
		// resolves on an internal network (e.g. *.zhyi.dn42), not that
		// the service is down — stay unknown.
		return st
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip != nil && !isPrivateIP(ip) {
			// At least one publicly reachable address: probe it.
			st.Status = ""
			return st
		}
	}
	return st
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// CGNAT 100.64.0.0/10 — non-routable from the public internet.
		if v4[0] == 100 && v4[1] >= 64 && v4[1] < 128 {
			return true
		}
		return false
	}
	if len(ip) == net.IPv6len {
		b := ip.To16()
		if b[0]&0xfe == 0xfc { // fc00::/7 ULA (includes fd00::/8)
			return true
		}
		if b[0] == 0x20 && b[1] == 0x00 { // 200::/7 yggdrasil
			return true
		}
	}
	return false
}

// probeOne measures latency via HEAD, falling back to a ranged GET when the
// server rejects HEAD (405/501). Any HTTP response counts as reachable
// (status code is surfaced for the user); only transport errors are "down".
// When a zhyi domain fails on its default port 443, it is retried on 8443:
// the home WAN port 443 is blocked by the ISP, so the public entry for
// on-premise services is :8443 (router DNATs 8443 -> opi5p:443). Cloud-hosted
// services answer on 443 and never trigger the retry.
func (p *prober) probeOne(rawURL string, now int64) error {
	code, ms, err := p.measure(rawURL)
	if err != nil {
		if alt := alt8443URL(rawURL); alt != "" {
			code, ms, err = p.measure(alt)
		}
	}
	st := &probeStatus{URL: rawURL, UpdatedAt: now}
	if err != nil {
		st.Status = "down"
	} else {
		st.Status = "up"
		st.Code = code
		st.LatencyMS = ms
	}
	p.set(st)
	return err
}

// measure does HEAD with a ranged-GET fallback and returns the status code,
// latency in milliseconds, and transport error (nil when any HTTP response
// was received).
func (p *prober) measure(rawURL string) (code int, ms int, err error) {
	start := time.Now()
	code, err = p.head(rawURL)
	if err != nil {
		code, err = p.get(rawURL)
	}
	return code, int(time.Since(start).Milliseconds()), err
}

// alt8443URL returns the :8443 variant for a zhyi-domain HTTPS URL that has
// no explicit port, or "" when the retry does not apply.
func alt8443URL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Port() != "" {
		return ""
	}
	host := u.Hostname()
	if !strings.HasSuffix(host, ".zhyi.xin") &&
		!strings.HasSuffix(host, ".zhyi.cc") &&
		!strings.HasSuffix(host, ".moliy.site") {
		return ""
	}
	c := *u
	c.Host = host + ":8443"
	return c.String()
}

func (p *prober) head(rawURL string) (int, error) {
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "navdash-probe/0.3")
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		return 0, fmt.Errorf("HEAD rejected with %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func (p *prober) get(rawURL string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "navdash-probe/0.3")
	req.Header.Set("Range", "bytes=0-0")
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 512)
	return resp.StatusCode, nil
}
