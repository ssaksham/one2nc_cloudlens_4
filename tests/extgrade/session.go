package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

var sessionSeq int

// session is one running cloudlens program in its own tmux server, with its
// own home directory holding the AWS shared files.
type session struct {
	h    *harness
	name string
	home string
	fx   *fixture

	credentials string
	config      string
}

func (h *harness) newHome() (string, error) {
	sessionSeq++
	dir := filepath.Join(h.work, fmt.Sprintf("home-%d", sessionSeq))
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return "", err
	}
	if h.runner != "" {
		_ = exec.Command("chown", "-R", h.runner, dir).Run()
	}
	return dir, nil
}

func writeFile(path, contents string) error {
	if contents == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	// The child may run as an unprivileged user under a root-owned harness, so
	// files must stay readable by it; MkdirAll above uses 0o777 for the same
	// reason. WriteFile ignores the mode for existing files, hence the chmod.
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// start boots the program with the given AWS file contents and CLI args.
// env overlays the default fixture environment; an empty value leaves the key
// unset. When useHomeDefaults is set, the shared files live under ~/.aws and
// no path overrides are passed.
func (h *harness) start(fx *fixture, credentials, config string, args []string, env map[string]string, useHomeDefaults bool) (*session, error) {
	home, err := h.newHome()
	if err != nil {
		return nil, err
	}
	s := &session{h: h, name: fmt.Sprintf("xg%d", sessionSeq), home: home, fx: fx, credentials: filepath.Join(home, "credentials"), config: filepath.Join(home, "config")}
	if useHomeDefaults {
		s.credentials = filepath.Join(home, ".aws", "credentials")
		s.config = filepath.Join(home, ".aws", "config")
	}
	if err := writeFile(s.credentials, credentials); err != nil {
		return nil, err
	}
	if err := writeFile(s.config, config); err != nil {
		return nil, err
	}
	vars := map[string]string{
		"PATH":                      "/usr/local/go/bin:/usr/bin:/bin",
		"TERM":                      "xterm-256color",
		"HOME":                      home,
		"AWS_EC2_METADATA_DISABLED": "true",
		"AWS_MAX_ATTEMPTS":          "1",
		"AWS_ENDPOINT_URL":          fx.url,
		"AWS_ENDPOINT_URL_STS":      fx.url,
	}
	if !useHomeDefaults {
		vars["AWS_SHARED_CREDENTIALS_FILE"] = s.credentials
		vars["AWS_CONFIG_FILE"] = s.config
	}
	for key, value := range env {
		if value == "" {
			delete(vars, key)
		} else {
			vars[key] = value
		}
	}
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var cmd strings.Builder
	cmd.WriteString("cd " + quote(home) + " && ")
	if h.runner != "" {
		cmd.WriteString("runuser -u " + h.runner + " -- ")
	}
	cmd.WriteString("env -i")
	for _, key := range keys {
		cmd.WriteString(" " + key + "=" + quote(vars[key]))
	}
	cmd.WriteString(" " + quote(h.bin) + " aws")
	for _, arg := range args {
		cmd.WriteString(" " + quote(arg))
	}
	cmd.WriteString("; echo __EXIT__$?; sleep 3600")
	if os.Getenv("EXTGRADE_DEBUG") != "" {
		fmt.Printf("[debug] session %s cmd: %s\n", s.name, cmd.String())
	}
	tmux := exec.Command("tmux", "-L", s.name, "new-session", "-d", "-s", s.name, "-x", "160", "-y", "48", cmd.String())
	if out, err := tmux.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("start tmux: %v: %s", err, out)
	}
	return s, nil
}

func (s *session) close() {
	_ = exec.Command("tmux", "-L", s.name, "kill-server").Run()
}

func (s *session) rewrite(credentials, config string) {
	if credentials != "" {
		_ = writeFile(s.credentials, credentials)
	}
	if config != "" {
		_ = writeFile(s.config, config)
	}
}

func (s *session) pane() string {
	out, err := exec.Command("tmux", "-L", s.name, "capture-pane", "-t", s.name, "-p").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func (s *session) sendKeys(args ...string) {
	full := append([]string{"-L", s.name, "send-keys", "-t", s.name}, args...)
	_ = exec.Command("tmux", full...).Run()
}

func (s *session) key(name string) { s.sendKeys(name) }

func (s *session) typeText(text string) {
	for _, r := range text {
		s.sendKeys("-l", string(r))
		time.Sleep(25 * time.Millisecond)
	}
}

func (s *session) waitFor(dur time.Duration, fn func(pane string) bool) bool {
	deadline := time.Now().Add(dur)
	for {
		if fn(s.pane()) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

var (
	profileHeaderRe = regexp.MustCompile(`Profile:[^A-Za-z0-9]*([A-Za-z0-9][A-Za-z0-9.+_-]*)`)
	regionHeaderRe  = regexp.MustCompile(`Region:[^A-Za-z0-9]*([A-Za-z0-9][A-Za-z0-9.+_-]*)`)
	itemRowRe       = regexp.MustCompile(`^(?:\s{4,}|│\s{4,})([A-Za-z0-9][A-Za-z0-9.+_-]*)\s`)
	overlayRowRe    = regexp.MustCompile(`^ (?:Profile|Region):\s{2,}([A-Za-z0-9][A-Za-z0-9.+_-]*)\s`)
	regionNameRe    = regexp.MustCompile(`^[a-z]{2,3}-[a-z-]+-[0-9][a-z]?$`)
)

func headerValue(re *regexp.Regexp, pane string) string {
	if m := re.FindStringSubmatch(pane); m != nil {
		return m[1]
	}
	return ""
}

// itemTokens returns the first token of each indented row below the first
// header row, in row order: the visible option rows of an open dropdown.
func itemTokens(pane string) []string {
	var out []string
	for i, line := range strings.Split(pane, "\n") {
		if i == 1 {
			// An open list draws its first item over the second header row.
			if m := overlayRowRe.FindStringSubmatch(line); m != nil {
				out = append(out, m[1])
			}
			continue
		}
		if i < 1 {
			continue
		}
		if m := itemRowRe.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
			continue
		}
		// Items drawn over the table header row share it with cell text.
		for _, field := range strings.Fields(line) {
			if regionNameRe.MatchString(field) {
				out = append(out, field)
			}
		}
	}
	return out
}

func normalize(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, text)
}

func (s *session) awaitHeader(profile, region string, dur time.Duration) bool {
	return s.waitFor(dur, func(pane string) bool {
		return headerValue(profileHeaderRe, pane) == profile && headerValue(regionHeaderRe, pane) == region
	})
}

func (s *session) awaitText(value string, dur time.Duration) bool {
	want := normalize(value)
	return s.waitFor(dur, func(pane string) bool {
		return strings.Contains(normalize(pane), want)
	})
}

func (s *session) awaitRequest(mark int, want signedRequest, dur time.Duration) bool {
	deadline := time.Now().Add(dur)
	for {
		if s.fx.hasSince(mark, want) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// awaitState waits until the header shows profile/region and the fixture has
// seen a service request signed with key in region since mark.
func (s *session) awaitState(id string, mark int, profile, region, key, token string) bool {
	deadline := 20 * time.Second
	okHeader := s.awaitHeader(profile, region, deadline)
	okReq := s.awaitRequest(mark, signedRequest{key, region, token}, deadline)
	return s.h.check(id, okHeader && okReq, "header/request for %s/%s with key %s not observed (header ok=%v, request ok=%v)", profile, region, key, okHeader, okReq)
}

// settleRegionRetry picks region repeatedly until the header confirms it.
func (s *session) settleRegionRetry(profile, region string) bool {
	return s.settleRegion(profile, region)
}

// waitHeader waits without failing and reports whether the header shows
// profile in region.
func (s *session) waitHeader(profile, region string, dur time.Duration) bool {
	return s.awaitHeader(profile, region, dur)
}

// settleRegion drives the session onto region, retrying the whole
// open-and-pick sequence when a click lands on a stale row.
func (s *session) settleRegion(profile, region string) bool {
	for i := 0; i < 3; i++ {
		if s.waitHeader(profile, region, 300*time.Millisecond) {
			return true
		}
		if !s.choose("region", region) {
			continue
		}
		if s.waitHeader(profile, region, 6*time.Second) {
			return true
		}
	}
	return s.waitHeader(profile, region, 300*time.Millisecond)
}

func (s *session) listOpen(kind string) bool {
	pane := s.pane()
	if !strings.Contains(pane, "Profile:") {
		return false // still on the splash screen
	}
	tokens := itemTokens(pane)
	if kind == "region" {
		count := 0
		for _, tok := range tokens {
			if regionNameRe.MatchString(tok) {
				count++
			}
		}
		return count >= 3
	}
	// A profile list is open when some visible item is not a region name;
	// region items alone mean the wrong dropdown opened.
	for _, tok := range tokens {
		if !regionNameRe.MatchString(tok) {
			return true
		}
	}
	return false
}

// openList opens the named dropdown's list. Clicking the field itself opens
// its list deterministically; Tab cycling is kept as a fallback because a
// leading Enter or Escape disturbs the page stack, so focus moves first and
// Escape is only sent to close a list that is actually open.
func (s *session) openList(kind string) bool {
	// Do not burn attempts on the splash screen.
	s.waitFor(15*time.Second, func(pane string) bool { return strings.Contains(pane, "Profile:") })
	fieldY := 1 // Profile field is the first pane row.
	if kind == "region" {
		fieldY = 2 // Region field is the second pane row.
	}
	if kind == "profile" {
		// The profile picker refreshes its options through its keyboard
		// input handler, so open it from the keyboard.
		for attempt := 0; attempt < 6; attempt++ {
			s.key("Tab")
			time.Sleep(200 * time.Millisecond)
			s.key("Enter")
			if s.waitFor(900*time.Millisecond, func(string) bool { return s.listOpen(kind) }) {
				return true
			}
			if len(itemTokens(s.pane())) > 0 {
				s.key("Escape")
				time.Sleep(150 * time.Millisecond)
			}
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		s.click(15, fieldY)
		if s.waitFor(900*time.Millisecond, func(string) bool { return s.listOpen(kind) }) {
			return true
		}
		if os.Getenv("EXTGRADE_DEBUG") != "" {
			fmt.Printf("  [debug] open %s click %d tokens=%v\n", kind, attempt, itemTokens(s.pane()))
		}
		if len(itemTokens(s.pane())) > 0 {
			s.key("Escape")
			time.Sleep(150 * time.Millisecond)
		}
	}
	for attempt := 0; attempt < 6; attempt++ {
		s.key("Tab")
		time.Sleep(200 * time.Millisecond)
		s.key("Enter")
		if s.waitFor(900*time.Millisecond, func(string) bool { return s.listOpen(kind) }) {
			return true
		}
		if os.Getenv("EXTGRADE_DEBUG") != "" {
			lines := strings.Split(s.pane(), "\n")
			for i := 0; i < len(lines) && i < 3; i++ {
				fmt.Printf("  [debug] open %s attempt %d | %s\n", kind, attempt, strings.TrimRight(lines[i], " "))
			}
		}
		if len(itemTokens(s.pane())) > 0 {
			s.key("Escape")
			time.Sleep(150 * time.Millisecond)
		}
	}
	fmt.Printf("  [open] %s list did not open; pane:\n%s\n", kind, s.pane())
	return false
}

// lineItem returns the dropdown item drawn on a pane line, if any.
func lineItem(line string, index int) string {
	if index == 1 {
		if m := overlayRowRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
		return ""
	}
	if index < 1 {
		return ""
	}
	if m := itemRowRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// mouse sends an SGR mouse event (button, 1-based coordinates) to the pane.
func (s *session) mouse(button, x, y int, release bool) {
	suffix := "M"
	if release {
		suffix = "m"
	}
	seq := fmt.Sprintf("\x1b[<%d;%d;%d%s", button, x, y, suffix)
	hexArgs := make([]string, 0, len(seq))
	for i := 0; i < len(seq); i++ {
		hexArgs = append(hexArgs, fmt.Sprintf("%02x", seq[i]))
	}
	args := append([]string{"-L", s.name, "send-keys", "-t", s.name, "-H"}, hexArgs...)
	_ = exec.Command("tmux", args...).Run()
}

func (s *session) click(x, y int) {
	s.mouse(0, x, y, false)
	time.Sleep(80 * time.Millisecond)
	s.mouse(0, x, y, true)
}

// choose opens the named dropdown and commits the target item. Mouse item
// clicks are unreliable on the region list in this tview version (press,
// release, double-click, and move cycles can resurface the field's old
// current item on flush), so regions are committed with the keyboard: Down
// moves the list's current item and Enter selects it, with item order
// learned from the rendered list. The profile list, in contrast, refreshes
// its options on keyboard input, which resets its current item, so profiles
// are committed by clicking the drawn row.
func (s *session) choose(kind, value string) bool {
	if !s.openList(kind) {
		return false
	}
	if kind == "profile" {
		return s.chooseByClick(kind, value)
	}
	if !s.listOpen(kind) {
		return true // the click that opened the list also committed it
	}
	// The list opens scrolled to the top with its first item current, so
	// learn the full order once, then step down to the target.
	items := s.collectItemsInPlace(kind)
	index := -1
	for i, item := range items {
		if item == value {
			index = i
			break
		}
	}
	if index < 0 {
		s.key("Escape")
		fmt.Printf("  [choose] %q not among %s items %v\n", value, kind, items)
		return false
	}
	// A load finishing mid-sequence can steal focus and close the list, so
	// verify the pick afterwards and retry when it did not take. The pick
	// counts as entered when the field shows the value or a new service
	// request shows a load started (its commit may legitimately be held by
	// a slow credential lookup).
	for attempt := 0; attempt < 3; attempt++ {
		if s.currentValue(kind) == value {
			return true
		}
		if !s.listOpen(kind) {
			if !s.openList(kind) {
				return false
			}
		} else if attempt > 0 {
			// Reopen so the highlight and scroll reset before retrying.
			s.key("Escape")
			time.Sleep(150 * time.Millisecond)
			if !s.openList(kind) {
				return false
			}
		}
		s.key("Home")
		time.Sleep(100 * time.Millisecond)
		for i := 0; i < index; i++ {
			s.key("Down")
			time.Sleep(40 * time.Millisecond)
		}
		mark := 0
		if s.fx != nil {
			mark = s.fx.mark()
		}
		s.key("Enter")
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if s.currentValue(kind) == value {
				return true
			}
			if s.fx != nil && s.fx.mark() > mark {
				return true
			}
			// A failed pick shows the error dialog instead of committing.
			if strings.Contains(s.pane(), "Dismiss") {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return s.currentValue(kind) == value
}

// collectItemsInPlace unions the visible items of the open list across
// key-driven scrolls, preserving list order, and leaves the list open with
// the highlight moved to the last item.
func (s *session) collectItemsInPlace(kind string) []string {
	var out []string
	seen := map[string]bool{}
	stale := 0
	for i := 0; i < 40 && stale < 3; i++ {
		before := len(out)
		for _, tok := range s.visibleItems(kind) {
			if !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
		}
		if len(out) == before {
			stale++
		} else {
			stale = 0
		}
		s.key("Down")
		time.Sleep(60 * time.Millisecond)
	}
	return out
}

// chooseByClick clicks the row displaying value in the open list. Items are
// matched as whitespace-separated fields because the list overlays the
// table, whose header cells share the row; the dropdown's own field row is
// skipped since it displays the current value rather than an item.
func (s *session) chooseByClick(kind, value string) bool {
	fieldRow := 0 // Profile field row.
	find := func() bool {
		lines := strings.Split(s.pane(), "\n")
		for i, line := range lines {
			if i == fieldRow {
				continue
			}
			for _, field := range strings.Fields(line) {
				if field == value {
					s.click(15, i+1)
					return true
				}
			}
		}
		return false
	}
	for scroll := 0; scroll < 12; scroll++ {
		if find() {
			return true
		}
		s.mouse(64, 15, 6, false)
		time.Sleep(250 * time.Millisecond)
	}
	for scroll := 0; scroll < 12; scroll++ {
		if find() {
			return true
		}
		s.mouse(65, 15, 6, false)
		time.Sleep(250 * time.Millisecond)
	}
	s.key("Escape")
	fmt.Printf("  [choose] %q not visible among %s items\n", value, kind)
	return false
}

// visibleItems returns the items of the currently open list in drawn order.
func (s *session) visibleItems(kind string) []string {
	var out []string
	add := func(tok string) {
		if kind == "region" && !regionNameRe.MatchString(tok) {
			return
		}
		if kind == "profile" && regionNameRe.MatchString(tok) {
			return
		}
		out = append(out, tok)
	}
	for _, tok := range itemTokens(s.pane()) {
		add(tok)
	}
	return out
}

// currentValue is the value shown in the named dropdown's field.
func (s *session) currentValue(kind string) string {
	re := regionHeaderRe
	if kind == "profile" {
		re = profileHeaderRe
	}
	return headerValue(re, s.pane())
}

// collectItems unions the visible items of an open dropdown across scrolls.
func (s *session) collectItems(kind string) []string {
	seen := map[string]bool{}
	var out []string
	add := func() {
		for _, tok := range itemTokens(s.pane()) {
			if kind == "region" && !regionNameRe.MatchString(tok) {
				continue
			}
			if !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
		}
	}
	add()
	stale := 0
	for i := 0; i < 16 && stale < 4; i++ {
		before := len(out)
		s.key("Down")
		s.key("Down")
		s.key("Down")
		time.Sleep(120 * time.Millisecond)
		add()
		if len(out) == before {
			stale++
		} else {
			stale = 0
		}
	}
	s.key("Escape")
	return out
}

func execOutput(name string, argv []string, dir string) (string, int) {
	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() {
		defer close(done)
		cmd := exec.Command(name, argv...)
		cmd.Dir = dir
		out, runErr = cmd.CombinedOutput()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		return string(out) + "\n(timeout)", -1
	}
	code := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return string(out), code
}

// runDirect runs the program without a terminal and returns its combined
// output and exit code; used for startup failures, which must be CLI errors.
func (h *harness) runDirect(fx *fixture, credentials, config string, args []string, env map[string]string) (string, int) {
	home, err := h.newHome()
	if err != nil {
		return err.Error(), -1
	}
	credPath, cfgPath := filepath.Join(home, "credentials"), filepath.Join(home, "config")
	_ = writeFile(credPath, credentials)
	_ = writeFile(cfgPath, config)
	vars := map[string]string{
		"PATH": "/usr/bin:/bin", "TERM": "xterm-256color", "HOME": home,
		"AWS_EC2_METADATA_DISABLED": "true", "AWS_MAX_ATTEMPTS": "1",
		"AWS_ENDPOINT_URL": fx.url, "AWS_ENDPOINT_URL_STS": fx.url,
		"AWS_SHARED_CREDENTIALS_FILE": credPath, "AWS_CONFIG_FILE": cfgPath,
	}
	for key, value := range env {
		if value == "" {
			delete(vars, key)
		} else {
			vars[key] = value
		}
	}
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	argv := []string{"env", "-i"}
	for _, key := range keys {
		argv = append(argv, key+"="+vars[key])
	}
	argv = append(argv, h.bin, "aws")
	argv = append(argv, args...)
	name := "env"
	if h.runner != "" {
		name = "runuser"
		argv = append([]string{"-u", h.runner, "--"}, argv...)
	}
	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() {
		defer close(done)
		cmd := exec.Command(name, argv...)
		cmd.Dir = home
		out, runErr = cmd.CombinedOutput()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		return string(out) + "\n(timeout)", -1
	}
	code := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return string(out), code
}
