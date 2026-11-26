package main

// Tests for request diagnostics (/v1/status, status), direct-only mode,
// keeper prewarm and the per-request keeper wait, and the build identity.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func resetDiag(t *testing.T) {
	t.Helper()
	old := diag
	diag = &diagnostics{}
	t.Cleanup(func() { diag = old })
}

func getStatus(t *testing.T, h http.Handler) statusDoc {
	t.Helper()
	rec := serve(h, "GET", "/v1/status", "", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("/v1/status = %d %s", rec.Code, rec.Body)
	}
	var st statusDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), "mlrun_") {
		t.Error("/v1/status must never contain a run token")
	}
	return st
}

func TestStatusReportsPathsFallbackAndKeeper(t *testing.T) {
	setupStub(t)
	resetDiag(t)
	_, h := directServer(t)
	fail := true
	setTransport(t, rtFunc(func(r *http.Request) (*http.Response, error) {
		if fail {
			return fakeResp(r, 500, "boom"), nil
		}
		return fakeResp(r, 200, gatewayOK), nil
	}))
	if rec := serve(h, "POST", "/v1/chat/completions", "", jsonHeader, strings.NewReader(chatBody)); rec.Code != 200 || !strings.Contains(rec.Body.String(), "stub:hello") {
		t.Fatalf("fallback request: %d %s", rec.Code, rec.Body)
	}
	fail = false
	if rec := serve(h, "POST", "/v1/chat/completions", "", jsonHeader, strings.NewReader(chatBody)); rec.Code != 200 || !strings.Contains(rec.Body.String(), "direct says hi") {
		t.Fatalf("direct request: %d %s", rec.Code, rec.Body)
	}

	st := getStatus(t, h)
	if st.Recent.Count != 2 || st.Recent.ByPath["cli"] != 1 || st.Recent.ByPath["direct"] != 1 {
		t.Errorf("recent = %+v, want one cli and one direct", st.Recent)
	}
	if st.Fallback == nil || !strings.Contains(st.Fallback.Reason, "HTTP 500") || st.Fallbacks != 1 {
		t.Errorf("last fallback = %+v (%d), want the HTTP 500 reason once", st.Fallback, st.Fallbacks)
	}
	if !st.Direct || st.DirectOnly || !st.Keeper.TokenReady {
		t.Errorf("direct=%v direct_only=%v keeper=%+v", st.Direct, st.DirectOnly, st.Keeper)
	}
	if st.Build.Bridge != version || st.Build.Package != packageVersion || st.Build.Platform == "" {
		t.Errorf("build = %+v", st.Build)
	}
	if st.Recent.Total.Count != 2 || st.Recent.Total.P95 < st.Recent.Total.P50 {
		t.Errorf("total percentiles = %+v", st.Recent.Total)
	}
}

func TestStatusNeedsTheKey(t *testing.T) {
	setupStub(t)
	s := newTestServer(t)
	h := testChain(s, "the-key")
	if rec := serve(h, "GET", "/v1/status", "", nil, nil); rec.Code != 401 {
		t.Errorf("without the key: %d, want 401", rec.Code)
	}
	if rec := serve(h, "GET", "/v1/status", "", map[string]string{"Authorization": "Bearer the-key"}, nil); rec.Code != 200 {
		t.Errorf("with the key: %d, want 200", rec.Code)
	}
}

func TestFirstTextIsRecordedForStreams(t *testing.T) {
	setupStub(t)
	resetDiag(t)
	s := newTestServer(t) // CLI path
	h := testChain(s, "")
	body := strings.Replace(chatBody, `"messages"`, `"stream":true,"messages"`, 1)
	rec := serve(h, "POST", "/v1/chat/completions", "", jsonHeader, strings.NewReader(body))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "stub:hello") {
		t.Fatalf("stream: %d %s", rec.Code, rec.Body)
	}
	st := getStatus(t, h)
	if st.Recent.FirstText.Count != 1 || st.Recent.FirstText.P50 <= 0 || st.Recent.FirstText.P50 > st.Recent.Total.P50 {
		t.Errorf("first text = %+v, total = %+v", st.Recent.FirstText, st.Recent.Total)
	}
}

func TestDirectOnlyNeverRunsTheCLI(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flag   bool
		header map[string]string
	}{
		{"header", false, map[string]string{"Content-Type": "application/json", "X-MLAI-Direct-Only": "true"}},
		{"flag", true, jsonHeader},
	} {
		for path, body := range map[string]string{"/v1/chat/completions": chatBody, "/v1/messages": anthropicBody} {
			t.Run(tc.name+path, func(t *testing.T) {
				env := setupStub(t)
				resetDiag(t)
				cfg.DirectOnly = tc.flag
				_, h := directServer(t)
				setTransport(t, rtFunc(func(r *http.Request) (*http.Response, error) {
					return fakeResp(r, 500, "boom"), nil
				}))
				rec := serve(h, "POST", path, "", tc.header, strings.NewReader(body))
				if rec.Code != 503 || !strings.Contains(rec.Body.String(), "direct-only") || !strings.Contains(rec.Body.String(), "HTTP 500") {
					t.Fatalf("got %d %s, want 503 with the reason", rec.Code, rec.Body)
				}
				if n := env.count(t, "-p"); n != 0 {
					t.Errorf("the CLI ran %d times", n)
				}
				if st := diag; len(st.recent) != 1 || st.recent[0].Path != "refused" {
					t.Errorf("recent = %+v, want one refused request", st.recent)
				}
			})
		}
	}
}

func TestDirectOnlyWithoutTheHeaderStillFallsBack(t *testing.T) {
	setupStub(t)
	_, h := directServer(t)
	setTransport(t, rtFunc(func(r *http.Request) (*http.Response, error) { return fakeResp(r, 500, "boom"), nil }))
	hdr := map[string]string{"Content-Type": "application/json", "X-MLAI-Direct-Only": "no"}
	if rec := serve(h, "POST", "/v1/chat/completions", "", hdr, strings.NewReader(chatBody)); rec.Code != 200 {
		t.Errorf("got %d, want the CLI answer", rec.Code)
	}
}

func TestDirectOnlyNeedsDirectMode(t *testing.T) {
	if err := validateConfig(config{Bind: "127.0.0.1", DirectOnly: true, Direct: false}); err == nil {
		t.Error("direct-only without direct mode must be refused")
	}
	if err := validateConfig(config{Bind: "127.0.0.1", DirectOnly: true, Direct: true}); err != nil {
		t.Errorf("direct-only with direct mode: %v", err)
	}
}

func TestKeeperWaitBoundsTheTokenWait(t *testing.T) {
	setupStub(t)
	t.Setenv("STUB_BG", "slow") // the keeper needs 30 s for its token
	cfg.KeeperWait = 300 * time.Millisecond
	s := newTestServer(t)
	s.direct = true
	h := testChain(s, "")
	t0 := time.Now()
	rec := serve(h, "POST", "/v1/chat/completions", "", jsonHeader, strings.NewReader(chatBody))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "stub:hello") {
		t.Fatalf("got %d %s, want the CLI answer", rec.Code, rec.Body)
	}
	if d := time.Since(t0); d > 10*time.Second {
		t.Errorf("the request waited %s for the keeper", d)
	}
	if st := s.keeper.State(); !st.Starting {
		t.Errorf("keeper = %+v, want it still starting for the next request", st)
	}
	s.keeper.Close()
}

func TestPrewarmStartsTheKeeper(t *testing.T) {
	env := setupStub(t)
	cfg.Prewarm = true
	s := newTestServer(t)
	s.direct = true
	s.startPrewarm()
	deadline := time.Now().Add(15 * time.Second)
	for !s.keeper.State().TokenReady && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if st := s.keeper.State(); !st.TokenReady || st.LastStartS <= 0 {
		t.Fatalf("keeper after prewarm = %+v", st)
	}
	if n := env.count(t, "-y"); n != 1 {
		t.Errorf("keeper ran %d times, want 1", n)
	}
	if tok, err := s.keeper.Token(context.Background()); err != nil || !strings.HasPrefix(tok, "mlrun_") {
		t.Errorf("first request token = %q, %v", tok, err)
	}
	if n := env.count(t, "-y"); n != 1 {
		t.Errorf("the first request started another keeper (%d)", n)
	}
}

func TestPrewarmIsOffByDefault(t *testing.T) {
	env := setupStub(t)
	s := newTestServer(t)
	s.direct = true
	s.startPrewarm()
	time.Sleep(300 * time.Millisecond)
	if n := env.count(t, "-y"); n != 0 || s.keeper.State().Starting {
		t.Errorf("keeper started without -prewarm (%d runs)", n)
	}
}

func TestStatusCommandPrintsDetails(t *testing.T) {
	cliSandbox(t)
	st := statusDoc{Build: currentBuild(), Direct: true, DirectOnly: true, KeeperWait: 30,
		Keeper:    keeperStatus{TokenReady: true, TokenAgeS: 12, LastStartS: 21.3},
		Fallback:  &fallbackDoc{AgoS: 120, Reason: "gateway returned HTTP 500"},
		Fallbacks: 3,
		Recent: recentDoc{Count: 2, ByPath: map[string]int{"direct": 1, "cli": 1},
			Keeper: percentiles{Count: 1, P50: 0.1, P95: 0.1}, Total: percentiles{Count: 2, P50: 1.5, P95: 4}}}
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.Write([]byte(`{"status":"ok","version":"1.0.0","uptime_s":5,"signed_in":true}`))
		case "/v1/status":
			json.NewEncoder(w).Encode(st)
		}
	}))
	defer bridge.Close()
	var out, errb bytes.Buffer
	if code := runCLI([]string{"status", "--url", bridge.URL}, &out, &errb); code != 0 {
		t.Fatalf("status: %d %s", code, errb.String())
	}
	for _, want := range []string{"build:      mlai-wrap " + packageVersion, "direct-only (no CLI fallback)", "token ready (age 12s); last start took 21.3s",
		"last 2m0s ago: gateway returned HTTP 500 (3 since start)", "last 2: cli 1, direct 1", "keeper wait  p50 100ms", "total        p50 1.5s    p95 4s"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status output lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code := runCLI([]string{"status", "--url", bridge.URL, "--json"}, &out, &errb); code != 0 || !strings.Contains(out.String(), `"direct_only": true`) {
		t.Errorf("--json: %d %s", code, out.String())
	}
}

func TestStatusCommandWithAnOlderBridge(t *testing.T) {
	cliSandbox(t)
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Write([]byte(`{"status":"ok","version":"1.0.0","uptime_s":5,"signed_in":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer bridge.Close()
	var out, errb bytes.Buffer
	if code := runCLI([]string{"status", "--url", bridge.URL}, &out, &errb); code != 0 || !strings.Contains(out.String(), "details:    not available") {
		t.Errorf("status: %d %q", code, out.String())
	}
}

func TestBuildIdentity(t *testing.T) {
	b := buildInfo{Package: "0.1.1", Bridge: "1.0.0", Commit: "8fa0f29abcdef", Built: "2026-11-26T14:00:00Z", Modified: true, Go: "go1.26.11", Platform: "windows/amd64"}
	want := "mlai-wrap 0.1.1 (mlai-bridge 1.0.0, commit 8fa0f29 (modified), committed 2026-11-26T14:00:00Z, go1.26.11, windows/amd64)"
	if got := b.String(); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
