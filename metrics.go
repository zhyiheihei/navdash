package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Live service data for cards, recovered from the deleted homepage-dashboard
// config's widgets (homepage-dashboard-config.nix).
//
// Two kinds of data are collected on a fixed interval and cached:
//
//   - prometheusmetric: host CPU / memory / disk utilisation, queried from a
//     read-only Prometheus endpoint (prometheusURL). The frontend renders
//     these as small labelled percentage bars on the card.
//
//   - Service-internal widgets (immich / jellyfin / gitea): each queries its
//     own service API (photos, media library counts, repositories...). These
//     are the services the portal can reach from the cloud host (public or
//     OAuth-gated); LAN-only services are skipped.
//
// The collector is independent of the health prober: a service can answer
// /api/metrics even when the probe marks it down, and vice versa.

type widgetItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type widgetData struct {
	Type      string       `json:"type"`
	Items     []widgetItem `json:"items"`
	UpdatedAt int64        `json:"updated_at"`
	Error     string       `json:"error,omitempty"`
}

// hostMetric is the per-instance result of the node exporter queries. Values
// are utilisation percentages (0-100), matching the homepage-dashboard widget.
type hostMetric struct {
	CPU       int    `json:"cpu"`
	Memory    int    `json:"memory"`
	Disk      int    `json:"disk"`
	UpdatedAt int64  `json:"updated_at"`
	Err       string `json:"error,omitempty"`
}

type widgetCollector struct {
	a *app

	mu       sync.RWMutex
	hosts    map[string]*hostMetric
	services map[string]*widgetData
}

func newWidgetCollector(a *app) *widgetCollector {
	return &widgetCollector{
		a:        a,
		hosts:    map[string]*hostMetric{},
		services: map[string]*widgetData{},
	}
}

func (w *widgetCollector) enabled() bool {
	return w.a.cfg.prometheusURL != ""
}

func widgetInterval() time.Duration {
	if v := os.Getenv("NAVDASH_WIDGET_INTERVAL"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 30 * time.Second
}

// metricHostName resolves which Prometheus instance a prometheusmetric card
// should query: explicit MetricHost wins, else the entry's Host (the vhost's
// source host, which equals the node exporter instance label).
func (e entry) metricHostName() string {
	if e.MetricHost != "" {
		return e.MetricHost
	}
	return e.Host
}

func (w *widgetCollector) loop(entries []entry) {
	tick := time.NewTicker(widgetInterval())
	defer tick.Stop()
	w.refresh(entries)
	for range tick.C {
		w.refresh(entries)
	}
}

func (w *widgetCollector) refresh(entries []entry) {
	hosts := map[string]bool{}
	var serviceCards []entry
	for _, e := range entries {
		switch e.Widget {
		case "prometheusmetric":
			hosts[e.metricHostName()] = true
		case "immich", "jellyfin", "gitea":
			serviceCards = append(serviceCards, e)
		}
	}
	hostList := make([]string, 0, len(hosts))
	for h := range hosts {
		hostList = append(hostList, h)
	}
	sort.Strings(hostList)

	nh := w.fetchHostMetrics(hostList)
	ns := w.fetchServiceData(serviceCards)

	w.mu.Lock()
	for h, m := range nh {
		w.hosts[h] = m
	}
	for u, d := range ns {
		w.services[u] = d
	}
	w.mu.Unlock()
}

func (w *widgetCollector) snapshotHosts() map[string]*hostMetric {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make(map[string]*hostMetric, len(w.hosts))
	for k, v := range w.hosts {
		out[k] = v
	}
	return out
}

func (w *widgetCollector) snapshotServices() map[string]*widgetData {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make(map[string]*widgetData, len(w.services))
	for k, v := range w.services {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------
// Prometheus host metrics

func (w *widgetCollector) fetchHostMetrics(hosts []string) map[string]*hostMetric {
	out := map[string]*hostMetric{}
	if len(hosts) == 0 {
		return out
	}
	base := w.a.cfg.prometheusURL
	queries := map[string]string{
		"cpu":    `100 * (1 - avg(rate(node_cpu_seconds_total{instance="%s",mode="idle"}[5m])))`,
		"memory": `100 * (1 - node_memory_MemAvailable_bytes{instance="%s"} / node_memory_MemTotal_bytes{instance="%s"})`,
		"disk":   `100 * (1 - sum(node_filesystem_avail_bytes{instance="%s",fstype!~"tmpfs|overlay|squashfs|devtmpfs|iso9660"}) / sum(node_filesystem_size_bytes{instance="%s",fstype!~"tmpfs|overlay|squashfs|devtmpfs|iso9660"}))`,
	}
	for _, h := range hosts {
		m := &hostMetric{UpdatedAt: time.Now().Unix()}
		vals := map[string]float64{}
		var firstErr error
		for name, q := range queries {
			// query strings with selector filters differ per metric, so build
			// each explicitly.
			sel := fmt.Sprintf(q, h, h)
			v, err := w.promQuery(base, sel)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			vals[name] = v
		}
		if firstErr != nil {
			m.Err = firstErr.Error()
		}
		if c, ok := vals["cpu"]; ok {
			m.CPU = int(c + 0.5)
		}
		if c, ok := vals["memory"]; ok {
			m.Memory = int(c + 0.5)
		}
		if c, ok := vals["disk"]; ok {
			m.Disk = int(c + 0.5)
		}
		out[h] = m
	}
	return out
}

// promQuery runs one Prometheus instant query and returns the first vector
// sample value.
func (w *widgetCollector) promQuery(endpoint, query string) (float64, error) {
	u := endpoint + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := w.a.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus status %d", resp.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, err
	}
	if body.Status != "success" || len(body.Data.Result) == 0 {
		return 0, fmt.Errorf("prometheus no result for %q", query)
	}
	if len(body.Data.Result[0].Value) < 2 {
		return 0, fmt.Errorf("prometheus malformed sample")
	}
	var s string
	if err := json.Unmarshal(body.Data.Result[0].Value[1], &s); err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Service-internal widgets

func (w *widgetCollector) fetchServiceData(cards []entry) map[string]*widgetData {
	out := map[string]*widgetData{}
	for _, e := range cards {
		d := &widgetData{Type: e.Widget, UpdatedAt: time.Now().Unix()}
		items, err := w.queryService(e)
		if err != nil {
			d.Error = err.Error()
		} else {
			d.Items = items
		}
		out[e.URL] = d
	}
	return out
}

// httpGet issues one GET request with the given headers and returns the body
// on a 200 response.
func (w *widgetCollector) httpGet(endpoint string, hdr map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := w.a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// fetchServiceEndpoint fetches path from baseURL, retrying on :8443 when the
// default port fails. On-premise zhyi services (immich/jellyfin) are only
// reachable from the cloud host via :8443 — the home WAN 443 is blocked by the
// ISP — so a failed fetch on 443 is retried on 8443, mirroring the health
// prober's alt8443URL fallback. Cloud-hosted services (gitea) answer on 443
// and never trigger the retry.
func (w *widgetCollector) fetchServiceEndpoint(baseURL, path string, hdr map[string]string) ([]byte, error) {
	raw, err := w.httpGet(baseURL+path, hdr)
	if err != nil {
		if alt := alt8443URL(baseURL); alt != "" {
			raw, err = w.httpGet(alt+path, hdr)
		}
	}
	return raw, err
}

func (w *widgetCollector) queryService(e entry) ([]widgetItem, error) {
	key := w.a.cfg.widgetKeys[e.Widget]

	switch e.Widget {
	case "immich":
		if key == "" {
			return nil, fmt.Errorf("immich api key not configured")
		}
		raw, err := w.fetchServiceEndpoint(e.URL, "/api/server/statistics", map[string]string{"x-api-key": key})
		if err != nil {
			return nil, err
		}
		var s struct {
			Photos int64   `json:"photos"`
			Videos int64   `json:"videos"`
			Usage  float64 `json:"usage"`
			Users  int     `json:"users"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		items := []widgetItem{
			{Label: "照片", Value: fmt.Sprintf("%d", s.Photos)},
			{Label: "视频", Value: fmt.Sprintf("%d", s.Videos)},
			{Label: "存储", Value: humanBytes(s.Usage)},
			{Label: "用户", Value: fmt.Sprintf("%d", s.Users)},
		}
		return items, nil

	case "jellyfin":
		if key == "" {
			return nil, fmt.Errorf("jellyfin api_key not configured")
		}
		raw, err := w.fetchServiceEndpoint(e.URL, "/Items/Counts", map[string]string{"X-Emby-Token": key})
		if err != nil {
			return nil, err
		}
		var c struct {
			MovieCount   int64 `json:"MovieCount"`
			SeriesCount  int64 `json:"SeriesCount"`
			EpisodeCount int64 `json:"EpisodeCount"`
			AlbumCount   int64 `json:"AlbumCount"`
			SongCount    int64 `json:"SongCount"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		items := []widgetItem{
			{Label: "电影", Value: fmt.Sprintf("%d", c.MovieCount)},
			{Label: "剧集", Value: fmt.Sprintf("%d", c.SeriesCount)},
			{Label: "单集", Value: fmt.Sprintf("%d", c.EpisodeCount)},
		}
		return items, nil

	case "gitea":
		if key == "" {
			return nil, fmt.Errorf("gitea api_key not configured")
		}
		repoCount, err := giteaTotal(w.a.client, e.URL, key, "/api/v1/user/repos")
		if err != nil {
			return nil, err
		}
		items := []widgetItem{
			{Label: "仓库", Value: fmt.Sprintf("%d", repoCount)},
		}
		return items, nil

	default:
		return nil, fmt.Errorf("unknown widget %q", e.Widget)
	}
}

// giteaTotal counts via the X-Total-Count response header (Gitea paginates
// list endpoints; the header carries the total without fetching every page).
func giteaTotal(client *http.Client, base, token, path string) (int64, error) {
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("gitea status %d", resp.StatusCode)
	}
	tot := resp.Header.Get("X-Total-Count")
	if tot == "" {
		return 0, fmt.Errorf("gitea missing X-Total-Count")
	}
	return strconv.ParseInt(tot, 10, 64)
}

func humanBytes(b float64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%.0fB", b)
	}
	div, exp := float64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", b/div, "KMGTPE"[exp])
}
