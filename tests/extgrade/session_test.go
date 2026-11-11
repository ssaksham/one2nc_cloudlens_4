package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func fakeTmux(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func requireEmptyWork(t *testing.T, work string) {
	t.Helper()
	entries, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("session files leaked: %v", entries)
	}
}

func TestSessionCloseRemovesHomeAfterStoppingProgram(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "stopped")
	fakeTmux(t, "if [ \"$3\" = kill-server ] && [ -d \"$EXTGRADE_TEST_HOME\" ]; then printf stopped > \"$EXTGRADE_TEST_MARKER\"; fi\n")
	t.Setenv("EXTGRADE_TEST_MARKER", marker)
	h := &harness{work: t.TempDir(), bin: "/unused"}
	s, err := h.start(&fixture{url: "http://127.0.0.1:1"}, "credentials", "config", nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXTGRADE_TEST_HOME", s.home)
	if err := os.WriteFile(filepath.Join(s.home, "cloudlens.log"), []byte("session output"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.close()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("program was not stopped before HOME removal: %v", err)
	}
	requireEmptyWork(t, h.work)
}

func TestFailedStartRemovesHome(t *testing.T) {
	for _, failAt := range []string{"credentials", "tmux"} {
		t.Run(failAt, func(t *testing.T) {
			fakeTmux(t, "exit 1\n")
			h := &harness{work: t.TempDir(), bin: "/unused"}
			if failAt == "credentials" {
				path := filepath.Join(h.work, fmt.Sprintf("home-%d", sessionSeq+1), "credentials")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := h.start(&fixture{url: "http://127.0.0.1:1"}, "credentials", "config", nil, nil, false); err == nil {
				t.Fatal("start unexpectedly succeeded")
			}
			requireEmptyWork(t, h.work)
		})
	}
}

func TestDirectRunRemovesHome(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "program")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf output > \"$HOME/cloudlens.log\"\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &harness{work: t.TempDir(), bin: bin}
	if _, code := h.runDirect(&fixture{url: "http://127.0.0.1:1"}, "credentials", "config", nil, nil); code != 7 {
		t.Fatalf("exit=%d, want 7", code)
	}
	requireEmptyWork(t, h.work)
}
