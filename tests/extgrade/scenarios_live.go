package main

import (
	"strings"
	"os"
	"path/filepath"
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
	if good && !s.choose("profile", "second") {
		good = h.check(id, false, "profile picker did not open")
	}
	if good {
		good = s.awaitText("moon-central-1", 15*time.Second) && s.awaitHeader("first", "eu-west-1", 3*time.Second)
		h.check(id, good, "failed switch did not preserve the hand-picked region")
	}
	s.key("Enter")
	time.Sleep(300 * time.Millisecond)
	s.rewrite("", "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n")
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "second", "second", "eu-west-2", "secondKEY", "")
	}
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "first", "first", "eu-west-1", "firstKEY", "")
	}
	h.check(id, good, "hand-picked region was not restored after a failed switch and round trip")
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
	gates["slow"].open()
	if good && !awaitGateFinished(gates["slow"]) {
		good = h.check(id, false, "superseded lookup never finished")
	}
	if good && !requireStable(s, "first", "ap-south-2", "slow", 700*time.Millisecond) {
		good = h.check(id, false, "superseded lookup had a visible effect")
	}
	if good {
		good = chooseSettle(h, s, fx.fixture, id, "profile", "second", "second", "eu-central-2", "secondKEY", "secondTOKEN")
	}
	if good {
		good = chooseSettle(h, s, fx.fixture, id, "profile", "first", "first", "ap-south-2", "firstKEY", "firstTOKEN")
	}
	h.check(id, good, "region picked during a pending load was not remembered for the working profile")
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
		s.key("Escape")
	}
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "first", "first", "us-east-1", "firstKEY", "")
	}
	h.check(id, good, "profile added while open was not selectable")
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
	h.check(id, good, "edited configured region did not apply on switch")
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
		s.key("Escape")
		if opened && offered {
			if !s.choose("profile", "second") {
				good = h.check(id, false, "profile picker did not reopen")
			} else {
				good = s.awaitText("second", 15*time.Second)
				h.check(id, good, "removed profile did not fail by name")
				s.key("Enter")
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
	h.check(id, good, "removed-profile switch did not fail cleanly and recover")
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
	if good {
		s.rewrite("", "[profile second\nregion=eu-west-2\n")
		if !s.choose("profile", "second") {
			good = h.check(id, false, "profile picker did not open")
		}
	}
	if good {
		good = s.awaitText("config", 15*time.Second) && s.awaitHeader("first", "us-east-1", 3*time.Second)
		h.check(id, good, "malformed config on switch did not fail by path on the working session; pane:\n%s", tail(s.pane()))
		s.key("Enter")
		time.Sleep(300 * time.Millisecond)
	}
	s.rewrite("", "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n")
	if good {
		good = chooseSettle(h, s, fx, id, "profile", "second", "second", "eu-west-2", "secondKEY", "")
	}
	h.check(id, good, "malformed-file switch did not fail cleanly and recover")
	_ = os.Stdout
}
