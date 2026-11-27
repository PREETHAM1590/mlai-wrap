package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// overrideBases makes placeKeeper try exactly these bases.
func overrideBases(t *testing.T, bases ...string) {
	t.Helper()
	old := keeperBases
	keeperBases = func(string) []string { return bases }
	t.Cleanup(func() { keeperBases = old })
}

// A workdir reached through a link into a repository, whose .keeper does not
// exist yet, is still seen as inside the repository.
func TestGitCheckFollowsALinkToAMissingFolder(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "sub")
	if err := errors.Join(os.MkdirAll(filepath.Join(repo, ".git"), 0o700), os.MkdirAll(sub, 0o700)); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := makeDirLink(alias, sub); err != nil {
		t.Skipf("cannot make a directory link here: %v", err)
	}
	missing := filepath.Join(alias, "workspace", ".keeper")
	if !inGitTree(missing) {
		t.Fatalf("%s (through %s -> %s) not seen as inside the repository", missing, alias, sub)
	}
	if want := filepath.Join(physicalDir(sub), "workspace", ".keeper"); !sameDir(physicalDir(missing), want) {
		t.Errorf("physicalDir = %s, want %s", physicalDir(missing), want)
	}

	plain := filepath.Join(t.TempDir(), "keeper")
	overrideBases(t, missing, plain)
	base, dir, err := placeKeeper(filepath.Join(alias, "workspace"), "1-ab")
	if err != nil || !sameDir(base, plain) || inGitTree(dir) {
		t.Errorf("placeKeeper = %s, %s, %v; want the folder outside the repository", base, dir, err)
	}
	if _, err := os.Stat(filepath.Join(sub, "workspace")); !os.IsNotExist(err) {
		t.Error("placeKeeper created a folder inside the repository")
	}
}

// A base that cannot be created is skipped; the next usable one is taken.
func TestKeeperSkipsAFolderItCannotCreate(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(t.TempDir(), "keeper")
	overrideBases(t, filepath.Join(blocker, "keeper"), good)
	base, dir, err := placeKeeper("unused", "1-ab")
	if err != nil || !sameDir(base, good) {
		t.Fatalf("placeKeeper = %s, %v; want %s", base, err, good)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("keeper folder %s was not created (%v)", dir, err)
	}
}

// With no usable folder, no keeper is ever started and Refresh says why.
func TestNoKeeperWithoutAUsableFolder(t *testing.T) {
	env := setupStub(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	overrideBases(t, filepath.Join(repo, ".keeper"), filepath.Join(blocker, "keeper"))
	k := newRunKeeper(cfg.MLAI, repo)
	t.Cleanup(k.Close)
	for i := 0; i < 2; i++ {
		_, err := k.Refresh(context.Background(), "")
		if err == nil || !strings.Contains(err.Error(), "no folder for the keeper") {
			t.Fatalf("Refresh %d: %v, want the placement error", i, err)
		}
	}
	if n := env.count(t, "-y"); n != 0 {
		t.Errorf("keeper ran %d times, want 0", n)
	}
}

// A keeper that exits without a token reports its exit status and output.
func TestKeeperFailureKeepsTheChildError(t *testing.T) {
	setupStub(t)
	t.Setenv("STUB_BG", "fail")
	k := newRunKeeper(cfg.MLAI, cfg.WorkDir)
	t.Cleanup(func() { waitKeeperIdle(t, k) })
	_, err := k.Refresh(context.Background(), "")
	if err == nil || !errors.Is(err, errNoRunToken) {
		t.Fatalf("err = %v, want errNoRunToken", err)
	}
	for _, want := range []string{"exit status 1", "cannot start a background run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestOutputTailMasksTokens(t *testing.T) {
	got := outputTail("line one\nkey mlrun_abcDEF123-x and sk-live_99\n", 300)
	if strings.Contains(got, "abcDEF") || strings.Contains(got, "live_99") || !strings.Contains(got, "mlrun_***") {
		t.Errorf("outputTail = %q", got)
	}
	if long := outputTail(strings.Repeat("a", 1000)+"END", 10); !strings.HasSuffix(long, "END") || len(long) > 20 {
		t.Errorf("tail = %q", long)
	}
}
