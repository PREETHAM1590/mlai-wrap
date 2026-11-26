package main

// isolate.go — run mlai without the user's Claude Code skills and agents.
//
// mlai loads skills and agents from the folder above its data folder
// (MLAI_HOME, default ~/.mlai), i.e. ~/.claude/skills and ~/.claude/agents,
// and copies their text into every request. Its Windows engine reads that
// request with the ANSI code page (cp1252) and crashes on characters such as
// U+201D. Pointing MLAI_HOME at <home>/mlai-bridge/home/.mlai, a link to the
// real data folder, keeps the sign-in and sessions but hides those skills.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// mlaiHome is the MLAI_HOME given to every mlai child, or "" to leave it alone.
var mlaiHome string

// realMlaiHome is mlai's own data folder: MLAI_HOME or ~/.mlai.
func realMlaiHome() string {
	if v := os.Getenv("MLAI_HOME"); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".mlai")
	}
	return ""
}

// setupIsolatedHome makes base/.mlai a link to the real data folder and returns
// it, ready to use as MLAI_HOME. It never removes anything: when base/.mlai
// exists and is not a link to the real folder, it returns an error.
func setupIsolatedHome(base string) (string, error) {
	real := realMlaiHome()
	if real == "" {
		return "", fmt.Errorf("no home folder")
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", fmt.Errorf("mlai data folder %s not found (sign in with `mlai login` first)", real)
	}
	link := filepath.Join(base, ".mlai")
	if _, err := os.Lstat(link); err == nil {
		if sameFolder(link, real) {
			return link, nil
		}
		return "", fmt.Errorf("%s exists and is not a link to %s; leaving it alone", link, real)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	if err := makeDirLink(link, real); err != nil {
		return "", fmt.Errorf("link %s to %s: %w", link, real, err)
	}
	if !sameFolder(link, real) {
		return "", fmt.Errorf("%s does not resolve to %s", link, real)
	}
	return link, nil
}

// sameFolder reports whether a and b are the same folder once links are
// followed. os.Stat follows Windows junctions; filepath.EvalSymlinks does not.
func sameFolder(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	return err == nil && os.SameFile(sa, sb)
}

// makeDirLink creates a directory junction on Windows (no admin rights needed)
// and a symlink elsewhere.
func makeDirLink(link, target string) error {
	if runtime.GOOS != "windows" {
		return os.Symlink(target, link)
	}
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// initIsolation sets mlaiHome when isolation is on, logging why it is off otherwise.
func initIsolation() {
	mlaiHome = ""
	if !cfg.IsolateSkills {
		return
	}
	h, err := os.UserHomeDir()
	if err != nil {
		logger.Printf("skill isolation is off: %v", err)
		return
	}
	link, err := setupIsolatedHome(filepath.Join(h, "mlai-bridge", "home"))
	if err != nil {
		logger.Printf("skill isolation is off: %v", err)
		return
	}
	mlaiHome = link
}

// cp1252Undefined are the bytes cp1252 cannot decode. A UTF-8 character that
// contains one of them crashes mlai's Windows engine.
var cp1252Undefined = [256]bool{0x81: true, 0x8d: true, 0x8f: true, 0x90: true, 0x9d: true}

// asciiFor replaces the common characters that hit cp1252Undefined.
var asciiFor = map[rune]string{
	'“': `"`, '”': `"`, // curly double quotes (the opening one is kept symmetric)
	'‐': "-", '‑': "-", '‒': "-", '–': "-", '—': "--", '―': "--",
	'←': "<-", '→': "->", '↔': "<->",
	' ': " ",
}

// cp1252Safe returns s with every character whose UTF-8 encoding contains a
// byte cp1252 leaves undefined replaced by an ASCII equivalent (or "?").
func cp1252Safe(s string) string {
	var b strings.Builder
	changed := false
	for _, r := range s {
		enc := string(r)
		bad := false
		for i := 0; i < len(enc) && r > 0x7f; i++ {
			if cp1252Undefined[enc[i]] {
				bad = true
				break
			}
		}
		if !bad {
			if r == '“' { // keep pairs of quotes matching
				b.WriteString(`"`)
				changed = true
				continue
			}
			b.WriteString(enc)
			continue
		}
		changed = true
		if a, ok := asciiFor[r]; ok {
			b.WriteString(a)
		} else {
			b.WriteString("?")
		}
	}
	if !changed {
		return s
	}
	return b.String()
}
