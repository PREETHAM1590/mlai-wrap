package main

// diag.go — per-request timings, the last fallback reason, build identity and
// GET /v1/status, which `mlai-wrap status` prints.

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

// packageVersion is the npm package / release version, set at build time with
// -ldflags "-X main.packageVersion=0.1.1". "dev" for a local build.
var packageVersion = "dev"

// buildInfo describes the running binary.
type buildInfo struct {
	Package  string `json:"package"`
	Bridge   string `json:"bridge"`
	Commit   string `json:"commit,omitempty"`
	Built    string `json:"built,omitempty"`
	Modified bool   `json:"modified,omitempty"`
	Go       string `json:"go"`
	Platform string `json:"platform"`
}

var currentBuild = sync.OnceValue(func() buildInfo {
	b := buildInfo{Package: packageVersion, Bridge: version, Go: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				b.Commit = s.Value
			case "vcs.time":
				b.Built = s.Value
			case "vcs.modified":
				b.Modified = s.Value == "true"
			}
		}
	}
	return b
})

// String is the one-line identity `version` and `status` print.
func (b buildInfo) String() string {
	var parts []string
	parts = append(parts, "mlai-bridge "+b.Bridge)
	if b.Commit != "" {
		c := b.Commit
		if len(c) > 7 {
			c = c[:7]
		}
		if b.Modified {
			c += " (modified)"
		}
		parts = append(parts, "commit "+c)
	}
	if b.Built != "" {
		parts = append(parts, "committed "+b.Built)
	}
	parts = append(parts, b.Go, b.Platform)
	return "mlai-wrap " + b.Package + " (" + strings.Join(parts, ", ") + ")"
}

// ---------- per-request trace ----------

// reqTrace records where one request spent its time. Durations are zero when
// that step did not happen.
type reqTrace struct {
	mu        sync.Mutex
	start     time.Time
	queue     time.Duration // waiting for an admission, CLI or direct slot
	keeper    time.Duration // waiting for a run token
	firstText time.Duration // start to the first streamed text sent to the client
	path      string        // direct, cli, cache
	fallback  string        // why the direct path was not used
	// directOnly: this request may not fall back to the CLI.
	directOnly bool
}

func (t *reqTrace) noteFallback(reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.fallback = reason
	t.mu.Unlock()
	diag.noteFallback(reason)
}

func (t *reqTrace) isDirectOnly() bool {
	if t == nil {
		return cfg.DirectOnly
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.directOnly
}

func (t *reqTrace) addQueue(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.queue += d
	t.mu.Unlock()
}

func (t *reqTrace) addKeeper(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.keeper += d
	t.mu.Unlock()
}

func (t *reqTrace) setPath(p string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.path == "" {
		t.path = p
	}
	t.mu.Unlock()
}

type traceKey struct{}

func traceFrom(ctx context.Context) *reqTrace {
	t, _ := ctx.Value(traceKey{}).(*reqTrace)
	return t
}

// traces maps a ResponseWriter to its request's trace, so the stream writers,
// which only see w, can mark the first text without a wrapped writer.
var traces sync.Map

// noteText marks the first streamed text of the request behind w.
func noteText(w http.ResponseWriter) {
	v, ok := traces.Load(w)
	if !ok {
		return
	}
	t := v.(*reqTrace)
	t.mu.Lock()
	if t.firstText == 0 {
		t.firstText = time.Since(t.start)
	}
	t.mu.Unlock()
}

// visibleContent matches an OpenAI stream chunk that carries text.
var visibleContent = regexp.MustCompile(`"content":"[^"]`)

// ---------- recent requests and the last fallback ----------

type traceRecord struct {
	At        time.Time
	Path      string
	Total     time.Duration
	Queue     time.Duration
	Keeper    time.Duration
	FirstText time.Duration
}

type diagnostics struct {
	mu         sync.Mutex
	recent     []traceRecord // ring of the last recentSize requests
	next       int
	fallbackAt time.Time
	fallback   string
	fallbacks  int64
}

const recentSize = 200

var diag = &diagnostics{}

func (d *diagnostics) noteFallback(reason string) {
	d.mu.Lock()
	d.fallbackAt, d.fallback = time.Now(), reason
	d.fallbacks++
	d.mu.Unlock()
}

func (d *diagnostics) record(t *reqTrace) {
	t.mu.Lock()
	rec := traceRecord{At: t.start, Path: t.path, Total: time.Since(t.start), Queue: t.queue, Keeper: t.keeper, FirstText: t.firstText}
	t.mu.Unlock()
	if rec.Path == "" {
		return // not a model request
	}
	d.mu.Lock()
	if len(d.recent) < recentSize {
		d.recent = append(d.recent, rec)
	} else {
		d.recent[d.next] = rec
	}
	d.next = (d.next + 1) % recentSize
	d.mu.Unlock()
}

// logSuffix is appended to the request log line.
func (t *reqTrace) logSuffix() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.path == "" {
		return ""
	}
	s := " path=" + t.path + " queue=" + t.queue.Round(time.Millisecond).String() + " keeper=" + t.keeper.Round(time.Millisecond).String()
	if t.firstText > 0 {
		s += " first_text=" + t.firstText.Round(time.Millisecond).String()
	}
	if t.fallback != "" {
		s += " fallback=" + strconvQuote(t.fallback)
	}
	return s
}

func strconvQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// ---------- GET /v1/status ----------

type percentiles struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50_s"`
	P95   float64 `json:"p95_s"`
}

func pct(ds []time.Duration) percentiles {
	if len(ds) == 0 {
		return percentiles{}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	at := func(q float64) float64 {
		i := int(q*float64(len(ds)-1) + 0.5)
		return ds[i].Seconds()
	}
	return percentiles{Count: len(ds), P50: at(0.50), P95: at(0.95)}
}

type statusDoc struct {
	Build      buildInfo    `json:"build"`
	UptimeS    int          `json:"uptime_s"`
	Direct     bool         `json:"direct"`
	DirectOnly bool         `json:"direct_only"`
	Prewarm    bool         `json:"prewarm"`
	KeeperWait float64      `json:"keeper_wait_s"`
	Keeper     keeperStatus `json:"keeper"`
	Fallback   *fallbackDoc `json:"last_fallback,omitempty"`
	Fallbacks  int64        `json:"fallbacks_total"`
	Recent     recentDoc    `json:"recent"`
	Active     int64        `json:"active_requests"`
	Requests   int64        `json:"requests_total"`
	Failed     int64        `json:"requests_failed_total"`
}

type fallbackDoc struct {
	At     time.Time `json:"at"`
	AgoS   int       `json:"ago_s"`
	Reason string    `json:"reason"`
}

type recentDoc struct {
	Count     int            `json:"count"`
	ByPath    map[string]int `json:"by_path"`
	Queue     percentiles    `json:"queue_wait"`
	Keeper    percentiles    `json:"keeper_wait"`
	FirstText percentiles    `json:"first_text"`
	Total     percentiles    `json:"total"`
}

func (s *server) statusSnapshot() statusDoc {
	doc := statusDoc{
		Build:      currentBuild(),
		UptimeS:    int(time.Since(startedAt).Seconds()),
		Direct:     s.direct,
		DirectOnly: cfg.DirectOnly,
		Prewarm:    cfg.Prewarm,
		KeeperWait: cfg.KeeperWait.Seconds(),
		Active:     activeRequests.Load(),
		Requests:   requestsTotal.Load(),
		Failed:     requestsFailed.Load(),
	}
	if s.keeper != nil {
		doc.Keeper = s.keeper.State()
	}
	diag.mu.Lock()
	recent := append([]traceRecord(nil), diag.recent...)
	if diag.fallback != "" {
		doc.Fallback = &fallbackDoc{At: diag.fallbackAt.UTC(), AgoS: int(time.Since(diag.fallbackAt).Seconds()), Reason: diag.fallback}
	}
	doc.Fallbacks = diag.fallbacks
	diag.mu.Unlock()

	doc.Recent = recentDoc{Count: len(recent), ByPath: map[string]int{}}
	var q, k, f, tot []time.Duration
	for _, r := range recent {
		doc.Recent.ByPath[r.Path]++
		q, tot = append(q, r.Queue), append(tot, r.Total)
		if r.Path == "direct" {
			k = append(k, r.Keeper)
		}
		if r.FirstText > 0 {
			f = append(f, r.FirstText)
		}
	}
	doc.Recent.Queue, doc.Recent.Keeper, doc.Recent.FirstText, doc.Recent.Total = pct(q), pct(k), pct(f), pct(tot)
	return doc
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(s.statusSnapshot())
}

// ---------- direct-only ----------

// directOnly reports whether this request must not fall back to the CLI: the
// bridge runs with -direct-only, or the client sent X-MLAI-Direct-Only: true.
func directOnly(r *http.Request) bool {
	if cfg.DirectOnly {
		return true
	}
	v := strings.TrimSpace(r.Header.Get("X-MLAI-Direct-Only"))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

// directOnlyError is the answer when the direct path failed and the request
// may not fall back: 503, retryable, with the reason.
func (s *server) directOnlyError(t *reqTrace) *apiError {
	e := errUnavailable
	reason := "direct mode is off on this bridge"
	if s.direct {
		reason = "the direct path is unavailable"
		if t != nil {
			t.mu.Lock()
			if t.fallback != "" {
				reason += ": " + t.fallback
			}
			t.mu.Unlock()
		}
	}
	e.Message = reason + " (direct-only: no CLI fallback) — retry shortly"
	return &e
}
