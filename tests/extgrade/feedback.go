package main

import (
	"strings"
	"time"
	"unicode/utf8"
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

// A flash/status message needs no confirmation. An error dialog is dismissed
// by clicking its button, which works whether or not the dialog took focus:
// Enter sent to a dialog without focus would reach the focused dropdown and
// open it instead. Enter is only a fallback for a dialog that ignores clicks.
func (s *session) dismissFeedback() {
	gone := func(pane string) bool { return !strings.Contains(pane, "Dismiss") }
	for row, line := range strings.Split(s.pane(), "\n") {
		if col := strings.Index(line, "Dismiss"); col >= 0 {
			// tview reports a click within 500ms of the previous one as a
			// double-click, which buttons ignore.
			time.Sleep(600 * time.Millisecond)
			s.click(utf8.RuneCountInString(line[:col])+4, row+1)
			if !s.waitFor(time.Second, gone) {
				s.key("Enter")
				s.waitFor(time.Second, gone)
			}
			break
		}
	}
	for _, kind := range []string{"profile", "region"} {
		if s.listOpen(kind) {
			s.key("Escape")
			s.awaitListState(kind, false, 2*time.Second)
		}
	}
	time.Sleep(300 * time.Millisecond)
}
