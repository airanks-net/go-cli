package main

import "testing"

func TestNormalizeHostname(t *testing.T) {
	cases := map[string]string{
		"example.com":          "example.com",
		"WWW.Example.com":      "example.com",
		"https://example.com/": "example.com",
		"best crm software":    "", // multi-word -> search, not a domain
		"localhost":            "",
		"127.0.0.1":            "",
		"[::1]":                "",
		"http://localhost:80":  "",
		"ftp://example.com":    "",
		"":                     "",
		"nodot":                "",
		"example.com.":         "",
	}
	for in, want := range cases {
		if got := normalizeHostname(in); got != want {
			t.Errorf("normalizeHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShouldAttachToken(t *testing.T) {
	env := ResolvedToken{Token: "t", Source: "env"}
	if !shouldAttachToken("https://anywhere.example/v1/x", env) {
		t.Error("env token must attach unconditionally")
	}

	file := ResolvedToken{Token: "t", Source: "file", Host: "airanks.net"}
	if !shouldAttachToken("https://airanks.net/api/v1/domains/x", file) {
		t.Error("file token must attach to its own host")
	}
	if shouldAttachToken("https://other.example/api/v1/domains/x", file) {
		t.Error("file token must not attach to a different host")
	}

	anon := ResolvedToken{Source: "anonymous"}
	if shouldAttachToken("https://airanks.net/api/v1/domains/x", anon) {
		t.Error("no token means never attach")
	}
}

func TestPKCEChallengeIsDeterministicAndURLSafe(t *testing.T) {
	v := generateVerifier()
	c1 := pkceChallenge(v)
	c2 := pkceChallenge(v)
	if c1 != c2 {
		t.Fatal("pkceChallenge must be deterministic for a given verifier")
	}
	for _, r := range c1 + v {
		if r == '+' || r == '/' || r == '=' {
			t.Fatalf("PKCE values must be base64url (no +, /, =), got %q in %q", r, c1)
		}
	}
}

func TestWizardShouldRun(t *testing.T) {
	cases := []struct {
		name                                  string
		hasAuth, jsonOut, stdinTTY, stderrTTY bool
		want                                  bool
	}{
		{"fresh interactive run prompts", false, false, true, true, true},
		{"existing auth never prompts again", true, false, true, true, false},
		{"--json never prompts", false, true, true, true, false},
		{"piped stdin never prompts", false, false, false, true, false},
		{"piped/plain stderr never prompts", false, false, true, false, false},
	}
	for _, c := range cases {
		if got := wizardShouldRun(c.hasAuth, c.jsonOut, c.stdinTTY, c.stderrTTY); got != c.want {
			t.Errorf("%s: wizardShouldRun(%v,%v,%v,%v) = %v, want %v", c.name, c.hasAuth, c.jsonOut, c.stdinTTY, c.stderrTTY, got, c.want)
		}
	}
}

func TestIsPending(t *testing.T) {
	// lookup()'s poll/deadline loop hinges entirely on this classification.
	if !isPending([]byte(`{"ai_files":{"status":"pending"}}`)) {
		t.Fatal("expected pending status to be detected")
	}
	if isPending([]byte(`{"ai_files":{"status":"ready"}}`)) {
		t.Fatal("ready status must not be reported as pending")
	}
}
