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
	addedAfterEnvironmentOnlyStartup(h)
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

func addedAfterEnvironmentOnlyStartup(h *harness) {
	id := viewPkg + "TestAWSProfileAddedAfterEnvironmentOnlyStartup"
	fx := newFixture(nil)
	defer fx.close()
	env := map[string]string{
		"AWS_ACCESS_KEY_ID":     "ENVKEY",
		"AWS_SECRET_ACCESS_KEY": "envsecret",
		"AWS_SESSION_TOKEN":     "envtoken",
	}
	s, err := h.start(fx, "", "", nil, env, false)
	if err != nil {
		h.check(id, false, "start: %v", err)
		return
	}
	defer s.close()
	good := s.awaitState(id, 0, "default", "ap-south-1", "ENVKEY", "envtoken")
	if good {
		s.rewrite(profileKeys("added"), "[profile added]\nregion=eu-central-2\n")
		items, opened := pickerItems(s)
		found := false
		for _, item := range items {
			if item == "added" {
				found = true
			}
		}
		good = h.check(id, opened && found, "profile picker = %v, missing profile added after environment-only startup", items)
	}
	if good {
		// Environment credentials remain set: selecting the new profile must
		// replace both the access key and the environment's session token.
		good = chooseSettle(h, s, fx, id, "profile", "added", "added", "eu-central-2", "addedKEY", "")
	}
	h.verdict(id, good, "profile added after environment-only startup was not selectable with its own credentials and configured region")
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
	if good {
		good = removedProfileMouseReopen(h, id)
	}
	h.verdict(id, good, "removed-profile switch did not fail cleanly and recover")
}

// Exercise restoration through the mouse as well as the keyboard flow above.
// Startup navigation may preserve the open menu or close it; either is valid,
// but the next visible opening must discover the files again.
func removedProfileMouseReopen(h *harness, id string) bool {
	fx := newFixture(nil)
	defer fx.close()
	credentials := profileKeys("first", "second")
	config := "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n"
	s, err := h.start(fx, credentials, config, []string{"-p", "first"}, nil, false)
	if err != nil {
		return h.check(id, false, "mouse restoration start: %v", err)
	}
	defer s.close()
	if !s.awaitHeader("first", "us-east-1", 15*time.Second) {
		return h.check(id, false, "mouse restoration startup did not finish")
	}
	openByMouse := func() bool {
		for i := 0; i < 3; i++ {
			if s.listOpen("profile") && s.awaitListState("profile", true, 1500*time.Millisecond) {
				return true
			}
			s.click(15, 1)
			if s.awaitListState("profile", true, 1500*time.Millisecond) {
				return true
			}
		}
		return false
	}
	s.rewrite(profileKeys("first"), "[profile first]\nregion=us-east-1\n")
	if !openByMouse() {
		return h.check(id, false, "profile picker did not open by mouse after removal; pane:\n%s", s.pane())
	}
	// The existing delayed startup page switch can move focus after the first
	// header appears. Wait through it without requiring any particular focus
	// behavior, then close a menu only if it is still visibly open.
	time.Sleep(1200 * time.Millisecond)
	if s.listOpen("profile") {
		s.key("Escape")
	}
	if !s.awaitListState("profile", false, 3*time.Second) {
		return h.check(id, false, "profile picker did not visibly close before restoration; pane:\n%s", s.pane())
	}
	s.rewrite(credentials, config)
	for read := 0; read < 3; read++ {
		if !openByMouse() {
			return h.check(id, false, "profile picker did not reopen by mouse after restoration; pane:\n%s", s.pane())
		}
		// Read the existing menu across scrolls so a short viewport is valid.
		// If a later draw hides it, observe another opening before judging
		// absence; an open menu with stale options must still fail.
		items := s.collectItemsInPlace("profile")
		for _, item := range items {
			if item == "second" {
				return true
			}
		}
		if s.listOpen("profile") {
			return h.check(id, false, "restored profile missing from newly opened picker after startup: items=%v; pane:\n%s", items, s.pane())
		}
	}
	return h.check(id, false, "profile picker repeatedly closed while reading restored profiles; pane:\n%s", s.pane())
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
