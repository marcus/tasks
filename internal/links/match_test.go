package links

import "testing"

func TestMatchKeyJoinsSpellingsOfOneLink(t *testing.T) {
	base := "https://acme.slack.com/archives/C042/p1700000000123456"
	for _, spelling := range []string{
		base,
		base + "/",
		base + "#reply",
		base + "?thread_ts=1700000000.000100&cid=C042",
		"HTTPS://ACME.slack.com/archives/C042/p1700000000123456",
		"https://acme.slack.com:443/archives/C042/p1700000000123456",
		base + "?utm_source=mail",
	} {
		if !SameLink(spelling, base) {
			t.Errorf("SameLink(%q, base) = false (key %q)", spelling, MatchKey(spelling))
		}
	}
	for _, pair := range [][2]string{
		{"http://127.0.0.1:8080/open?p=/home/a.md", "http://127.0.0.1:8080/open?p=%2Fhome%2Fa.md"},
		{"https://example.com/a~b", "https://example.com/a%7Eb"},
		{"https://example.com/x?q=a+b", "https://example.com/x?q=a%20b"},
		{"https://example.com/x?q=%2f", "https://example.com/x?q=%2F"},
	} {
		if !SameLink(pair[0], pair[1]) {
			t.Errorf("SameLink(%q, %q) = false (%q vs %q)", pair[0], pair[1], MatchKey(pair[0]), MatchKey(pair[1]))
		}
	}
	if !SameLink("https://www.example.com/a?b=2&a=1", "https://example.com/a?a=1&b=2") {
		t.Error("www and query order should not split a link")
	}
}

func TestMatchKeyKeepsWhatAddressesTheResource(t *testing.T) {
	for _, pair := range [][2]string{
		{"https://acme.slack.com/archives/C042/p1", "https://acme.slack.com/archives/C042/p2"},
		{"http://127.0.0.1:8080/open?p=/notes/a.md", "http://127.0.0.1:8080/open?p=/notes/b.md"},
		{"http://127.0.0.1:8080/x", "http://127.0.0.1:9090/x"},
		{"https://example.com/x", "http://example.com/x"},
		// thread_ts is noise only on Slack, where the path names the message.
		{"https://example.com/x?thread_ts=1", "https://example.com/x"},
	} {
		if SameLink(pair[0], pair[1]) {
			t.Errorf("SameLink(%q, %q) = true", pair[0], pair[1])
		}
	}
	if MatchKey(" not a url ") != "not a url" {
		t.Errorf("an unparseable value should match only itself, got %q", MatchKey(" not a url "))
	}
}

func TestDocClassification(t *testing.T) {
	for raw, want := range map[string]string{
		"notes/2026-01-01-review.md":                 SystemDoc,
		"review.TXT":                                 SystemDoc,
		"file:///Users/me/notes/review.md":           SystemDoc,
		"notes/image.png":                            "link",
		"/abs/path/review.md":                        "link",
		"http://127.0.0.1:8080/open?p=/x/r.md":       "127.0.0.1",
		"https://github.com/acme/app/blob/main/x.md": "github",
	} {
		if got := Classify(raw, nil); got != want {
			t.Errorf("Classify(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestDocPatterns(t *testing.T) {
	patterns := ParseDocPatterns("  http://127.0.0.1:8080/open?p=*   https://notes.example.com/*.md ")
	if len(patterns) != 2 {
		t.Fatalf("patterns = %q", patterns)
	}
	for raw, want := range map[string]bool{
		"http://127.0.0.1:8080/open?p=/home/user/notes/review.md": true,
		"https://notes.example.com/a/b.md":                        true,
		"https://notes.example.com/a/b.mdx":                       false,
		"http://127.0.0.1:9090/open?p=/x.md":                      false,
		"http://127.0.0.1:8080/openXp=/x.md":                      false,
	} {
		if got := MatchesDocPattern(raw, patterns); got != want {
			t.Errorf("MatchesDocPattern(%q) = %v, want %v", raw, got, want)
		}
	}
}
