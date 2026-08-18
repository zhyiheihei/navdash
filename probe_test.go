package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestProbeStatusJSONKeepsZeroLatency: a sub-millisecond probe truncates to
// LatencyMS 0; the field must survive serialization, otherwise the frontend
// renders "undefinedms" and miscolours the health dot.
func TestProbeStatusJSONKeepsZeroLatency(t *testing.T) {
	raw, err := json.Marshal(probeStatus{URL: "https://zhyi.xin", Status: "up"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"latency_ms":0`) {
		t.Fatalf("latency_ms missing from %s", raw)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		e    entry
		want string
	}{
		// 家内服务聚合段（198.18.0.128 等）隧道可达，应进入探测。
		{entry{URL: "http://198.18.0.128", Access: "public"}, ""},
		{entry{URL: "http://192.168.1.5", Access: "public"}, "unknown"},
		{entry{URL: "http://10.10.10.10", Access: "public"}, "unknown"},
		{entry{URL: "https://searx.localhost", Access: "public"}, "unknown"},
		{entry{URL: ":::", Access: "public"}, "unknown"},
		{entry{URL: "ftp://example.com", Access: "public"}, "unknown"},
		{entry{URL: "https://example.com", Access: "private"}, "unknown"},
	}
	for _, c := range cases {
		got := ""
		if st := classify(c.e, time.Now().Unix()); st != nil {
			got = st.Status
		}
		if got != c.want {
			t.Errorf("classify(%s): got %q want %q", c.e.URL, got, c.want)
		}
	}
}

func TestAlt8443URL(t *testing.T) {
	cases := map[string]string{
		"https://immich.zhyi.xin":      "https://immich.zhyi.xin:8443",
		"https://nav.zhyi.xin/":        "https://nav.zhyi.xin:8443/",
		"https://x.tencent.zhyi.cc":    "https://x.tencent.zhyi.cc:8443",
		"https://a.moliy.site":         "https://a.moliy.site:8443",
		"https://example.com":          "", // 非 zhyi 域
		"https://zhyi.xin:9443":        "", // 已带显式端口
		"http://immich.zhyi.xin":       "", // 非 https
		"https://immich.zhyi.xin.evil": "", // 后缀不匹配
	}
	for in, want := range cases {
		if got := alt8443URL(in); got != want {
			t.Errorf("alt8443URL(%s): got %q want %q", in, got, want)
		}
	}
}

func TestProbeUp(t *testing.T) {
	p := newProber(time.Minute)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := p.probeOne(srv.URL, time.Now().Unix()); err != nil {
		t.Fatalf("probeOne: %v", err)
	}
	st := p.m[srv.URL]
	if st == nil || st.Status != "up" || st.Code != 200 {
		t.Fatalf("status = %+v, want up/200", st)
	}
}

func TestProbeDown(t *testing.T) {
	p := newProber(time.Minute)
	// 127.0.0.1 上已关闭的端口：传输层失败 → down；URL 非 zhyi 域，不触发 8443 重试。
	p.probeOne("http://127.0.0.1:1", time.Now().Unix())
	st := p.m["http://127.0.0.1:1"]
	if st == nil || st.Status != "down" {
		t.Fatalf("status = %+v, want down", st)
	}
}
