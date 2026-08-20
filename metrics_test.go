package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// portFailRT is a RoundTripper that fails every request to port 443 (the home
// WAN 443 is ISP-blocked) and answers 200 on any other port, so the 8443
// fallback path can be exercised without real network.
type portFailRT struct{}

func (portFailRT) RoundTrip(req *http.Request) (*http.Response, error) {
	// https with no explicit port means 443 (the ISP-blocked home WAN port).
	port := req.URL.Port()
	if port == "" && req.URL.Scheme == "https" {
		port = "443"
	}
	if port == "443" {
		return nil, &netError{msg: "connection timed out"}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"photos":1,"videos":2,"usage":3,"users":1}`)),
		Request:    req,
	}, nil
}

type netError struct{ msg string }

func (e *netError) Error() string   { return e.msg }
func (e *netError) Timeout() bool   { return true }
func (e *netError) Temporary() bool { return true }

// TestFetchServiceEndpoint8443Fallback: a zhyi-domain fetch that fails on 443
// is retried on 8443 and succeeds there.
func TestFetchServiceEndpoint8443Fallback(t *testing.T) {
	w := &widgetCollector{a: &app{client: &http.Client{Transport: portFailRT{}}}}
	raw, err := w.fetchServiceEndpoint("https://immich.zhyi.xin", "/api/server/statistics", nil)
	if err != nil {
		t.Fatalf("fetchServiceEndpoint: %v", err)
	}
	if !strings.Contains(string(raw), `"photos":1`) {
		t.Fatalf("unexpected body %q", raw)
	}
}

// TestFetchServiceEndpointNoFallbackForNonZhyi: a non-zhyi domain that fails
// on 443 is not retried (alt8443URL returns ""), so the error surfaces.
func TestFetchServiceEndpointNoFallbackForNonZhyi(t *testing.T) {
	w := &widgetCollector{a: &app{client: &http.Client{Transport: portFailRT{}}}}
	if _, err := w.fetchServiceEndpoint("https://example.com", "/x", nil); err == nil {
		t.Fatal("expected error for non-zhyi domain on 443")
	}
}

// TestQueryServiceImmich8443Fallback: queryService for an immich card whose
// URL is the public 443 name still returns items via the 8443 fallback.
func TestQueryServiceImmich8443Fallback(t *testing.T) {
	w := &widgetCollector{
		a: &app{
			client: &http.Client{Transport: portFailRT{}},
			cfg: &config{widgetKeys: map[string]string{
				"immich": "test-key",
			}},
		},
	}
	items, err := w.queryService(entry{Widget: "immich", URL: "https://immich.zhyi.xin"})
	if err != nil {
		t.Fatalf("queryService: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4", len(items))
	}
	if items[0].Label != "照片" || items[0].Value != "1" {
		t.Fatalf("unexpected first item %+v", items[0])
	}
}
