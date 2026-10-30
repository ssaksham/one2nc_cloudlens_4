package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	cmdPkg  = "github.com/one2nc/cloudlens/cmd."
	awsPkg  = "github.com/one2nc/cloudlens/internal/aws."
	viewPkg = "github.com/one2nc/cloudlens/internal/view."
)

func profileKeys(names ...string) string {
	var b strings.Builder
	for _, name := range names {
		b.WriteString("[" + name + "]\naws_access_key_id = " + name + "KEY\naws_secret_access_key = secret\n")
	}
	return b.String()
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := sortedCopy(a), sortedCopy(b)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func runScenarios(h *harness) {
	groups := []struct {
		name string
		run  func(*harness)
	}{
		{"discovery", discoveryScenarios},
		{"regions catalog", regionCatalogScenarios},
		{"command errors", commandErrorScenarios},
		{"initialization errors", initErrorScenarios},
		{"startup", startupScenarios},
		{"switch precedence", precedenceScenarios},
		{"failed switch", failedSwitchScenarios},
		{"localstack", localstackScenarios},
		{"async supersede", asyncScenarios},
		{"live files and remembered regions", liveScenarios},
	}
	for _, group := range groups {
		if !h.run(group.name, group.run) {
			return
		}
	}
}

// pickerItems opens the profile dropdown and returns its visible items.
func pickerItems(s *session) ([]string, bool) {
	if !s.openList("profile") {
		return nil, false
	}
	return s.collectItems("profile"), true
}

func discoveryCase(h *harness, id, credentials, config string, want []string, forbidden []string, useHome bool) {
	discoveryCaseStart(h, id, credentials, config, want, forbidden, useHome, "")
}

// startProfile names a credentials-bearing profile to boot on when want[0]
// has no usable credentials of its own; the picker contents are judged.
func discoveryCaseStart(h *harness, id, credentials, config string, want []string, forbidden []string, useHome bool, startProfile string) {
	fx := newFixture(nil)
	defer fx.close()
	args := []string{}
	if startProfile != "" {
		args = []string{"-p", startProfile}
	} else if len(want) > 0 {
		args = []string{"-p", want[0]}
	}
	s, err := h.start(fx, credentials, config, args, nil, useHome)
	if err != nil {
		h.check(id, false, "start: %v", err)
		return
	}
	defer s.close()
	if len(want) > 0 {
		boot := startProfile
		if boot == "" {
			boot = want[0]
		}
		s.awaitHeader(boot, "", 15*time.Second)
	}
	items, ok := pickerItems(s)
	if !ok {
		h.check(id, false, "profile picker did not open; pane:\n%s", s.pane())
		return
	}
	got := filterCandidates(items, append(append([]string{}, want...), forbidden...))
	if !sameSet(got, want) {
		h.check(id, false, "picker items %v, want set %v", items, want)
		return
	}
	for _, name := range forbidden {
		for _, tok := range items {
			if tok == name {
				h.check(id, false, "picker lists forbidden name %q in %v", name, items)
				return
			}
		}
	}
	h.verdict(id, true, "")
}

func wantFirst(want []string, fallback string) string {
	if len(want) > 0 {
		return sortedCopy(want)[0]
	}
	return fallback
}

func filterCandidates(items, candidates []string) []string {
	set := map[string]bool{}
	for _, c := range candidates {
		set[c] = true
	}
	var out []string
	for _, tok := range items {
		if set[tok] {
			out = append(out, tok)
		}
	}
	return out
}

func tail(pane string) string {
	lines := strings.Split(pane, "\n")
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	return strings.Join(lines, "\n")
}

func discoveryScenarios(h *harness) {
	discoveryCaseStart(h, awsPkg+"TestGetProfilesCombinesCredentialsAndConfig",
		"[source]\naws_access_key_id = source-key\naws_secret_access_key = source-secret\n[shared]\naws_access_key_id = shared-key\naws_secret_access_key = shared-secret\n",
		"[profile sandbox-dev]\nrole_arn = arn:aws:iam::123456789012:role/Developer\nsource_profile = source\n[profile shared]\nregion = eu-central-2\n[profile office-sso]\nsso_session = company\nsso_account_id = 123456789012\nsso_role_name = Developer\n[sso-session company]\nsso_start_url = https://example.invalid/start\nsso_region = us-east-1\n",
		[]string{"office-sso", "sandbox-dev", "shared", "source"}, []string{"company"}, false, "source")

	fx := newFixture(nil)
	defer fx.close()
	s, err := h.start(fx, "[zebra]\naws_access_key_id = z\naws_secret_access_key = s\n[alpha]\naws_access_key_id = a\naws_secret_access_key = s\n[default]\naws_access_key_id = d\naws_secret_access_key = s\n", "", []string{"-p", "alpha"}, nil, false)
	if err == nil {
		defer s.close()
		items, ok := pickerItems(s)
		want := []string{"alpha", "default", "zebra"}
		got := filterCandidates(items, want)
		h.verdict(awsPkg+"TestGetProfilesSortsCredentialNames", ok && strings.Join(got, ",") == strings.Join(want, ","), "picker order %v, want %v", items, want)
	} else {
		h.check(awsPkg+"TestGetProfilesSortsCredentialNames", false, "start: %v", err)
	}

	discoveryCaseStart(h, awsPkg+"TestGetProfilesPreservesCase",
		"[Team]\naws_access_key_id = capital\naws_secret_access_key = secret\n[team]\naws_access_key_id = lowercase\naws_secret_access_key = secret\n",
		"[profile Team]\nregion = eu-west-1\n[profile TEAM]\nregion = us-west-2\n",
		[]string{"TEAM", "Team", "team"}, nil, false, "team")

	// A credentials-bearing starter profile joins the expected list: the app
	// cannot boot on an empty profile, and listing empty sections is what is
	// judged.
	discoveryCaseStart(h, awsPkg+"TestGetProfilesKeepsEmptyNamedSections",
		"[empty-credentials]\n[default]\n[starter]\naws_access_key_id = starter-key\naws_secret_access_key = secret\n",
		"[profile empty-config]\n[default]\n",
		[]string{"default", "empty-config", "empty-credentials", "starter"}, nil, false, "starter")

	discoveryCaseStart(h, awsPkg+"TestGetProfilesIgnoresUnsectionedAndConfigMetadata",
		"aws_access_key_id = unsectioned\naws_secret_access_key = unsectioned\n[person]\naws_access_key_id = person-key\naws_secret_access_key = secret\n",
		"region = us-east-1\n[default]\nregion = us-west-2\n[profile role]\nsource_profile = person\nrole_arn = arn:aws:iam::123456789012:role/Developer\n[sso-session company]\nsso_region = us-east-1\n[services local]\ns3 =\n endpoint_url = http://localhost:4566\n[unrelated]\nregion = eu-west-1\n",
		[]string{"default", "person", "role"}, []string{"company", "local", "unrelated"}, false, "person")

	discoveryCaseStart(h, awsPkg+"TestGetProfilesConfigOnlyFiltersSections",
		"[engineering]\naws_access_key_id = engineering-key\naws_secret_access_key = secret\n",
		"[default]\nregion = us-east-1\n[profile engineering]\nregion = eu-central-2\n[profile role]\nsource_profile = engineering\nrole_arn = arn:aws:iam::123456789012:role/Developer\n[profile office-sso]\nsso_session = work\nsso_account_id = 123456789012\nsso_role_name = Developer\n[unrelated]\nvalue = ignored\n[sso-session work]\nsso_start_url = https://example.invalid/start\nsso_region = us-east-1\n",
		[]string{"default", "engineering", "office-sso", "role"}, []string{"work", "unrelated"}, false, "engineering")

	// Overrides: harness start already points both overrides at the case
	// files; home defaults with different names must not appear.
	fx2 := newFixture(nil)
	defer fx2.close()
	s2, err := h.start(fx2, "[override-credentials]\naws_access_key_id = selected\naws_secret_access_key = secret\n", "[profile override-config]\nregion = eu-central-2\n", []string{"-p", "override-credentials"}, nil, false)
	if err == nil {
		defer s2.close()
		_ = writeFile(filepath.Join(s2.home, ".aws", "credentials"), "[home-credentials]\naws_access_key_id = ignored\n")
		_ = writeFile(filepath.Join(s2.home, ".aws", "config"), "[profile home-config]\nregion = us-east-1\n")
		s2.awaitHeader("override-credentials", "", 15*time.Second)
		items, ok := pickerItems(s2)
		want := []string{"override-config", "override-credentials"}
		got := filterCandidates(items, append(want, "home-config", "home-credentials"))
		h.verdict(awsPkg+"TestGetProfilesHonorsBothFileOverrides", ok && sameSet(got, want), "picker opened=%v items %v, want %v; pane:\n%s", ok, items, want, s2.pane())
	} else {
		h.check(awsPkg+"TestGetProfilesHonorsBothFileOverrides", false, "start: %v", err)
	}

	discoveryCaseStart(h, awsPkg+"TestGetProfilesUsesHomeDefaults",
		"[source]\naws_access_key_id = selected\naws_secret_access_key = secret\n",
		"[profile role]\nsource_profile = source\nrole_arn = arn:aws:iam::123456789012:role/Developer\n",
		[]string{"role", "source"}, nil, true, "source")

	// Missing and empty files: the app comes up on the implicit default.
	for _, tc := range []struct {
		id          string
		credentials string
		config      string
		write       bool
	}{
		{awsPkg + "TestGetProfilesMissingFilesAreEmpty", "", "", false},
		{awsPkg + "TestGetProfilesEmptyFilesAreEmpty", "", "", true},
	} {
		fx := newFixture(nil)
		s, err := h.start(fx, "", "", nil, map[string]string{"AWS_ACCESS_KEY_ID": "ENVKEY", "AWS_SECRET_ACCESS_KEY": "envsecret"}, false)
		if tc.write && err == nil {
			_ = writeFile(s.credentials, "")
			_ = writeFile(s.config, "")
		}
		if err != nil {
			h.check(tc.id, false, "start: %v", err)
		} else {
			ok := s.awaitHeader("default", "ap-south-1", 15*time.Second)
			h.verdict(tc.id, ok, "empty discovery did not come up on implicit default; pane:\n%s", tail(s.pane()))
			s.close()
		}
		fx.close()
	}

	discoveryCase(h, awsPkg+"TestGetProfilesCredentialsOnly",
		"[default]\naws_access_key_id = example\naws_secret_access_key = example-secret\n", "",
		[]string{"default"}, nil, false)

	// Malformed and unreadable files must fail startup naming the path.
	malformed := []struct {
		id          string
		credentials string
		config      string
		wantPath    string // "credentials" or "config"
	}{
		{awsPkg + "TestGetProfilesRejectsMalformedCredentials", "[broken\naws_access_key_id = invalid\n", "[profile usable]\nregion = us-east-1\n", "credentials"},
		{awsPkg + "TestGetProfilesRejectsMalformedConfig", "[usable]\naws_access_key_id = example\naws_secret_access_key = secret\n", "[profile broken\nregion = us-east-1\n", "config"},
	}
	for _, tc := range malformed {
		fx := newFixture(nil)
		out, code := h.runDirect(fx, tc.credentials, tc.config, nil, nil)
		h.verdict(tc.id, code != 0 && strings.Contains(out, tc.wantPath) && !strings.Contains(out, "panic:"), "exit=%d output:\n%s", code, tail(out))
		fx.close()
	}
	unreadable := []struct {
		id       string
		dirName  string
		other    string
		otherIs  string
		wantPath string
	}{
		{awsPkg + "TestGetProfilesRejectsUnreadableCredentials", "credentials", "[profile usable]\nregion = us-east-1\n", "config", "credentials"},
		{awsPkg + "TestGetProfilesRejectsUnreadableConfig", "config", "[usable]\naws_access_key_id = example\naws_secret_access_key = secret\n", "credentials", "config"},
	}
	for _, tc := range unreadable {
		fx := newFixture(nil)
		home, herr := h.newHome()
		if herr != nil {
			h.check(tc.id, false, "%v", herr)
			continue
		}
		_ = os.Mkdir(filepath.Join(home, tc.dirName), 0o700)
		_ = writeFile(filepath.Join(home, tc.otherIs), tc.other)
		out, code := h.runDirectIn(home, fx)
		h.verdict(tc.id, code != 0 && strings.Contains(out, tc.wantPath), "exit=%d output:\n%s", code, tail(out))
		fx.close()
	}

	// Listing must not execute credential_process.
	fx3 := newFixture(nil)
	defer fx3.close()
	home, err := h.newHome()
	if err != nil {
		h.check(awsPkg+"TestGetProfilesDoesNotExecuteCredentialProcess", false, "%v", err)
		return
	}
	marker := filepath.Join(home, "credentials-were-executed")
	helper := filepath.Join(home, "credentials-helper")
	script := "#!/bin/sh\nprintf invoked > '" + marker + "'\nprintf '%s' '{\"Version\":1,\"AccessKeyId\":\"PROCESSKEY\",\"SecretAccessKey\":\"secret\"}'\n"
	_ = os.WriteFile(helper, []byte(script), 0o755)
	_ = os.Chmod(helper, 0o755)
	s3, err := h.start(fx3, "[source]\naws_access_key_id=source\naws_secret_access_key=secret\n",
		"[profile external]\ncredential_process="+helper+"\nregion=us-east-1\n", []string{"-p", "source"}, nil, false)
	if err != nil {
		h.check(awsPkg+"TestGetProfilesDoesNotExecuteCredentialProcess", false, "start: %v", err)
		return
	}
	defer s3.close()
	items, ok := pickerItems(s3)
	_, statErr := os.Stat(marker)
	h.verdict(awsPkg+"TestGetProfilesDoesNotExecuteCredentialProcess",
		ok && sameSet(filterCandidates(items, []string{"external", "source"}), []string{"external", "source"}) && os.IsNotExist(statErr),
		"picker %v, credential_process marker stat err=%v", items, statErr)
}

// runDirectIn runs the program in an existing home (for directory-as-file
// cases runDirect cannot express).
func (h *harness) runDirectIn(home string, fx *fixture) (string, int) {
	credPath, cfgPath := filepath.Join(home, "credentials"), filepath.Join(home, "config")
	vars := map[string]string{
		"PATH": "/usr/bin:/bin", "TERM": "xterm-256color", "HOME": home,
		"AWS_EC2_METADATA_DISABLED": "true", "AWS_MAX_ATTEMPTS": "1",
		"AWS_ENDPOINT_URL": fx.url, "AWS_ENDPOINT_URL_STS": fx.url,
		"AWS_SHARED_CREDENTIALS_FILE": credPath, "AWS_CONFIG_FILE": cfgPath,
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
	name := "env"
	if h.runner != "" {
		name = "runuser"
		argv = append([]string{"-u", h.runner, "--"}, argv...)
	}
	return execOutput(name, argv, home)
}

func regionCatalogScenarios(h *harness) {
	fx := newFixture(nil)
	defer fx.close()
	s, err := h.start(fx, profileKeys("dev"), "[profile dev]\nregion=us-east-1\n", []string{"-p", "dev"}, nil, false)
	if err != nil {
		h.check(awsPkg+"TestGetAllRegionsIncludesBundledAWSRegions", false, "start: %v", err)
		h.check(awsPkg+"TestGetAllRegionsIncludesNewerCommercialRegions", false, "start: %v", err)
		return
	}
	defer s.close()
	var items []string
	opened := s.openList("region")
	if opened {
		items = s.collectItems("region")
	}
	set := map[string]bool{}
	for _, item := range items {
		set[item] = true
	}
	samples := []string{"us-east-1", "cn-north-1", "us-gov-west-1", "eu-isoe-west-1", "af-south-1"}
	missing := []string{}
	for _, sample := range samples {
		if !set[sample] {
			missing = append(missing, sample)
		}
	}
	h.verdict(awsPkg+"TestGetAllRegionsIncludesBundledAWSRegions", opened && len(missing) == 0 && len(items) >= 30,
		"region picker collected %d items, missing %v", len(items), missing)
	h.verdict(awsPkg+"TestGetAllRegionsIncludesNewerCommercialRegions", set["eu-central-2"] && set["ap-south-2"],
		"region picker lacks newer commercial regions in %v", items)
}

func commandErrorScenarios(h *harness) {
	files := func() (string, string) {
		return "[dev]\naws_access_key_id=key\naws_secret_access_key=secret\n", "[profile dev]\nregion=moon-central-1\n"
	}
	cases := []struct {
		sub  string
		args []string
		env  map[string]string
		want string
	}{
		{"missing_flag_profile", []string{"-p", "absent"}, nil, "absent"},
		{"missing_environment_profile", nil, map[string]string{"AWS_PROFILE": "absent"}, "absent"},
		{"implicit_profile_without_default", nil, nil, "moon-central-1"},
		{"explicit_default_is_not_implicit", []string{"--profile", "default"}, nil, "default"},
		{"profile_flag_overrides_environment", []string{"-p", "dev"}, map[string]string{"AWS_PROFILE": "absent"}, "moon-central-1"},
		{"region_flag_is_used", []string{"--profile", "dev", "--region", "invalid-region"}, nil, "invalid-region"},
	}
	all := true
	for _, tc := range cases {
		creds, cfg := files()
		fx := newFixture(nil)
		out, code := h.runDirect(fx, creds, cfg, tc.args, tc.env)
		ok := code != 0 && strings.Contains(out, tc.want) && !strings.Contains(out, "panic:")
		h.verdict(cmdPkg+"TestAWSCommandConfigurationErrors/"+tc.sub, ok, "exit=%d output:\n%s", code, tail(out))
		all = all && ok
		fx.close()
	}
	h.verdict(cmdPkg+"TestAWSCommandConfigurationErrors", all, "a command error subcase failed")
}

func initErrorScenarios(h *harness) {
	cases := []struct {
		sub    string
		args   []string
		env    map[string]string
		config string
		want   string
	}{
		{"missing_flag_profile", []string{"-p", "absent"}, nil, "", "absent"},
		{"missing_env_profile", nil, map[string]string{"AWS_PROFILE": "absent"}, "", "absent"},
		{"profile_case_mismatch", []string{"-p", "DEV"}, nil, "", "DEV"},
		{"unknown_flag_region", []string{"-p", "dev", "-r", "moon-central-1"}, nil, "", "moon-central-1"},
		{"unknown_profile_region", []string{"-p", "dev"}, nil, "[profile dev]\nregion=moon-central-1\n", "moon-central-1"},
		{"malformed_config", []string{"-p", "dev"}, nil, "[profile dev\nregion=us-east-1\n", "config"},
		{"missing_source_profile", []string{"-p", "role"}, nil, "[profile role]\nrole_arn=arn:aws:iam::123456789012:role/dev\nsource_profile=absent\nregion=us-east-1\n", "role"},
	}
	all := true
	for _, tc := range cases {
		fx := newFixture(nil)
		out, code := h.runDirect(fx, profileKeys("dev"), tc.config, tc.args, tc.env)
		ok := code != 0 && strings.Contains(strings.ToLower(out), strings.ToLower(tc.want)) && !strings.Contains(out, "panic:")
		h.verdict(viewPkg+"TestAWSInitializationErrors/"+tc.sub, ok, "exit=%d output:\n%s", code, tail(out))
		all = all && ok
		fx.close()
	}
	h.verdict(viewPkg+"TestAWSInitializationErrors", all, "an initialization error subcase failed")
}

type startupCase struct {
	sub, profiles, config   string
	args                    []string
	env                     map[string]string
	wantProfile, wantRegion string
	wantKey                 string
}

func startupScenarios(h *harness) {
	cases := []startupCase{
		{"named_profile_region", profileKeys("dev"), "[profile dev]\nregion=eu-west-2\n", []string{"-p", "dev"}, nil, "dev", "eu-west-2", "devKEY"},
		{"default_profile_region", profileKeys("default", "dev"), "[default]\nregion=eu-west-2\n", nil, nil, "default", "eu-west-2", "defaultKEY"},
		{"implicit_first_without_default", profileKeys("zulu", "alpha"), "[profile alpha]\nregion=eu-central-2\n", nil, nil, "alpha", "eu-central-2", "alphaKEY"},
		{"environment_profile_with_files", profileKeys("default", "dev"), "[profile dev]\nregion=eu-west-2\n", nil, map[string]string{"AWS_PROFILE": "dev"}, "dev", "eu-west-2", "devKEY"},
		{"flag_profile_over_environment", profileKeys("default", "dev"), "[default]\nregion=eu-west-2\n", []string{"-p", "default"}, map[string]string{"AWS_PROFILE": "dev"}, "default", "eu-west-2", "defaultKEY"},
		{"flag_region_over_all_sources", profileKeys("dev"), "[profile dev]\nregion=eu-west-2\n", []string{"-p", "dev", "-r", "ap-south-2"}, map[string]string{"AWS_REGION": "us-west-2", "AWS_DEFAULT_REGION": "us-east-2"}, "dev", "ap-south-2", "devKEY"},
		{"aws_region_over_default_region", profileKeys("dev"), "[profile dev]\nregion=eu-west-2\n", []string{"-p", "dev"}, map[string]string{"AWS_REGION": "us-west-2", "AWS_DEFAULT_REGION": "us-east-2"}, "dev", "us-west-2", "devKEY"},
		{"default_region_over_config", profileKeys("dev"), "[profile dev]\nregion=eu-west-2\n", []string{"-p", "dev"}, map[string]string{"AWS_DEFAULT_REGION": "us-east-2"}, "dev", "us-east-2", "devKEY"},
		{"unsectioned_region_is_not_inherited", profileKeys("dev"), "region=eu-west-1\n[profile dev]\noutput=json\n", []string{"-p", "dev"}, nil, "dev", "ap-south-1", "devKEY"},
		{"missing_region_fallback", profileKeys("dev"), "", []string{"-p", "dev"}, nil, "dev", "ap-south-1", "devKEY"},
		{"case_sensitive_profile_choice", profileKeys("prod", "Prod"), "[profile prod]\nregion=us-east-2\n[profile Prod]\nregion=eu-west-2\n", []string{"-p", "Prod"}, nil, "Prod", "eu-west-2", "ProdKEY"},
		{"config_only_static_profile", "", "[profile solo]\naws_access_key_id=soloKEY\naws_secret_access_key=secret\nregion=eu-central-2\n", []string{"-p", "solo"}, nil, "solo", "eu-central-2", "soloKEY"},
	}
	all := true
	for _, tc := range cases {
		id := viewPkg + "TestAWSProfileStartup/" + tc.sub
		fx := newFixture(nil)
		s, err := h.start(fx, tc.profiles, tc.config, tc.args, tc.env, false)
		if err != nil {
			h.check(id, false, "start: %v", err)
			all = false
			fx.close()
			continue
		}
		ok := s.awaitState(id, 0, tc.wantProfile, tc.wantRegion, tc.wantKey, "")
		h.verdict(id, ok, "startup did not reach the expected profile session")
		all = all && ok
		s.close()
		fx.close()
	}
	h.verdict(viewPkg+"TestAWSProfileStartup", all, "a startup subcase failed")

	// Role profile from config only (STS ungated in the fixture).
	fx := newFixture(nil)
	defer fx.close()
	s, err := h.start(fx, profileKeys("source"), "[profile sandbox-dev]\nrole_arn=arn:aws:iam::123456789012:role/dev\nsource_profile=source\nregion=eu-central-2\n", []string{"-p", "sandbox-dev"}, nil, false)
	if err != nil {
		h.check(viewPkg+"TestAWSRoleProfileFromConfig", false, "start: %v", err)
	} else {
		defer s.close()
		id := viewPkg + "TestAWSRoleProfileFromConfig"
		ok := s.awaitState(id, 0, "sandbox-dev", "eu-central-2", "ROLEKEY", "roletoken")
		h.verdict(id, ok, "role profile did not reach the expected session")
	}

	// Environment-only startup, then a hand-picked region keeps the token.
	envAll := true
	for _, region := range []string{"", "us-west-2"} {
		sub := viewPkg + "TestAWSEnvironmentOnlyStartup/region_" + region
		want := region
		if want == "" {
			want = "ap-south-1"
		}
		fx := newFixture(nil)
		env := map[string]string{"AWS_ACCESS_KEY_ID": "ENVKEY", "AWS_SECRET_ACCESS_KEY": "envsecret", "AWS_SESSION_TOKEN": "envtoken"}
		if region != "" {
			env["AWS_DEFAULT_REGION"] = region
		}
		s, err := h.start(fx, "", "", nil, env, false)
		if err != nil {
			h.check(sub, false, "start: %v", err)
			envAll = false
			fx.close()
			continue
		}
		ok := s.awaitState(sub, 0, "default", want, "ENVKEY", "envtoken")
		if ok {
			mark := fx.mark()
			if !s.choose("region", "us-east-2") {
				ok = h.check(sub, false, "region picker did not open")
			} else {
				ok = s.awaitHeader("default", "us-east-2", 15*time.Second) && s.awaitRequest(mark, signedRequest{"ENVKEY", "us-east-2", "envtoken"}, 15*time.Second)
				h.check(sub, ok, "environment session lost its token after region change")
			}
		}
		h.verdict(sub, ok, "environment-only startup and region choice did not succeed")
		envAll = envAll && ok
		s.close()
		fx.close()
	}
	h.verdict(viewPkg+"TestAWSEnvironmentOnlyStartup", envAll, "an environment-only subcase failed")
}

func precedenceScenarios(h *harness) {
	for _, override := range []string{"config", "flag", "AWS_REGION", "AWS_DEFAULT_REGION", "fallback"} {
		id := viewPkg + "TestAWSProfileSwitchRegionPrecedence/" + override
		config := "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-central-2\n"
		if override == "fallback" {
			config = "[profile first]\nregion=us-east-1\n"
		}
		var args []string
		env := map[string]string{}
		expected := "eu-central-2"
		if override == "flag" {
			args = []string{"-p", "first", "-r", "ap-south-2"}
			expected = "ap-south-2"
		} else {
			args = []string{"-p", "first"}
		}
		if strings.HasPrefix(override, "AWS_") {
			env[override] = "us-west-2"
			expected = "us-west-2"
		}
		if override == "fallback" {
			expected = "ap-south-1"
		}
		fx := newFixture(nil)
		s, err := h.start(fx, profileKeys("first", "second"), config, args, env, false)
		if err != nil {
			h.check(id, false, "start: %v", err)
			fx.close()
			continue
		}
		startRegion := "us-east-1"
		if override == "flag" {
			startRegion = "ap-south-2"
		}
		if strings.HasPrefix(override, "AWS_") {
			startRegion = "us-west-2"
		}
		ok := s.awaitHeader("first", startRegion, 15*time.Second)
		steps := []struct {
			kind, value, profile, region, key string
		}{
			{"region", "eu-west-1", "first", "eu-west-1", "firstKEY"},
			{"profile", "second", "second", expected, "secondKEY"},
			{"region", "us-east-2", "second", "us-east-2", "secondKEY"},
		}
		final := expected
		if override == "config" || override == "fallback" {
			final = "eu-west-1"
		}
		steps = append(steps, struct {
			kind, value, profile, region, key string
		}{"profile", "first", "first", final, "firstKEY"})
		for _, step := range steps {
			if !ok {
				break
			}
			mark := fx.mark()
			opened := false
			if step.kind == "region" {
				opened = s.settleRegion(step.profile, step.region)
			} else {
				opened = s.choose(step.kind, step.value)
			}
			if !opened {
				ok = h.check(id, false, "%s picker did not open for %q", step.kind, step.value)
				break
			}
			ok = s.awaitHeader(step.profile, step.region, 15*time.Second) && s.awaitRequest(mark, signedRequest{step.key, step.region, ""}, 15*time.Second)
			if !ok {
				h.check(id, false, "did not settle on %s/%s after choosing %s %q; pane:\n%s", step.profile, step.region, step.kind, step.value, tail(s.pane()))
			}
		}
		h.verdict(id, ok, "precedence sequence failed for %s", override)
		s.close()
		fx.close()
	}
	// Parent passes when every override subcase passed; recorded via checks
	// above plus this aggregate marker.
	parentOK := true
	for _, override := range []string{"config", "flag", "AWS_REGION", "AWS_DEFAULT_REGION", "fallback"} {
		if r := h.results[viewPkg+"TestAWSProfileSwitchRegionPrecedence/"+override]; r == nil || !r.pass {
			parentOK = false
		}
	}
	h.verdict(viewPkg+"TestAWSProfileSwitchRegionPrecedence", parentOK, "a precedence subcase failed")
}

func failedSwitchScenarios(h *harness) {
	all := true
	for _, failure := range []string{"profile_region", "profile_credentials", "region_credentials"} {
		id := viewPkg + "TestAWSFailedSwitchPreservesSession/" + failure
		fx := newFixture(nil)
		s, err := h.start(fx, profileKeys("first", "second"), "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n", []string{"-p", "first"}, nil, false)
		if err != nil {
			h.check(id, false, "start: %v", err)
			all = false
			fx.close()
			continue
		}
		ok := s.awaitHeader("first", "us-east-1", 15*time.Second)
		kind, value, wantErr := "profile", "second", "second"
		switch failure {
		case "profile_region":
			s.rewrite("", "[profile first]\nregion=us-east-1\n[profile second]\nregion=moon-central-1\n")
			wantErr = "moon-central-1"
		case "profile_credentials":
			s.rewrite(profileKeys("first")+"[second]\naws_access_key_id=incomplete\n", "")
		case "region_credentials":
			s.rewrite("[first]\naws_access_key_id=incomplete\n", "")
			kind, value, wantErr = "region", "us-east-2", "first"
		}
		before := s.pane()
		if ok && !s.choose(kind, value) {
			ok = h.check(id, false, "%s picker did not open", kind)
		}
		if ok {
			ok = s.awaitNewText(before, wantErr, 15*time.Second)
			h.check(id, ok, "failed change did not display %q; pane:\n%s", wantErr, tail(s.pane()))
		}
		if ok {
			ok = s.awaitHeader("first", "us-east-1", 5*time.Second)
			h.check(id, ok, "working session not preserved; pane:\n%s", tail(s.pane()))
		}
		// Repair and prove the same choice now succeeds.
		s.rewrite(profileKeys("first", "second"), "[profile first]\nregion=us-east-1\n[profile second]\nregion=eu-west-2\n")
		s.dismissFeedback()
		time.Sleep(300 * time.Millisecond)
		if ok {
			mark := fx.mark()
			target := "second"
			if failure == "region_credentials" {
				mark = fx.mark()
				if s.choose("region", "us-east-2") {
					ok = s.awaitHeader("first", "us-east-2", 15*time.Second) && s.awaitRequest(mark, signedRequest{"firstKEY", "us-east-2", ""}, 15*time.Second)
					h.check(id, ok, "region choice did not recover")
				} else {
					ok = h.check(id, false, "region picker did not reopen")
				}
			} else if s.choose("profile", target) {
				ok = s.awaitHeader("second", "eu-west-2", 15*time.Second) && s.awaitRequest(mark, signedRequest{"secondKEY", "eu-west-2", ""}, 15*time.Second)
				h.check(id, ok, "profile choice did not recover")
			} else {
				ok = h.check(id, false, "profile picker did not reopen")
			}
		}
		h.verdict(id, ok, "failed-switch sequence did not preserve the session and recover")
		all = all && ok
		s.close()
		fx.close()
	}
	h.verdict(viewPkg+"TestAWSFailedSwitchPreservesSession", all, "a failed-switch subcase failed")
}

func localstackScenarios(h *harness) {
	all := true
	for _, region := range []string{"", "us-west-2", "ap-south-2"} {
		sub := viewPkg + "TestAWSLocalStackSelection/region_" + region
		fx := newFixture(nil)
		args := []string{"--localstack", "--port", fx.port()}
		if region != "" {
			args = append(args, "-r", region)
		}
		want := region
		if want == "" {
			want = "af-south-1"
		}
		s, err := h.start(fx, "", "", args, map[string]string{"AWS_REGION": "eu-west-1", "AWS_DEFAULT_REGION": "us-east-2"}, false)
		if err != nil {
			h.check(sub, false, "start: %v", err)
			all = false
			fx.close()
			continue
		}
		ok := s.awaitHeader("localstack", want, 15*time.Second) && s.awaitRequest(0, signedRequest{"test", want, ""}, 15*time.Second)
		h.verdict(sub, ok, "localstack did not settle on %s; pane:\n%s", want, tail(s.pane()))
		all = all && ok
		s.close()
		fx.close()
	}
	h.verdict(viewPkg+"TestAWSLocalStackSelection", all, "a localstack subcase failed")
}
