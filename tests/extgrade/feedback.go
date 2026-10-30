package main

import (
	"strings"
	"time"
)

// hasNewNamedText compares rendered lines rather than matching an error
// vocabulary. Existing picker entries and the working profile header are not
// evidence of an error; changed lines may wrap the relevant name.
func hasNewNamedText(before, pane, value string) bool {
	baseline := map[string]bool{}
	for _, line := range strings.Split(before, "\n") {
		baseline[line] = true
	}
	var changed strings.Builder
	for _, line := range strings.Split(pane, "\n") {
		if !baseline[line] {
			changed.WriteString(line)
		}
	}
	return strings.Contains(normalize(changed.String()), normalize(value))
}

// Include selector rows exposed by opening the picker in the baseline, but
// preserve feedback first shown during discovery on open. A whole-pane
// baseline taken after opening would swallow that legitimate error.
func selectionBaseline(before, choice string) string {
	var baseline strings.Builder
	baseline.WriteString(before)
	for i, line := range strings.Split(choice, "\n") {
		item := lineItem(line, i)
		if item == "" {
			continue
		}
		bare := strings.Trim(line, " \t│┃║") == item
		if bare || (i == 1 && overlayRowRe.MatchString(line)) {
			baseline.WriteByte('\n')
			baseline.WriteString(line)
		}
	}
	return baseline.String()
}

func (s *session) awaitNewText(before, value string, dur time.Duration) bool {
	baseline := selectionBaseline(before, s.beforeChoice)
	return s.waitFor(dur, func(pane string) bool {
		return hasNewNamedText(baseline, pane, value)
	})
}

// A flash/status message needs no confirmation. Sending Enter to it can
// reopen the focused dropdown and accidentally make a second selection.
func (s *session) dismissFeedback() {
	if strings.Contains(s.pane(), "Dismiss") {
		s.key("Enter")
	} else if s.listOpen("profile") || s.listOpen("region") {
		s.key("Escape")
	}
	time.Sleep(300 * time.Millisecond)
}
