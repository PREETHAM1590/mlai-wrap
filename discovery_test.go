package main

// discovery_test.go: the discovery file `serve` writes, the /healthz proof,
// the plain-HTTP opt-in for non-loopback binds, and the in-flight limit.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// testProof is the proof /healthz must give, computed independently of the
// production code: hex(HMAC-SHA256(key = secret string, message = challenge string)).
// testSecret is a fresh random discovery secret. Tests never hard-code one:
// secret scanners flag any 64-digit hex literal.
func testSecret(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

func testProof(secret, challenge string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(challenge))
	return hex.EncodeToString(m.Sum(nil))
}

// useDiscoveryFile points discoveryFile at path for the test.
func useDiscoveryFile(t *testing.T, path string) {
	t.Helper()
	old := discoveryFile
	discoveryFile = path
	t.Cleanup(func() { discoveryFile = old })
}

// writeTestDiscovery writes a discovery file for a bridge at rawURL, as `serve` would.
func writeTestDiscovery(t *testing.T, rawURL, secret string) {
	t.Helper()
	raw, err := json.Marshal(discoveryDoc{URL: rawURL, PID: os.Getpid(), Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(discoveryFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(discoveryFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestDiscovery(t *testing.T, path string) discoveryDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc discoveryDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("discovery file is not JSON: %v", err)
	}
	return doc
}

// ---------- the discovery file ----------

func TestServeWritesAndRemovesDiscoveryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mlai-bridge", "serve.json")
	useDiscoveryFile(t, path)

	secret, err := publishDiscovery("127.0.0.1", 8197)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(secret) {
		t.Errorf("secret %q is not 64 hex characters", secret)
	}
	doc := readTestDiscovery(t, path)
	if doc.URL != "http://127.0.0.1:8197" || doc.PID != os.Getpid() || doc.Secret != secret {
		t.Errorf("discovery file = %+v, want url http://127.0.0.1:8197, pid %d, our secret", doc, os.Getpid())
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("file mode: %v, %v; want 0600", st, err)
		}
		if st, err := os.Stat(filepath.Dir(path)); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("directory mode: %v, %v; want 0700", st, err)
		}
	}
	if entries, err := os.ReadDir(filepath.Dir(path)); err != nil || len(entries) != 1 {
		t.Errorf("directory holds %v (%v), want only serve.json", entries, err)
	}

	// A newer bridge replaces the file; the older one must not delete it.
	secret2, err := publishDiscovery("127.0.0.1", 8198)
	if err != nil || secret2 == secret {
		t.Fatalf("second publish: %q, %v", secret2, err)
	}
	removeDiscovery(secret)
	if got := readTestDiscovery(t, path); got.Secret != secret2 || got.URL != "http://127.0.0.1:8198" {
		t.Errorf("a stale bridge changed the file: %+v", got)
	}
	removeDiscovery(secret2)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still there after removeDiscovery: %v", err)
	}
	removeDiscovery(secret2) // already gone: no panic, no error
}

func TestDiscoveryURLBracketsIPv6(t *testing.T) {
	useDiscoveryFile(t, filepath.Join(t.TempDir(), "serve.json"))
	secret, err := publishDiscovery("::1", 9000)
	if err != nil {
		t.Fatal(err)
	}
	defer removeDiscovery(secret)
	if got := readTestDiscovery(t, discoveryFile).URL; got != "http://[::1]:9000" {
		t.Errorf("url = %q, want http://[::1]:9000", got)
	}
}

func TestDiscoveryPathDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	useDiscoveryFile(t, "")
	if got, want := discoveryPath(), filepath.Join(home, "mlai-bridge", "serve.json"); got != want {
		t.Errorf("discoveryPath() = %q, want %q", got, want)
	}
}

func TestPrivateBridgeWritesNoDiscoveryFile(t *testing.T) {
	cliSandbox(t)
	initFlags(nil)
	_, _, stop, err := startWrapServer()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err := os.Stat(discoveryPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the launcher's private bridge wrote a discovery file: %v", err)
	}
}

// ---------- /healthz proof ----------

func healthDocFor(t *testing.T, s *server, challenge string) map[string]any {
	t.Helper()
	target := "/healthz"
	if challenge != "-" {
		target += "?challenge=" + url.QueryEscape(challenge)
	}
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge %q: status %d", challenge, rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	for _, f := range []string{"status", "version", "uptime_s", "signed_in"} {
		if _, ok := doc[f]; !ok {
			t.Errorf("challenge %q: field %q is missing: %v", challenge, f, doc)
		}
	}
	return doc
}

// cachedHealthServer answers /healthz from its cache, so no process starts.
func cachedHealthServer(secret string) *server {
	return &server{healthAt: time.Now(), healthOK: true, discoverySecret: secret}
}

func TestHealthzProofMatchesHMAC(t *testing.T) {
	secret := testSecret(t)
	s := cachedHealthServer(secret)
	for _, challenge := range []string{
		strings.Repeat("a", 16),
		strings.Repeat("0123456789abcdef", 4),
		strings.Repeat("F", 128),
	} {
		doc := healthDocFor(t, s, challenge)
		if doc["proof"] != testProof(secret, challenge) {
			t.Errorf("challenge %q: proof = %v, want %s", challenge, doc["proof"], testProof(secret, challenge))
		}
	}
	// A proof for one challenge is no proof for another.
	a := healthDocFor(t, s, strings.Repeat("a", 32))["proof"]
	b := healthDocFor(t, s, strings.Repeat("b", 32))["proof"]
	if a == b {
		t.Error("two challenges got the same proof")
	}
}

func TestHealthzWrongOrMissingChallengeGivesNoProof(t *testing.T) {
	s := cachedHealthServer(testSecret(t))
	for name, challenge := range map[string]string{
		"missing":     "-",
		"empty":       "",
		"too short":   strings.Repeat("a", 15),
		"too long":    strings.Repeat("a", 129),
		"not hex":     strings.Repeat("g", 32),
		"with spaces": strings.Repeat("ab ", 10),
		"with symbol": strings.Repeat("a", 20) + "&x=1",
	} {
		if doc := healthDocFor(t, s, challenge); doc["proof"] != nil {
			t.Errorf("%s: got a proof for challenge %q", name, challenge)
		}
	}
}

func TestHealthzWithoutDiscoveryFileGivesNoProof(t *testing.T) {
	s := cachedHealthServer("")
	if doc := healthDocFor(t, s, strings.Repeat("a", 32)); doc["proof"] != nil {
		t.Errorf("a bridge that wrote no discovery file gave a proof: %v", doc["proof"])
	}
}

// ---------- plain HTTP on non-loopback binds ----------

func TestValidateConfigPlainHTTPOptIn(t *testing.T) {
	for _, bind := range []string{"0.0.0.0", "::", "192.168.1.5"} {
		err := validateConfig(config{Bind: bind, APIKey: "k"})
		if err == nil {
			t.Errorf("bind %q with a key but without the opt-in must be refused", bind)
		} else if !strings.Contains(err.Error(), "MLAI_ALLOW_PLAIN_HTTP") || !strings.Contains(err.Error(), "TLS") {
			t.Errorf("bind %q: the error should name the TLS proxy and the opt-in: %v", bind, err)
		}
		if err := validateConfig(config{Bind: bind, APIKey: "k", AllowPlainHTTP: true}); err != nil {
			t.Errorf("bind %q with key and opt-in: %v", bind, err)
		}
		// The opt-in does not replace the key.
		if err := validateConfig(config{Bind: bind, AllowPlainHTTP: true}); err == nil {
			t.Errorf("bind %q with the opt-in but without a key must be refused", bind)
		}
	}
	for _, bind := range []string{"127.0.0.1", "localhost", "::1"} {
		for _, c := range []config{{Bind: bind}, {Bind: bind, APIKey: "k"}, {Bind: bind, AllowPlainHTTP: true}} {
			if err := validateConfig(c); err != nil {
				t.Errorf("loopback %+v: %v", c, err)
			}
		}
	}
}

func TestPlainHTTPFlagAndEnv(t *testing.T) {
	saveGlobals(t)
	t.Setenv("MLAI_ALLOW_PLAIN_HTTP", "")
	initFlags(nil)
	if cfg.AllowPlainHTTP {
		t.Error("plain HTTP must be off by default")
	}
	t.Setenv("MLAI_ALLOW_PLAIN_HTTP", "true")
	initFlags(nil)
	if !cfg.AllowPlainHTTP {
		t.Error("MLAI_ALLOW_PLAIN_HTTP=true was not applied")
	}
	t.Setenv("MLAI_ALLOW_PLAIN_HTTP", "")
	initFlags([]string{"-allow-plain-http"})
	if !cfg.AllowPlainHTTP {
		t.Error("-allow-plain-http was not applied")
	}
}

// ---------- admission ----------

func TestInflightLimitReturns429ForPOSTOnly(t *testing.T) {
	saveGlobals(t)
	oldMax := maxInflight
	maxInflight = 1
	t.Cleanup(func() { maxInflight = oldMax })
	cfg.QueueWait = 50 * time.Millisecond
	cfg.Timeout = time.Minute

	block := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(block) }) }
	t.Cleanup(release)
	entered := make(chan struct{}, 4)
	h := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/x" {
			entered <- struct{}{}
			<-block
		}
		w.WriteHeader(http.StatusOK)
	}))
	do := func(method, target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader("{}")))
		return rec
	}

	first := make(chan int, 1)
	go func() { first <- do(http.MethodPost, "/v1/x").Code }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first request never reached the handler")
	}

	start := time.Now()
	rec := do(http.MethodPost, "/v1/x")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("second POST = %d, want 429 (%s)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("the busy answer lacks X-Request-Id")
	}
	if d := time.Since(start); d < 40*time.Millisecond || d > 5*time.Second {
		t.Errorf("the second POST waited %s, want about QueueWait (50ms)", d)
	}
	if len(entered) != 0 {
		t.Error("the rejected request reached the handler")
	}
	for _, target := range []string{"/healthz", "/v1/models"} {
		if rec := do(http.MethodGet, target); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d while the slot is taken, want 200", target, rec.Code)
		}
	}

	release()
	select {
	case code := <-first:
		if code != http.StatusOK {
			t.Errorf("first POST = %d, want 200", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the first request did not finish")
	}
	if rec := do(http.MethodPost, "/v1/x"); rec.Code != http.StatusOK {
		t.Errorf("POST after the slot was released = %d, want 200", rec.Code)
	}
}
