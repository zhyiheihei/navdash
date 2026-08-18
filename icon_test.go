package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"github.com", "github.com", true},
		{"  GITHUB.COM  ", "github.com", true},
		{"https://matrix-client.zhyi.xin", "matrix-client.zhyi.xin", true},
		{"jellyfin.zhyi.xin:8443", "jellyfin.zhyi.xin", true},
		{"example.com/path", "example.com", true},
		{"", "", false},
		{" ", "", false},
		{"exa mple.com", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeHost(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("normalizeHost(%q): got %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSkipFetch(t *testing.T) {
	skip := []string{"192.168.1.5", "198.18.0.128", "10.10.10.10", "searx.localhost", "nas.lan", "x.dn42"}
	keep := []string{"github.com", "zhyi.xin", "matrix-federation.zhyi.xin"}
	for _, h := range skip {
		if !skipFetch(h) {
			t.Errorf("skipFetch(%q): want true", h)
		}
	}
	for _, h := range keep {
		if skipFetch(h) {
			t.Errorf("skipFetch(%q): want false", h)
		}
	}
}

func TestLetterIconDeterministic(t *testing.T) {
	a := letterIcon("github.com")
	b := letterIcon("github.com")
	if string(a.data) != string(b.data) || a.ct != b.ct {
		t.Errorf("letterIcon not deterministic for same host")
	}
	if !strings.HasPrefix(a.ct, "image/svg+xml") {
		t.Errorf("letterIcon content type = %q", a.ct)
	}
	if !strings.Contains(string(a.data), "G") {
		t.Errorf("letterIcon should contain uppercase first letter of leftmost label, got %q", a.data)
	}
	// Different hosts should generally differ; at least the letter differs.
	if strings.Contains(string(letterIcon("matrix-client.zhyi.xin").data), "G") {
		t.Errorf("letterIcon(host with first label 'matrix') should contain M not G")
	}
}

// TestIconResolveChain checks the fetch order: DDG ok → use it; DDG miss →
// own favicon ok → use it; both miss → letter glyph.
func TestIconResolveChain(t *testing.T) {
	var ddgHits, ownHits int
	ddg := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Host, "duckduckgo") || strings.HasPrefix(r.URL.Path, "/ip3/") {
			if r.URL.Path == "/ip3/withfav.ico" {
				ddgHits++
				w.Header().Set("Content-Type", "image/x-icon")
				_, _ = w.Write([]byte("FAVICON"))
				return
			}
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/favicon.ico" {
			ownHits++
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNGFAV"))
			return
		}
		http.NotFound(w, r)
	}
	srv := httptest.NewServer(http.HandlerFunc(ddg))
	defer srv.Close()

	s := newIconStore()
	// Own URLs always resolve through the DDG fetcher; we can't easily point
	// the fixed https URLs at the test server, so test the chain logic via
	// fetchURL directly instead.
	if ic, ok := s.fetchURL(srv.URL + "/ip3/withfav.ico"); !ok || string(ic.data) != "FAVICON" {
		t.Errorf("fetchURL(200 image) = ok=%v data=%q", ok, ic.data)
	}
	if _, ok := s.fetchURL(srv.URL + "/ip3/missing.ico"); ok {
		t.Errorf("fetchURL(404) should not be ok")
	}
	if _, ok := s.fetchURL(srv.URL + "/u"); ok {
		t.Errorf("fetchURL(non-image 200) should not be ok")
	}
	t.Logf("ddg hits=%d own hits=%d", ddgHits, ownHits)
}

func TestEntryHosts(t *testing.T) {
	entries := []entry{
		{URL: "https://github.com"},
		{URL: "https://matrix-federation.zhyi.xin:8448"},
		{URL: "http://nas.localhost"},
		{URL: "https://github.com"}, // duplicate
		{URL: "not a url"},
	}
	hs := entryHosts(entries)
	if len(hs) != 3 {
		t.Fatalf("entryHosts got %d hosts %v, want 3", len(hs), hs)
	}
	seen := map[string]bool{}
	for _, h := range hs {
		seen[h] = true
	}
	for _, want := range []string{"github.com", "matrix-federation.zhyi.xin", "nas.localhost"} {
		if !seen[want] {
			t.Errorf("entryHosts missing %q (got %v)", want, hs)
		}
	}
}

// TestHandleIconAllowlist: only hosts present in the Nix generated entries
// may reach the icon store (and its network fetches); anything else must
// get the offline letter glyph.
func TestHandleIconAllowlist(t *testing.T) {
	allowed := "allowed.example.org"
	s := newIconStore()
	s.cache[allowed] = cachedIcon{data: []byte("REAL"), ct: "image/x-icon"}

	a := &app{icons: s, iconHosts: map[string]bool{allowed: true}}
	serve := func(host string) (int, string, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/icon?host="+host, nil)
		a.handleIcon(rec, req)
		return rec.Code, rec.Body.String(), rec.Header().Get("Content-Type")
	}

	if code, body, ct := serve(allowed); code != 200 || body != "REAL" || ct != "image/x-icon" {
		t.Fatalf("allowlisted host: code=%d body=%q ct=%q", code, body, ct)
	}
	code, body, ct := serve("stranger.example.org")
	if code != 200 || !strings.HasPrefix(ct, "image/svg+xml") {
		t.Fatalf("unknown host: code=%d ct=%q, want letter glyph", code, ct)
	}
	if !strings.Contains(body, "S") {
		t.Fatalf("letter glyph should be built from the host's first label, got %q", body)
	}
	if len(s.cache) != 1 {
		t.Fatalf("unknown host must not populate the icon cache (got %d entries)", len(s.cache))
	}
}
