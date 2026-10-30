package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func reportForTest(t *testing.T, h *harness) junitSuite {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.xml")
	if err := h.writeJUnit(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report junitSuite
	if err := xml.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestReportRequiresCompletedPassingVerdict(t *testing.T) {
	const id = "example.TestSelection"
	for _, tc := range []struct {
		name string
		run  func(*harness)
		fail bool
	}{
		{"unobserved", func(h *harness) {}, true},
		{"intermediate success only", func(h *harness) { h.check(id, true, "") }, true},
		{"completed", func(h *harness) { h.verdict(id, true, "") }, false},
		{"pass then fail", func(h *harness) {
			h.verdict(id, true, "")
			h.check(id, false, "later failure")
		}, true},
		{"fail then pass", func(h *harness) {
			h.check(id, false, "earlier failure")
			if h.verdict(id, true, "") {
				t.Error("final verdict ignored an earlier failure")
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &harness{results: map[string]*result{}, ids: []string{id}}
			tc.run(h)
			report := reportForTest(t, h)
			if report.Tests != 1 || len(report.Cases) != 1 {
				t.Fatalf("wrong report shape: %+v", report)
			}
			if got := report.Cases[0].Failure != nil; got != tc.fail {
				t.Fatalf("failed=%v, want %v", got, tc.fail)
			}
		})
	}
}

func TestGroupPanicDiscardsPartialSuccess(t *testing.T) {
	const id = "example.TestSelection"
	h := &harness{results: map[string]*result{}, ids: []string{id}, timeout: time.Second}
	if h.run("panicking scenario", func(group *harness) {
		group.verdict(id, true, "")
		panic("scenario stopped before completion")
	}) {
		t.Fatal("panic did not stop grading")
	}
	if report := reportForTest(t, h); report.Failures != 1 {
		t.Fatalf("panicking group published success: %+v", report)
	}
}

func TestGroupTimeoutCannotPublishEarlyOrLateSuccess(t *testing.T) {
	const complete = "example.TestComplete"
	const incomplete = "example.TestIncomplete"
	h := &harness{results: map[string]*result{}, ids: []string{complete, incomplete}, timeout: 30 * time.Millisecond}
	if !h.run("completed scenario", func(group *harness) {
		group.verdict(complete, true, "")
	}) {
		t.Fatal("completed group did not finish")
	}
	release := make(chan struct{})
	finished := make(chan struct{})
	if h.run("timed out scenario", func(group *harness) {
		defer close(finished)
		group.verdict(incomplete, true, "")
		<-release
		group.verdict(incomplete, true, "")
	}) {
		close(release)
		t.Fatal("timeout did not stop grading")
	}
	// The abandoned worker may finish after the timeout. Neither its early
	// result nor its late write may reach the published report.
	close(release)
	<-finished
	report := reportForTest(t, h)
	if report.Failures != 1 || report.Cases[0].Failure != nil || report.Cases[1].Failure == nil {
		t.Fatalf("timeout changed completed work or published incomplete work: %+v", report)
	}
}
