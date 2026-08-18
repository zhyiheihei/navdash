package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// validWebP returns a payload whose magic bytes sniff as image/webp.
func validWebP(payload string) []byte {
	hdr := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	return append(hdr, []byte(payload)...)
}

func testBGStore(t *testing.T) (*bgStore, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write(validWebP("DATA"))
	}))
	t.Cleanup(srv.Close)
	s := &bgStore{client: srv.Client(), url: srv.URL}
	return s, &hits
}

// TestBGStoreCachesAfterFetch: one network hit, then cache serves repeats.
func TestBGStoreCachesAfterFetch(t *testing.T) {
	s, hits := testBGStore(t)

	if _, _ = s.get(); *hits != 1 {
		t.Fatalf("first get hits = %d, want 1", *hits)
	}
	if string(s.data) != string(validWebP("DATA")) {
		t.Fatalf("cached data = %q", s.data)
	}
	if _, _ = s.get(); *hits != 1 {
		t.Fatalf("second get hit the network again (%d hits)", *hits)
	}
}

// TestBGStoreRefetchesAfterTTL: expired cache triggers one refetch with the
// same lock, so concurrent gets still only hit the network once.
func TestBGStoreRefetchesAfterTTL(t *testing.T) {
	s, hits := testBGStore(t)

	_, _ = s.get()
	s.at = time.Now().Add(-bgTTL - time.Minute) // expire
	_, _ = s.get()
	if *hits != 2 {
		t.Fatalf("got %d hits, want 2 (warmup + refetch)", *hits)
	}
	if string(s.ct) != "image/webp" {
		t.Fatalf("content type = %q", s.ct)
	}
}

// TestBGStoreKeepsOldPictureOnFetchError: a failed refetch must not blank the
// background; the stale picture is served while the TTL keeps expiring.
func TestBGStoreKeepsOldPictureOnFetchError(t *testing.T) {
	s, _ := testBGStore(t)
	_, _ = s.get()
	// Point at a dead port so the next fetch fails.
	s.client.CloseIdleConnections()
	s.url = "http://127.0.0.1:1/x"
	s.at = time.Now().Add(-bgTTL - time.Minute)
	data, _ := s.get()
	if string(data) != string(validWebP("DATA")) {
		t.Fatalf("expected stale picture to survive fetch error, got %q", data)
	}
}

// TestBGFetchRejectsNonImage: bytes that sniff as anything else than an
// image must fail the fetch even when the header claims an image type.
func TestBGFetchRejectsNonImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write([]byte("<html>not an image</html>"))
	}))
	t.Cleanup(srv.Close)
	s := &bgStore{client: srv.Client(), url: srv.URL}
	if _, _, err := s.fetch(); err == nil {
		t.Fatal("fetch of non-image bytes should fail")
	}
}

// TestHandleBG: the HTTP handler serves the cached picture with image/* CT.
func TestHandleBG(t *testing.T) {
	a := &app{bg: &bgStore{data: []byte("IMG"), ct: "image/webp", at: time.Now()}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/bg", nil)
	a.handleBG(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Body.String(); got != "IMG" {
		t.Fatalf("body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Fatalf("content type = %q", ct)
	}
}
