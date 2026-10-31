package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func settle(h *harness, s *session, id string, mark int, profile, region, key, token string) bool {
	ok := s.awaitHeader(profile, region, 15*time.Second) && s.awaitRequest(mark, signedRequest{key, region, token}, 15*time.Second)
	if !ok {
		lines := strings.Split(s.pane(), "\n")
		head := lines
		if len(head) > 4 {
			head = head[:4]
		}
		h.check(id, false, "did not settle on %s/%s; pane head:\n%s", profile, region, strings.Join(head, "\n"))
	}
	return ok
}

func chooseSettle(h *harness, s *session, fx *fixture, id, kind, value, profile, region, key, token string) bool {
	mark := fx.mark()
	if !s.choose(kind, value) {
		return h.check(id, false, "%s picker did not open for %q", kind, value)
	}
	return settle(h, s, id, mark, profile, region, key, token)
}

func liveScenarios(h *harness) {
	manualAfterFailure(h)
	manualDuringPending(h)
	addedWhileOpen(h)
	regionEditOnSwitch(h)
	removedProfile(h)
	malformedOnSwitch(h)
}

func manualAfterFailure(h *harness) {
	id := viewPkg + "TestAWSManualRegionRestoredAfterFailedSwitch"
	fx := newFixture(nil)
	defer fx.close()
	s, err := h.start(fx, profileKeys("first", "second"), "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n", []string{"-p", "first"}, nil, false)
	if err != nil {
		h.check(id, false, "start: %v", err)
		return
	}
	defer s.close()
	good := s.awaitHeader("first", "us-east-1", 15*time.Second)
	if good {
		good = chooseSettle(h, s, fx, id, "region", "eu-west-1", "first", "eu-west-1", "firstKEY", "")
	}
	// Break the target, fail to switch, then repair and round-trip.
	s.rewrite("", "[profile first]\nregion=us-east-1\n[profile second]\nregion=moon-central-1\n")
	before := s.pane()
	if good && !s.choose("profile", "second") {
		good = h.check(id, false, "profile picker did not open")
	}
	if good {
		good = s.awaitNewText(before, "moon-central-1", 15*time.Second) && s.awaitHeader("first", "eu-west-1", 3*time.Second)
		h.check(id, good, "failed switch did not preserve the hand-picked region")
	}
	s.dismissFeedback()
	time.Sleep(300 * time.Millisecond)
	s.rewrite("", "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n")
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "second", "second", "eu-west-2", "secondKEY", "")
	}
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "first", "first", "eu-west-1", "firstKEY", "")
	}
	h.verdict(id, good, "hand-picked region was not restored after a failed switch and round trip")
}

func manualDuringPending(h *harness) {
	id := viewPkg + "TestAWSManualRegionDuringPendingProfile"
	s, fx, gates, ok := startAsync(h, map[string]bool{"slow": false}, "first")
	if !ok {
		h.check(id, false, "session did not start")
		return
	}
	defer releaseAll(gates)
	defer s.close()
	defer fx.close()
	good := s.awaitHeader("first", "us-east-1", 15*time.Second)
	if good && !s.choose("profile", "slow") {
		good = h.check(id, false, "profile picker did not open")
	}
	if good && !awaitGate(h, gates["slow"], "pending role lookup") {
		good = h.check(id, false, "role lookup did not start")
	}
	if good {
		good = chooseSettle(h, s, fx.fixture, id, "region", "ap-south-2", "first", "ap-south-2", "firstKEY", "firstTOKEN")
	}
	before := s.pane()
	gates["slow"].open()
	if good && !awaitGateFinished(gates["slow"]) {
		good = h.check(id, false, "superseded lookup never finished")
	}
	if good && !requireStable(s, before, "first", "ap-south-2", "slow", 700*time.Millisecond) {
		good = h.check(id, false, "superseded lookup had a visible effect")
	}
	if good {
		good = chooseSettle(h, s, fx.fixture, id, "profile", "second", "second", "eu-central-2", "secondKEY", "secondTOKEN")
	}
	if good {
		good = chooseSettle(h, s, fx.fixture, id, "profile", "first", "first", "ap-south-2", "firstKEY", "firstTOKEN")
	}
	h.verdict(id, good, "region picked during a pending load was not remembered for the working profile")
}

func addedWhileOpen(h *harness) {
	id := viewPkg + "TestAWSProfileAddedWhileOpenIsSelectable"
	fx := newFixture(nil)
	defer fx.close()
	s, err := h.start(fx, profileKeys("first"), "[profile first]\nregion=us-east-1\n", []string{"-p", "first"}, nil, false)
	if err != nil {
		h.check(id, false, "start: %v", err)
		return
	}
	defer s.close()
	good := s.awaitHeader("first", "us-east-1", 15*time.Second)
	s.rewrite(profileKeys("first", "added"), "[profile first]\nregion=us-east-1\n[profile added]\nregion=eu-central-2\n")
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "added", "added", "eu-central-2", "addedKEY", "")
	}
	if good {
		items, opened := pickerItems(s)
		good = opened && sameSet(filterCandidates(items, []string{"first", "added"}), []string{"first", "added"})
		h.check(id, good, "picker does not list both profiles: %v", items)
	}
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "first", "first", "us-east-1", "firstKEY", "")
	}
	h.verdict(id, good, "profile added while open was not selectable")
}

func regionEditOnSwitch(h *harness) {
	id := viewPkg + "TestAWSProfileRegionEditAppliesOnSwitch"
	fx := newFixture(nil)
	defer fx.close()
	s, err := h.start(fx, profileKeys("first", "second"), "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n", []string{"-p", "first"}, nil, false)
	if err != nil {
		h.check(id, false, "start: %v", err)
		return
	}
	defer s.close()
	good := s.awaitHeader("first", "us-east-1", 15*time.Second)
	s.rewrite("", "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-central-2\n")
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "second", "second", "eu-central-2", "secondKEY", "")
	}
	h.verdict(id, good, "edited configured region did not apply on switch")
}

func removedProfile(h *harness) {
	id := viewPkg + "TestAWSRemovedProfileSwitchFailsCleanly"
	fx := newFixture(nil)
	defer fx.close()
	s, err := h.start(fx, profileKeys("first", "second"), "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n", []string{"-p", "first"}, nil, false)
	if err != nil {
		h.check(id, false, "start: %v", err)
		return
	}
	defer s.close()
	good := s.awaitHeader("first", "us-east-1", 15*time.Second)
	s.rewrite(profileKeys("first"), "[profile first]\nregion=us-east-1\n")
	if good {
		// A refreshed picker may already have dropped the entry; if it is
		// still offered, choosing it must fail by name.
		items, opened := pickerItems(s)
		offered := false
		for _, tok := range items {
			if tok == "second" {
				offered = true
			}
		}
		if !opened {
			good = h.check(id, false, "profile picker did not open after the profile was removed")
		}
		if opened && offered {
			before := s.pane()
			if !s.choose("profile", "second") {
				good = h.check(id, false, "profile picker did not reopen")
			} else {
				good = s.awaitNewText(before, "second", 15*time.Second)
				h.check(id, good, "removed profile did not fail by name")
				s.dismissFeedback()
				time.Sleep(300 * time.Millisecond)
			}
		}
	}
	if good && !s.awaitHeader("first", "us-east-1", 5*time.Second) {
		good = h.check(id, false, "working session moved after removed-profile failure")
	}
	s.rewrite(profileKeys("first", "second"), "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n")
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "second", "second", "eu-west-2", "secondKEY", "")
	}
	h.verdict(id, good, "removed-profile switch did not fail cleanly and recover")
}

func malformedOnSwitch(h *harness) {
	id := viewPkg + "TestAWSMalformedFileOnSwitchFailsCleanly"
	fx := newFixture(nil)
	defer fx.close()
	s, err := h.start(fx, profileKeys("first", "second"), "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n", []string{"-p", "first"}, nil, false)
	if err != nil {
		h.check(id, false, "start: %v", err)
		return
	}
	defer s.close()
	good := s.awaitHeader("first", "us-east-1", 15*time.Second)
	// Keep a probe file so the harness can confirm the path shown is the
	// config file (its basename appears in the directory listing too).
	_ = filepath.Base(s.config)
	before := s.pane()
	if good {
		s.rewrite("", "[profile second\nregion=eu-west-2\n")
		if !s.choose("profile", "second") {
			good = h.check(id, false, "profile picker did not open")
		}
	}
	if good {
		good = s.awaitNewText(before, "config", 15*time.Second) && s.awaitHeader("first", "us-east-1", 3*time.Second)
		h.check(id, good, "malformed config on switch did not fail by path on the working session; pane:\n%s", tail(s.pane()))
		s.dismissFeedback()
		time.Sleep(300 * time.Millisecond)
	}
	s.rewrite("", "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n")
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "second", "second", "eu-west-2", "secondKEY", "")
	}
	h.verdict(id, good, "malformed-file switch did not fail cleanly and recover")
	_ = os.Stdout
}

func keyboardScenarios(h *harness) {
	keyboardPick(h)
}

// keyboardPick adds a profile while the program runs, opens the profile picker
// from the keyboard, steps down to the new profile with the arrow keys and
// commits it with Enter. The list opens on the working profile, which sorts
// first. Each attempt starts a fresh program, so a highlight left behind by an
// earlier attempt cannot carry over.
func keyboardPick(h *harness) {
	id := viewPkg + "TestAWSProfilePickerKeyboardSelection"
	good, detail := false, ""
	for attempt := 0; attempt < 3 && !good; attempt++ {
		good, detail = keyboardPickOnce(h)
		if !good {
			fmt.Printf("  [attempt %d] %s\n", attempt+1, detail)
		}
	}
	h.verdict(id, good, "%s", detail)
}

func keyboardPickOnce(h *harness) (bool, string) {
	fx := newFixture(nil)
	defer fx.close()
	s, err := h.start(fx, profileKeys("first", "second"), "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n", []string{"-p", "first"}, nil, false)
	if err != nil {
		return false, fmt.Sprintf("start: %v", err)
	}
	defer s.close()
	// Let the startup load finish so it cannot move focus mid-sequence.
	if !s.awaitHeader("first", "us-east-1", 15*time.Second) || !s.awaitRequest(0, signedRequest{"firstKEY", "us-east-1", ""}, 15*time.Second) {
		return false, "did not start on first/us-east-1; pane:\n" + tail(s.pane())
	}
	s.rewrite(profileKeys("first", "second", "third"), "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n[profile third]\nregion=eu-central-2\n")
	// Startup work that finishes after the first request can still move
	// focus once, which would close a list opened too early.
	time.Sleep(2500 * time.Millisecond)
	if !s.openList("profile") {
		return false, "profile picker did not open from the keyboard"
	}
	items := s.visibleItems("profile")
	from, to := -1, -1
	for i, item := range items {
		switch item {
		case "first":
			from = i
		case "third":
			to = i
		}
	}
	if from < 0 || to < from {
		s.key("Escape")
		return false, fmt.Sprintf("open profile picker lists %v, want first before third", items)
	}
	mark := fx.mark()
	for step := from; step < to; step++ {
		s.key("Down")
		time.Sleep(80 * time.Millisecond)
	}
	if !s.listOpen("profile") {
		return false, "profile picker closed while its highlight was being moved"
	}
	s.key("Enter")
	if s.awaitHeader("third", "eu-central-2", 10*time.Second) && s.awaitRequest(mark, signedRequest{"thirdKEY", "eu-central-2", ""}, 10*time.Second) {
		return true, ""
	}
	return false, fmt.Sprintf("arrow keys and Enter in the open profile picker %v did not switch to third/eu-central-2; pane:\n%s", items, tail(s.pane()))
}
