package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsolatedHomeLinksTheRealDataFolder(t *testing.T) {
	saveGlobals(t)
	home := t.TempDir()
	real := filepath.Join(home, ".mlai")
	if err := os.MkdirAll(filepath.Join(real, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MLAI_HOME", "")
	base := filepath.Join(home, "mlai-bridge", "home")

	link, err := setupIsolatedHome(base)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(link, "marker")); err != nil || string(b) != "x" {
		t.Fatalf("link does not reach the real folder: %v", err)
	}
	// The link's parent holds no .claude, so mlai finds no user skills there.
	if _, err := os.Stat(filepath.Join(base, ".claude")); err == nil {
		t.Error("isolated home must not contain .claude")
	}
	if again, err := setupIsolatedHome(base); err != nil || again != link {
		t.Errorf("second call: %q, %v", again, err)
	}
	t.Cleanup(func() { os.Remove(link) }) // removes the link only, never the target

	// A real folder in the way is left alone and isolation reports why.
	other := filepath.Join(home, "other")
	if err := os.MkdirAll(filepath.Join(other, ".mlai"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := setupIsolatedHome(other); err == nil || !strings.Contains(err.Error(), "leaving it alone") {
		t.Errorf("real folder in the way: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, ".mlai")); err != nil {
		t.Error("the folder in the way was removed")
	}
}

func TestChildEnvSetsIsolatedHome(t *testing.T) {
	saveGlobals(t)
	old := mlaiHome
	t.Cleanup(func() { mlaiHome = old })
	t.Setenv("MLAI_HOME", "/elsewhere/.mlai")

	mlaiHome = ""
	if env := strings.Join(childEnv(), "\n"); !strings.Contains(env, "MLAI_HOME=/elsewhere/.mlai") {
		t.Error("with isolation off the user's MLAI_HOME must pass through")
	}
	mlaiHome = filepath.Join("x", "home", ".mlai")
	n := 0
	for _, kv := range childEnv() {
		if strings.HasPrefix(strings.ToUpper(kv), "MLAI_HOME=") {
			n++
			if kv != "MLAI_HOME="+mlaiHome {
				t.Errorf("got %q", kv)
			}
		}
	}
	if n != 1 {
		t.Errorf("MLAI_HOME appears %d times", n)
	}
}

func TestCP1252Safe(t *testing.T) {
	for in, want := range map[string]string{
		"plain ascii": "plain ascii",
		"“quoted”":    `"quoted"`,
		// Only characters with a byte cp1252 cannot decode change.
		"it’s fine é — → ok": "it’s fine é — → ok",
		"a‐b":                "a-b",
		"日本":                 "日本",
		"⁝ odd":              "? odd",
	} {
		got := cp1252Safe(in)
		if got != want {
			t.Errorf("cp1252Safe(%q) = %q, want %q", in, got, want)
		}
		for _, r := range got {
			enc := string(r)
			for i := 0; i < len(enc) && r > 0x7f; i++ {
				if cp1252Undefined[enc[i]] {
					t.Errorf("%q still contains %U", got, r)
				}
			}
		}
	}
}

func TestJunctionCmdLineQuotesCmdMetacharacters(t *testing.T) {
	line, err := junctionCmdLine(`C:\Users\A&B\link`, `C:\Users\A&B (x)\.mlai`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `cmd.exe /d /c mklink /J "C:\Users\A&B\link" "C:\Users\A&B (x)\.mlai"`; line != want {
		t.Errorf("line = %s\nwant   %s", line, want)
	}
	for _, bad := range []string{`C:\Users\100%x%\.mlai`, `C:\Users\a"b`} {
		if _, err := junctionCmdLine(`C:\link`, bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// On Windows, a real junction under a home folder whose name has & in it.
func TestJunctionWithAmpersandInPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows-only")
	}
	base := filepath.Join(t.TempDir(), "A&B")
	target := filepath.Join(base, "real")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "probe"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := makeDirLink(link, target); err != nil {
		t.Fatalf("makeDirLink: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(link, "probe")); err != nil || string(raw) != "ok" {
		t.Errorf("reading through the junction: %q, %v", raw, err)
	}
	os.Remove(link) // removes the junction only
}
