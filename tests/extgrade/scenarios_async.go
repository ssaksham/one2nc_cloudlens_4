package main

import (
	"fmt"
	"strings"
	"time"
)

// asyncFixture builds a fixture whose named roles are held open by gates.
// Credentials carry session tokens so signed service requests can be checked
// end to end.
func asyncFixture(gateDefs map[string]bool) (*fixtureWithFiles, map[string]*gate) {
	gates := map[string]*gate{}
	configured := map[string]bool{}
	var config strings.Builder
	config.WriteString("[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-central-2\n[profile invalid]\nregion=moon-central-1\n")
	for name, fail := range gateDefs {
		role := strings.SplitN(name, "@", 2)[0]
		gates[name] = newGate(fail)
		if !configured[role] {
			configured[role] = true
			config.WriteString(fmt.Sprintf("[profile %s]\nrole_arn=arn:aws:iam::123456789012:role/%s\nsource_profile=source\nregion=eu-west-2\n", role, role))
		}
	}
	credentials := profileKeys("first", "second", "source")
	credentials = strings.Replace(credentials, "[first]\n", "[first]\naws_session_token=firstTOKEN\n", 1)
	credentials = strings.Replace(credentials, "[second]\n", "[second]\naws_session_token=secondTOKEN\n", 1)
	fx := newFixture(gates)
	return &fixtureWithFiles{fx, credentials, config.String()}, gates
}

type fixtureWithFiles struct {
	*fixture
	credentials string
	config      string
}

func releaseAll(gates map[string]*gate) {
	for _, g := range gates {
		g.open()
	}
}

func awaitGate(t *harness, g *gate, what string) bool {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if g.isStarted() {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	fmt.Printf("  [fail] timed out waiting for %s\n", what)
	return false
}

func awaitGateFinished(g *gate) bool {
	select {
	case <-g.finished:
		return true
	case <-time.After(15 * time.Second):
		return false
	}
}

// requireStable samples the pane for a window: the committed selection must
// not move and no feedback about the superseded choice may appear.
func requireStable(s *session, profile, region, obsolete string, dur time.Duration) bool {
	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		pane := s.pane()
		if headerValue(profileHeaderRe, pane) != profile || headerValue(regionHeaderRe, pane) != region {
			return false
		}
		if obsolete != "" && strings.Contains(normalize(pane), normalize(obsolete)) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}

func asyncScenarios(h *harness) {
	pendingProfile(h)
	regionChoiceSupersedes(h)
	invalidChoiceSupersedes(h)
	latestRoleFailure(h)
	pendingRegion(h)
}

func startAsync(h *harness, gateDefs map[string]bool, profile string) (*session, *fixtureWithFiles, map[string]*gate, bool) {
	fx, gates := asyncFixture(gateDefs)
	s, err := h.start(fx.fixture, fx.credentials, fx.config, []string{"-p", profile}, nil, false)
	if err != nil {
		fmt.Printf("  [fail] start async session: %v\n", err)
		fx.close()
		return nil, fx, gates, false
	}
	return s, fx, gates, true
}

func pendingProfile(h *harness) {
	all := true
	for _, oldFailure := range []bool{false, true} {
		sub := fmt.Sprintf("%sTestAWSPendingProfileCanBeSuperseded/old_failure_%t", viewPkg, oldFailure)
		s, fx, gates, ok := startAsync(h, map[string]bool{"slow": oldFailure, "newer": false}, "first")
		if !ok {
			h.check(sub, false, "session did not start")
			all = false
			continue
		}
		func() {
			defer releaseAll(gates)
			defer s.close()
			defer fx.close()
			good := s.awaitHeader("first", "us-east-1", 15*time.Second)
			if good && !s.choose("profile", "slow") {
				good = h.check(sub, false, "profile picker did not open for slow")
			}
			if good && !awaitGate(h, gates["slow"], "older role lookup") {
				good = h.check(sub, false, "older role lookup did not start")
			}
			if good && !s.awaitHeader("first", "us-east-1", 3*time.Second) {
				good = h.check(sub, false, "working session moved while slow was pending")
			}
			if good && !s.choose("profile", "newer") {
				good = h.check(sub, false, "profile picker did not open for newer")
			}
			if good && !awaitGate(h, gates["newer"], "latest role lookup") {
				good = h.check(sub, false, "latest role lookup did not start")
			}
			mark := fx.mark()
			gates["newer"].open()
			if good && !s.awaitState(sub, mark, "newer", "eu-west-2", "newerKEY", "newerTOKEN") {
				good = false
			}
			gates["slow"].open()
			if good && !awaitGateFinished(gates["slow"]) {
				good = h.check(sub, false, "superseded lookup never finished or was cancelled")
			}
			if good && !requireStable(s, "newer", "eu-west-2", "slow", 700*time.Millisecond) {
				good = h.check(sub, false, "superseded lookup had a visible effect; pane:\n%s", tail(s.pane()))
			}
			h.check(sub, good, "pending-profile sequence failed (old_failure=%t)", oldFailure)
			all = all && good
		}()
	}
	h.check(viewPkg+"TestAWSPendingProfileCanBeSuperseded", all, "a pending-profile subcase failed")
}

func regionChoiceSupersedes(h *harness) {
	id := viewPkg + "TestAWSRegionChoiceSupersedesPendingProfile"
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
	mark := fx.mark()
	if good && !s.choose("region", "ap-south-2") {
		good = h.check(id, false, "region picker did not open")
	}
	if good {
		good = s.awaitHeader("first", "ap-south-2", 15*time.Second) && s.awaitRequest(mark, signedRequest{"firstKEY", "ap-south-2", "firstTOKEN"}, 15*time.Second)
		h.check(id, good, "region choice did not commit for the working profile")
	}
	gates["slow"].open()
	if good && !awaitGateFinished(gates["slow"]) {
		good = h.check(id, false, "superseded lookup never finished")
	}
	if good && !requireStable(s, "first", "ap-south-2", "slow", 700*time.Millisecond) {
		good = h.check(id, false, "superseded profile had a visible effect")
	}
	h.check(id, good, "region-choice supersede sequence failed")
}

func invalidChoiceSupersedes(h *harness) {
	id := viewPkg + "TestAWSInvalidChoiceSupersedesPendingProfile"
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
	if good && !s.choose("profile", "invalid") {
		good = h.check(id, false, "profile picker did not reopen")
	}
	if good {
		good = s.awaitText("moon-central-1", 15*time.Second) && s.awaitHeader("first", "us-east-1", 3*time.Second)
		h.check(id, good, "invalid choice did not fail visibly on the working session")
	}
	s.key("Enter")
	time.Sleep(300 * time.Millisecond)
	gates["slow"].open()
	if good && !awaitGateFinished(gates["slow"]) {
		good = h.check(id, false, "superseded lookup never finished")
	}
	if good && !requireStable(s, "first", "us-east-1", "slow", 700*time.Millisecond) {
		good = h.check(id, false, "superseded lookup had a visible effect")
	}
	mark := fx.mark()
	if good && s.choose("profile", "second") {
		good = s.awaitHeader("second", "eu-central-2", 15*time.Second) && s.awaitRequest(mark, signedRequest{"secondKEY", "eu-central-2", "secondTOKEN"}, 15*time.Second)
		h.check(id, good, "selection did not recover after invalid choice")
	} else if good {
		good = h.check(id, false, "profile picker did not open after invalid choice")
	}
	h.check(id, good, "invalid-choice supersede sequence failed")
}

func latestRoleFailure(h *harness) {
	id := viewPkg + "TestAWSLatestRoleFailureKeepsWorkingSession"
	s, fx, gates, ok := startAsync(h, map[string]bool{"denied": true}, "first")
	if !ok {
		h.check(id, false, "session did not start")
		return
	}
	defer releaseAll(gates)
	defer s.close()
	defer fx.close()
	good := s.awaitHeader("first", "us-east-1", 15*time.Second)
	if good && !s.choose("profile", "denied") {
		good = h.check(id, false, "profile picker did not open")
	}
	if good && !awaitGate(h, gates["denied"], "latest role lookup") {
		good = h.check(id, false, "role lookup did not start")
	}
	gates["denied"].open()
	if good {
		good = s.awaitText("denied", 15*time.Second) && s.awaitHeader("first", "us-east-1", 3*time.Second)
		h.check(id, good, "latest failure was not shown on the working session; pane:\n%s", tail(s.pane()))
	}
	s.key("Enter")
	time.Sleep(300 * time.Millisecond)
	mark := fx.mark()
	if good && s.choose("region", "us-east-2") {
		good = s.awaitHeader("first", "us-east-2", 15*time.Second) && s.awaitRequest(mark, signedRequest{"firstKEY", "us-east-2", "firstTOKEN"}, 15*time.Second)
		h.check(id, good, "region choice did not recover after role failure")
	} else if good {
		good = h.check(id, false, "region picker did not open after role failure")
	}
	mark = fx.mark()
	if good && s.choose("profile", "second") {
		good = s.awaitHeader("second", "eu-central-2", 15*time.Second) && s.awaitRequest(mark, signedRequest{"secondKEY", "eu-central-2", "secondTOKEN"}, 15*time.Second)
		h.check(id, good, "profile choice did not recover after role failure")
	} else if good {
		good = h.check(id, false, "profile picker did not reopen after role failure")
	}
	h.check(id, good, "latest-role-failure sequence failed")
}

func pendingRegion(h *harness) {
	all := true
	for _, target := range []string{"profile", "region"} {
		for _, oldFailure := range []bool{false, true} {
			sub := fmt.Sprintf("%sTestAWSPendingRegionCanBeSuperseded/%s_old_failure_%t", viewPkg, target, oldFailure)
			s, fx, gates, ok := startAsync(h, map[string]bool{"current": false, "current@us-west-2": oldFailure}, "current")
			if !ok {
				h.check(sub, false, "session did not start")
				all = false
				continue
			}
			func() {
				defer releaseAll(gates)
				defer s.close()
				defer fx.close()
				// The initial role lookup is ungated in effect: release it.
				gates["current"].open()
				good := s.awaitState(sub, 0, "current", "eu-west-2", "currentKEY", "currentTOKEN")
				if good && !s.choose("region", "us-west-2") {
					good = h.check(sub, false, "region picker did not open")
				}
				// A cached session may commit without a new STS request; a
				// fresh lookup is held by the gate. Either is legitimate.
				pending := false
				if good {
					deadline := time.Now().Add(5 * time.Second)
					for time.Now().Before(deadline) {
						if gates["current@us-west-2"].isStarted() {
							pending = true
							break
						}
						if headerValue(profileHeaderRe, s.pane()) == "current" && headerValue(regionHeaderRe, s.pane()) == "us-west-2" {
							break
						}
						time.Sleep(50 * time.Millisecond)
					}
					if pending && !s.awaitHeader("current", "eu-west-2", 2*time.Second) {
						good = h.check(sub, false, "committed selection moved while region lookup was pending")
					}
				}
				profile, region, key, token, kind, value := "current", "ap-south-2", "currentKEY", "currentTOKEN", "region", "ap-south-2"
				if target == "profile" {
					profile, region, key, token, kind, value = "second", "eu-central-2", "secondKEY", "secondTOKEN", "profile", "second"
				}
				mark := fx.mark()
				if good && !s.choose(kind, value) {
					good = h.check(sub, false, "%s picker did not open", kind)
				}
				if good {
					good = s.awaitHeader(profile, region, 15*time.Second) && s.awaitRequest(mark, signedRequest{key, region, token}, 15*time.Second)
					h.check(sub, good, "superseding %s choice did not commit", target)
				}
				gates["current@us-west-2"].open()
				if good && pending && !awaitGateFinished(gates["current@us-west-2"]) {
					good = h.check(sub, false, "superseded region lookup never finished")
				}
				if good && !requireStable(s, profile, region, "us-west-2", 700*time.Millisecond) {
					// The obsolete string is the superseded region; it may
					// legitimately appear in the open picker's list, which is
					// closed by now. Only the header/stability matters here.
					good = s.awaitHeader(profile, region, 2*time.Second)
					h.check(sub, good, "selection unstable after superseded region lookup")
				}
				h.check(sub, good, "pending-region sequence failed (%s, old_failure=%t)", target, oldFailure)
				all = all && good
			}()
		}
	}
	h.check(viewPkg+"TestAWSPendingRegionCanBeSuperseded", all, "a pending-region subcase failed")
}
