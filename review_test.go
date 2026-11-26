package main

// review_test.go: regression tests for the review findings (UTF-8 safe
// splitting, cache byte limits, per-instance keeper folders, shutdown of a
// warming keeper, slow stream readers).

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSplitTextKeepsRunesWhole(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
	}{
		{"ascii", strings.Repeat("abcdefghij", 12), 48},
		{"shorter than n", "hello", 48},
		{"latin", "héllo wörld", 3},
		{"emoji", strings.Repeat("a😀", 40), 5},
		{"cjk", strings.Repeat("日本語", 30), 48},
		{"rune longer than n", "😀😀😀", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pieces := splitText(c.in, c.n)
			if got := strings.Join(pieces, ""); got != c.in {
				t.Errorf("joined = %q, want %q", got, c.in)
			}
			for _, p := range pieces {
				if p == "" || !utf8.ValidString(p) {
					t.Errorf("bad piece %q", p)
				}
				if len(p) > c.n && utf8.RuneCountInString(p) != 1 {
					t.Errorf("piece %q is longer than %d bytes and not a single rune", p, c.n)
				}
			}
		})
	}
	if got := splitText("", 48); got != nil {
		t.Errorf("empty input gave %q", got)
	}
}

func TestLRUByteLimits(t *testing.T) {
	oldBytes, oldEntry := cacheMaxBytes, cacheMaxEntry
	t.Cleanup(func() { cacheMaxBytes, cacheMaxEntry = oldBytes, oldEntry })
	cacheMaxBytes, cacheMaxEntry = 100, 40
	c := NewLRU(100, time.Hour)
	val := func(n int) []byte { return []byte(strings.Repeat("v", n)) }

	// Each entry is 1 key byte + 30 value bytes: three fit, the fourth evicts the oldest.
	for _, k := range []string{"a", "b", "c"} {
		c.Put(k, val(30))
	}
	if c.bytes != 93 {
		t.Errorf("bytes = %d, want 93", c.bytes)
	}
	c.Put("d", val(30))
	if _, ok := c.Get("a"); ok {
		t.Error("the oldest entry survived the byte limit")
	}
	for _, k := range []string{"b", "c", "d"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("entry %q was evicted too early", k)
		}
	}
	if c.bytes != 93 || c.order.Len() != 3 {
		t.Errorf("bytes = %d, entries = %d, want 93 and 3", c.bytes, c.order.Len())
	}

	// An oversize entry is not stored and drops the older value under its key.
	c.Put("big", val(41))
	if _, ok := c.Get("big"); ok {
		t.Error("an oversize entry was stored")
	}
	c.Put("b", val(41))
	if _, ok := c.Get("b"); ok {
		t.Error("an oversize update left the old value behind")
	}
	if c.bytes != 62 || c.order.Len() != 2 {
		t.Errorf("bytes = %d, entries = %d, want 62 and 2", c.bytes, c.order.Len())
	}

	// Replacing a value adjusts the total.
	c.Put("c", val(10))
	if c.bytes != 42 {
		t.Errorf("bytes = %d after a smaller update, want 42", c.bytes)
	}

	// Expiry subtracts the bytes.
	short := NewLRU(10, time.Nanosecond)
	short.Put("k", val(5))
	time.Sleep(time.Millisecond)
	if _, ok := short.Get("k"); ok || short.bytes != 0 {
		t.Errorf("expired entry: bytes = %d", short.bytes)
	}
}

func TestKeepersOnOneWorkdirAreSeparate(t *testing.T) {
	setupStub(t)
	k1 := newRunKeeper(cfg.MLAI, cfg.WorkDir)
	k2 := newRunKeeper(cfg.MLAI, cfg.WorkDir)
	if k1.dir == k2.dir {
		t.Fatalf("both keepers use %s", k1.dir)
	}
	base := filepath.Join(cfg.WorkDir, ".keeper")
	if k1.markerDir != base || k2.markerDir != base {
		t.Errorf("markerDir = %q and %q, want %q", k1.markerDir, k2.markerDir, base)
	}
	for _, k := range []*runKeeper{k1, k2} {
		if filepath.Dir(k.dir) != base {
			t.Errorf("keeper dir %q is not directly inside %q", k.dir, base)
		}
		if st, err := os.Stat(k.dir); err != nil || !st.IsDir() {
			t.Errorf("keeper dir %q was not created: %v", k.dir, err)
		}
	}

	session := t.TempDir()
	raw, _ := json.Marshal(map[string]string{"root": k1.dir})
	if err := os.WriteFile(filepath.Join(session, "session.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if !sessionRunsIn(session, k1.dir) {
		t.Error("a keeper rejects its own session")
	}
	if sessionRunsIn(session, k2.dir) {
		t.Error("a keeper accepts the session of another keeper")
	}

	// Close removes only the folder of its own instance.
	k1.Close()
	if _, err := os.Stat(k1.dir); !os.IsNotExist(err) {
		t.Errorf("Close left %s behind (%v)", k1.dir, err)
	}
	for _, p := range []string{k2.dir, base} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("Close removed %s: %v", p, err)
		}
	}
	k2.Close()
}

func TestCloseStopsAWarmingKeeper(t *testing.T) {
	env := setupStub(t)
	t.Setenv("STUB_BG", "slow")
	k := newRunKeeper(cfg.MLAI, cfg.WorkDir)
	t.Cleanup(func() { waitKeeperIdle(t, k) })

	result := make(chan error, 1)
	go func() {
		_, err := k.Refresh(context.Background(), "")
		result <- err
	}()
	var done chan struct{}
	waitFor(t, 20*time.Second, "the keeper process to start", func() bool {
		k.mu.Lock()
		defer k.mu.Unlock()
		done = k.procDone
		return done != nil
	})

	t0 := time.Now()
	k.Close()
	if d := time.Since(t0); d > 20*time.Second {
		t.Errorf("Close took %s", d)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Error("the keeper process is still running after Close")
	}
	select {
	case err := <-result:
		if err == nil {
			t.Error("Refresh succeeded although the keeper was closed")
		}
	case <-time.After(10 * time.Second):
		t.Error("Refresh did not return after Close")
	}
	if n := env.count(t, "-y"); n != 1 {
		t.Errorf("keeper ran %d times, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(k.markerDir, unsupportedFile)); !os.IsNotExist(err) {
		t.Errorf("a closed keeper marked direct mode as unsupported (%v)", err)
	}
	if _, err := os.Stat(k.dir); !os.IsNotExist(err) {
		t.Errorf("Close left %s behind (%v)", k.dir, err)
	}
}

func TestSlowStreamReaderLosesTheConnection(t *testing.T) {
	saveGlobals(t)
	oldStall := writeStall
	writeStall = 300 * time.Millisecond
	t.Cleanup(func() { writeStall = oldStall })

	handlerDone := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		renewWriteDeadline(w)
		w.WriteHeader(200)
		event := "data: " + strings.Repeat("x", 4096) + "\n\n"
		for range 4096 { // about 16 MiB, far more than the socket buffers hold
			if _, err := fmt.Fprint(w, event); err != nil {
				handlerDone <- err
				return
			}
			streamFlush(w)
			if r.Context().Err() != nil {
				handlerDone <- r.Context().Err()
				return
			}
		}
		handlerDone <- nil
	}))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetReadBuffer(1024)
	}
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	// Read the headers and then stop reading.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var head []byte
	one := make([]byte, 1)
	for !strings.HasSuffix(string(head), "\r\n\r\n") {
		if _, err := conn.Read(one); err != nil {
			t.Fatalf("reading the headers: %v (got %q)", err, head)
		}
		head = append(head, one[0])
	}

	select {
	case err := <-handlerDone:
		if err == nil {
			t.Error("the handler wrote everything to a client that does not read")
		}
	case <-time.After(10 * time.Second):
		t.Error("the handler is still blocked on a client that does not read")
	}
}
