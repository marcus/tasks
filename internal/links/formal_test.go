package links

import "testing"

func TestExpandFormalPassesURLsAndExpandsConfiguredShorthands(t *testing.T) {
	shorthands := map[string]string{"jira": "https://jira.example.test/browse/%s", "gh": "https://github.com/"}
	cases := []struct {
		raw, url, label string
		ok              bool
	}{
		{"https://example.test/a", "https://example.test/a", "", true},
		{"jira:OPS-1234", "https://jira.example.test/browse/OPS-1234", "jira:OPS-1234", true},
		{"gh:marcus/tasks", "https://github.com/marcus/tasks", "gh:marcus/tasks", true},
		{"linear:ABC-1", "", "", false},
		{"jira:", "", "", false},
		{"file:///etc/passwd", "", "", false},
	}
	for _, c := range cases {
		url, label, ok := ExpandFormal(c.raw, shorthands)
		if url != c.url || label != c.label || ok != c.ok {
			t.Errorf("ExpandFormal(%q) = %q, %q, %v", c.raw, url, label, ok)
		}
	}
}

func TestLiftTitleURLKeepsTheWordsAndLiftsTheTrailingURL(t *testing.T) {
	cases := []struct {
		title, wantTitle string
		explicit         []FormalLink
		wantLinks        []FormalLink
	}{
		{"Read the RFC https://example.test/rfc", "Read the RFC", nil,
			[]FormalLink{{URL: "https://example.test/rfc"}}},
		{"see https://example.test/rfc.", "see.", nil, []FormalLink{{URL: "https://example.test/rfc"}}},
		{"https://example.test/only", "https://example.test/only", nil,
			[]FormalLink{{URL: "https://example.test/only"}}},
		{"no url here", "no url here", nil, nil},
		{"jira:OPS-1", "jira:OPS-1", nil, nil},
		{"Review https://example.test/pr", "Review https://example.test/pr",
			[]FormalLink{{URL: "https://example.test/pr", Label: "PR"}},
			[]FormalLink{{URL: "https://example.test/pr", Label: "PR"}}},
	}
	for _, c := range cases {
		title, lifted := LiftTitleURL(c.title, c.explicit)
		if title != c.wantTitle || len(lifted) != len(c.wantLinks) {
			t.Errorf("LiftTitleURL(%q) = %q, %#v", c.title, title, lifted)
			continue
		}
		for index := range lifted {
			if lifted[index] != c.wantLinks[index] {
				t.Errorf("LiftTitleURL(%q) link %d = %#v", c.title, index, lifted[index])
			}
		}
	}
}

// The caller's slice is never appended into: a shared backing array would let
// one capture's lifted link appear in another's list.
func TestLiftTitleURLDoesNotWriteIntoTheCallersSlice(t *testing.T) {
	backing := make([]FormalLink, 1, 4)
	backing[0] = FormalLink{URL: "https://example.test/a"}
	_, lifted := LiftTitleURL("x https://example.test/b", backing)
	if len(lifted) != 2 || backing[:2][1].URL != "" {
		t.Errorf("lifted = %#v, backing = %#v", lifted, backing[:2])
	}
}
