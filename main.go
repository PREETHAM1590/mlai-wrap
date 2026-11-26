package main

// main.go — server wiring, config, health, metrics.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const version = "1.0.0"

type config struct {
	Port              int
	MLAI              string
	WorkDir           string
	DefaultModel      string
	Concurrency       int
	DirectConcurrency int
	QueueWait         time.Duration
	Timeout           time.Duration
	CacheTTL          time.Duration
	CacheSize         int
	Effort            string
	Direct            bool
	Bind              string
	APIKey            string
	IsolateSkills     bool
	AllowPlainHTTP    bool
}

var (
	cfg    = config{}
	logger = log.New(os.Stdout, "", log.LstdFlags|log.LUTC)

	requestsTotal  atomic.Int64
	requestsFailed atomic.Int64
	activeRequests atomic.Int64
	startedAt      = time.Now()
)

// initFlags fills cfg from args (without the program name) and the environment.
func initFlags(args []string) {
	fs := flag.NewFlagSet("mlai-bridge", flag.ExitOnError)
	fs.IntVar(&cfg.Port, "port", envInt("MLAI_PORT", 8197), "listen port (127.0.0.1)")
	fs.StringVar(&cfg.MLAI, "mlai", envStr("MLAI_BIN", "mlai"), "path to the mlai CLI")
	fs.StringVar(&cfg.WorkDir, "workdir", envStr("MLAI_WORKDIR", defaultWorkDir()), "folder the agent runs in")
	fs.StringVar(&cfg.DefaultModel, "model", envStr("MLAI_MODEL", "pixis-opus"), "default model id")
	fs.IntVar(&cfg.Concurrency, "concurrency", envInt("MLAI_CONCURRENCY", 1), "max parallel CLI runs")
	fs.IntVar(&cfg.DirectConcurrency, "direct-concurrency", envInt("MLAI_DIRECT_CONCURRENCY", 8), "max parallel gateway requests")
	fs.DurationVar(&cfg.QueueWait, "queue-wait", 30*time.Second, "how long a request waits for a free slot")
	fs.DurationVar(&cfg.Timeout, "timeout", 10*time.Minute, "per-request wall clock limit")
	fs.DurationVar(&cfg.CacheTTL, "cache-ttl", 10*time.Minute, "identical-request cache TTL (0 disables)")
	fs.IntVar(&cfg.CacheSize, "cache-size", 512, "cache entries")
	fs.StringVar(&cfg.Effort, "effort", envStr("MLAI_EFFORT", ""), "reasoning effort: low|medium|high|max")
	fs.BoolVar(&cfg.Direct, "direct", envBool("MLAI_DIRECT", true), "talk to the gateway directly (real token streaming) with CLI fallback")
	fs.BoolVar(&cfg.IsolateSkills, "isolate-skills", envBool("MLAI_ISOLATE_SKILLS", true), "run mlai without your Claude Code skills and agents (MLAI_HOME=~/mlai-bridge/home/.mlai)")
	fs.StringVar(&cfg.Bind, "bind", envStr("MLAI_BIND", "127.0.0.1"), "listen address (0.0.0.0 to expose; requires an API key)")
	fs.BoolVar(&cfg.AllowPlainHTTP, "allow-plain-http", envBool("MLAI_ALLOW_PLAIN_HTTP", false), "allow a non-loopback bind, which serves the API key and request bodies over plain HTTP (trusted networks only)")
	// The env default is applied after Parse so `-h` never prints the key.
	fs.StringVar(&cfg.APIKey, "api-key", "", "require this bearer key on /v1/* (env MLAI_API_KEY; empty = open, loopback only)")
	fs.Parse(args)
	cfg.Bind = strings.Trim(cfg.Bind, "[]")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(os.Getenv("MLAI_API_KEY"))
	}
}

// validateConfig refuses to listen beyond loopback without an API key, and
// without the explicit opt-in to plain HTTP (the bridge speaks no TLS).
func validateConfig(c config) error {
	if isLoopbackHost(c.Bind) {
		return nil
	}
	if c.APIKey == "" {
		return fmt.Errorf("bind address %q is not loopback: set MLAI_API_KEY (or -api-key) to expose the bridge", c.Bind)
	}
	if !c.AllowPlainHTTP {
		return fmt.Errorf("bind address %q is not loopback and the bridge serves plain HTTP, so the API key and request bodies would travel in clear: keep the bridge on 127.0.0.1 behind a TLS reverse proxy, or set MLAI_ALLOW_PLAIN_HTTP=true (-allow-plain-http) on a trusted network", c.Bind)
	}
	return nil
}

func defaultWorkDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, "mlai-bridge", "workspace")
	}
	return "workspace"
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
	}
	return def
}

func envStr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return d
}

// childEnv is the environment for every child process: the bridge's own key
// stays out of it.
func childEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(name, "MLAI_API_KEY") || (mlaiHome != "" && strings.EqualFold(name, "MLAI_HOME")) {
			continue
		}
		out = append(out, kv)
	}
	if mlaiHome != "" {
		out = append(out, "MLAI_HOME="+mlaiHome)
	}
	return append(out, "NO_COLOR=1", "CI=1")
}

type server struct {
	runner    *Runner
	cache     *LRU
	sem       chan struct{}
	directSem chan struct{}
	keeper    *runKeeper
	direct    bool

	// cancelBase ends the base context of the http.Server built by newHTTPServer.
	cancelBase context.CancelFunc

	healthMu sync.Mutex
	healthAt time.Time
	healthOK bool

	// discoverySecret is set by serveMain when it wrote the discovery file; it
	// lets /healthz prove which process owns the file. Empty: no proof.
	discoverySecret string
}

func newServer() *server {
	os.MkdirAll(cfg.WorkDir, 0o700)
	initIsolation()
	var c *LRU
	if cfg.CacheTTL > 0 {
		c = NewLRU(cfg.CacheSize, cfg.CacheTTL)
	}
	return &server{
		runner:    &Runner{Bin: cfg.MLAI, WorkDir: cfg.WorkDir, Timeout: int(cfg.Timeout.Seconds())},
		cache:     c,
		sem:       make(chan struct{}, maxInt(1, cfg.Concurrency)),
		directSem: make(chan struct{}, maxInt(1, cfg.DirectConcurrency)),
		keeper:    newRunKeeper(cfg.MLAI, cfg.WorkDir),
		direct:    cfg.Direct,
	}
}

func (s *server) acquire(ctx context.Context, sem chan struct{}) (func(), *apiError) {
	wait := time.NewTimer(cfg.QueueWait)
	defer wait.Stop()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-wait.C:
		e := errBusy
		return nil, &e
	case <-ctx.Done():
		e := errBusy
		e.Message = "client closed the request"
		return nil, &e
	}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// The lock is held during the exec so concurrent calls start at most one process.
	s.healthMu.Lock()
	if s.healthAt.IsZero() || time.Since(s.healthAt) >= 60*time.Second {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, cfg.MLAI, "whoami")
		cmd.Env = childEnv()
		cmd.WaitDelay = 5 * time.Second
		hideConsole(cmd)
		out, _ := cmd.CombinedOutput()
		cancel()
		s.healthOK = strings.HasPrefix(strings.TrimSpace(string(out)), "Signed in")
		s.healthAt = time.Now()
	}
	signedIn := s.healthOK
	s.healthMu.Unlock()

	status := "ok"
	if !signedIn {
		status = "degraded"
	}
	doc := map[string]any{
		"status":    status,
		"version":   version,
		"uptime_s":  int(time.Since(startedAt).Seconds()),
		"signed_in": signedIn,
	}
	if c := r.URL.Query().Get("challenge"); s.discoverySecret != "" && challengePattern.MatchString(c) {
		doc["proof"] = hex.EncodeToString(discoveryMAC(s.discoverySecret, c))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(doc)
}

func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	hits, miss := int64(0), int64(0)
	if s.cache != nil {
		hits, miss = s.cache.Stats()
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# TYPE mlai_requests_total counter\nmlai_requests_total %d\n", requestsTotal.Load())
	fmt.Fprintf(w, "# TYPE mlai_requests_failed_total counter\nmlai_requests_failed_total %d\n", requestsFailed.Load())
	fmt.Fprintf(w, "# TYPE mlai_cache_hits_total counter\nmlai_cache_hits_total %d\nmlai_cache_misses_total %d\n", hits, miss)
	fmt.Fprintf(w, "# TYPE mlai_active_requests gauge\nmlai_active_requests %d\n", activeRequests.Load())
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// maxInflight caps the POST /v1/* requests the bridge works on at once. The
// others wait up to cfg.QueueWait for a slot before their body is read.
var maxInflight = 32

func withMiddleware(next http.Handler) http.Handler {
	slots := make(chan struct{}, maxInt(1, maxInflight))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !requestIDPattern.MatchString(id) {
			id = fmt.Sprintf("req_%d", time.Now().UnixNano())
		}
		w.Header().Set("X-Request-Id", id)
		// Overall bound on writing the response; streaming code renews it (stream.go).
		http.NewResponseController(w).SetWriteDeadline(time.Now().Add(cfg.Timeout + time.Minute))
		t0 := time.Now()
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/") {
			if !takeSlot(r.Context(), slots) {
				writeJSONAuthError(w, r, errBusy)
				return
			}
			defer func() { <-slots }()
			// Time spent queued does not count against sending the body.
			http.NewResponseController(w).SetReadDeadline(time.Now().Add(bodyReadTimeout))
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
		next.ServeHTTP(w, r)
		logger.Printf("%s %q %q %s", id, r.Method, r.URL.Path, time.Since(t0).Round(time.Millisecond))
	})
}

// takeSlot takes a free slot at once, or waits up to cfg.QueueWait for one.
// A free slot always wins: with a zero wait, select would pick at random.
func takeSlot(ctx context.Context, slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
	}
	wait := time.NewTimer(cfg.QueueWait)
	defer wait.Stop()
	select {
	case slots <- struct{}{}:
		return true
	case <-wait.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// bodyReadTimeout bounds how long a client may take to send a request body.
// http.Server.ReadTimeout would also cancel long streaming responses, so the
// deadline is set per request and cleared by decodeBody once the body is in.
var bodyReadTimeout = 30 * time.Second

func withBodyDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NewResponseController(w).SetReadDeadline(time.Now().Add(bodyReadTimeout))
		next.ServeHTTP(w, r)
	})
}

// decodeBody reads the whole request body, clears the read deadline, then decodes JSON.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return errors.New("request body too large")
		}
		return errors.New("could not read the request body")
	}
	// Only on success: after a failed read the server still drains the body.
	http.NewResponseController(w).SetReadDeadline(time.Time{})
	return json.Unmarshal(raw, v)
}

// newHTTPServer builds the mux and middleware chain from cfg. The caller
// starts it and stops it with s.shutdown.
func newHTTPServer(s *server) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/chat/completions", s.handleOpenAIChat)
	mux.HandleFunc("/v1/responses", s.handleResponses)
	mux.HandleFunc("/v1/messages", s.handleAnthropicMessages)
	mux.HandleFunc("/v1/messages/count_tokens", s.handleCountTokens)

	base, cancelBase := context.WithCancel(context.Background())
	s.cancelBase = cancelBase
	return &http.Server{
		BaseContext:       func(net.Listener) context.Context { return base },
		Addr:              net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port)),
		Handler:           withBodyDeadline(withMiddleware(apiKeyGate(mux, cfg.APIKey))),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

// shutdown stops srv gracefully, cancels CLI runs that outlive the grace
// period, and closes the run keeper.
func (s *server) shutdown(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if srv.Shutdown(ctx) != nil {
		if s.cancelBase != nil {
			s.cancelBase() // kill CLI runs that outlived the grace period, then let handlers return
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel2()
		srv.Shutdown(ctx2)
	}
	s.keeper.Close()
}

// programName is the name the program was started as. The npm launcher sets
// MLAI_WRAP_ARGV0 because Windows ignores argv[0] overrides.
func programName(arg0 string) string {
	if v := os.Getenv("MLAI_WRAP_ARGV0"); v != "" {
		arg0 = v
		os.Unsetenv("MLAI_WRAP_ARGV0") // not for the clients this starts
	}
	return binaryName(arg0)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 && strings.HasPrefix(programName(os.Args[0]), "mlai-wrap") {
		printHelp(os.Stdout)
		return
	}
	if len(args) == 0 || (strings.HasPrefix(args[0], "-") && !isHelpFlag(args[0])) {
		serveMain(args)
		return
	}
	if args[0] == "serve" {
		serveMain(args[1:])
		return
	}
	os.Exit(runCLI(args, os.Stdout, os.Stderr))
}

// serveMain runs the bridge until SIGINT or SIGTERM.
func serveMain(args []string) {
	initFlags(args)
	if err := validateConfig(cfg); err != nil {
		logger.Fatal(err)
	}
	s := newServer()
	srv := newHTTPServer(s)

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		logger.Fatalf("listen: %v", err)
	}
	// Only a loopback bridge is discoverable: the launcher sends its key there.
	if isLoopbackHost(cfg.Bind) {
		port := cfg.Port
		if ta, ok := ln.Addr().(*net.TCPAddr); ok {
			port = ta.Port
		}
		if secret, err := publishDiscovery(cfg.Bind, port); err != nil {
			logger.Printf("discovery file not written (claude/codex will start a private bridge): %v", err)
		} else {
			s.discoverySecret = secret
		}
	}

	go func() {
		auth := "open"
		if cfg.APIKey != "" {
			auth = "bearer key required"
		}
		logger.Printf("mlai-bridge %s on http://%s  (mlai=%s model=%s concurrency=%d cache=%s auth=%s)",
			version, srv.Addr, cfg.MLAI, cfg.DefaultModel, cap(s.sem), cfg.CacheTTL, auth)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Print("shutting down")
	if s.discoverySecret != "" {
		removeDiscovery(s.discoverySecret)
	}
	s.shutdown(srv)
}

// ---------- discovery ----------

// discoveryFile is where `serve` announces itself to the launcher. Empty means
// ~/mlai-bridge/serve.json, looked up when needed. Tests set it.
var discoveryFile string

func discoveryPath() string {
	if discoveryFile != "" {
		return discoveryFile
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "mlai-bridge", "serve.json")
}

// discoveryDoc is the content of the discovery file. The secret never leaves
// the file: /healthz only returns an HMAC made with it.
type discoveryDoc struct {
	URL    string `json:"url"`
	PID    int    `json:"pid"`
	Secret string `json:"secret"`
}

// challengePattern is what /healthz?challenge= accepts: 16 to 128 hex digits.
var challengePattern = regexp.MustCompile(`^[0-9A-Fa-f]{16,128}$`)

// discoveryMAC is HMAC-SHA256 keyed with the secret string, over the challenge string.
func discoveryMAC(secret, challenge string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(challenge))
	return m.Sum(nil)
}

// publishDiscovery writes the discovery file for a bridge listening on
// bind:port and returns its secret. The file is written to a temporary file in
// the same directory and renamed, so a reader never sees a partial file.
func publishDiscovery(bind string, port int) (string, error) {
	path := discoveryPath()
	if path == "" {
		return "", errors.New("no home directory")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(raw)
	doc, err := json.Marshal(discoveryDoc{
		URL:    "http://" + net.JoinHostPort(bind, strconv.Itoa(port)),
		PID:    os.Getpid(),
		Secret: secret,
	})
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".serve-*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(doc)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), 0o600)); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return secret, nil
}

// removeDiscovery deletes the discovery file if it still holds secret, so a
// newer bridge's file is left alone.
func removeDiscovery(secret string) {
	path := discoveryPath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var doc discoveryDoc
	if json.Unmarshal(raw, &doc) == nil && doc.Secret == secret {
		os.Remove(path)
	}
}
