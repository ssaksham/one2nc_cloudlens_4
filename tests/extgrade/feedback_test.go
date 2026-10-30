package main

import "testing"

func TestNewNamedFeedback(t *testing.T) {
	before := "Profile: first\nRegion: us-east-1\n    slow    \n    newer    \n"
	cases := []struct {
		name  string
		pane  string
		value string
		want  bool
	}{
		{"unchanged picker entry", before, "slow", false},
		{"retained profile header", before + "loading\n", "first", false},
		{"new feedback without fixed vocabulary", before + "could not load role slow\n", "slow", true},
		{"wrapped name", before + "could not load role sl\n│ ow │\n", "slow", true},
		{"new error names the obsolete choice", "Profile: first\nRegion: us-east-1\nerror: slow\n", "slow", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasNewNamedText(before, tc.pane, tc.value); got != tc.want {
				t.Fatalf("new feedback = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestSelectionBaselineKeepsDiscoveryError(t *testing.T) {
	before := "Profile: first\nRegion: us-east-1\n"
	choice := before + "    second    \n    first    \ncould not read config\n"
	baseline := selectionBaseline(before, choice)
	if hasNewNamedText(baseline, choice, "second") {
		t.Fatal("picker option counted as an error")
	}
	if !hasNewNamedText(baseline, choice, "config") {
		t.Fatal("discovery error shown during open was lost")
	}
}
