package charwidth

import "testing"

// The kernel has no Ruby test file of its own — lib/char_width.rb is covered
// through test_ansi.rb and test_term_form_text_fields.rb. These tests pin the
// table boundaries directly, because every layout decision in the TUI depends
// on them.

func TestRuneWidthClassesCodepoints(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{' ', 1}, {'~', 1}, {'a', 1},
		{0x00, 0}, {0x1F, 0}, // controls
		{0x7F, 1},                             // DEL is outside the fast path and not tabled
		{0x0301, 0},                           // combining acute
		{0x200D, 0},                           // zero-width joiner
		{0xFE0F, 0},                           // variation selector, alone
		{0xFEFF, 0},                           // BOM
		{0x1100, 2}, {0x115F, 2}, {0x1160, 1}, // Hangul jamo boundary
		{0x4E00, 2}, {0x9FFF, 2}, {0xA000, 2},
		{0x2713, 1}, {0x26A0, 1}, {0x25B8, 1}, // text-presentation symbols
		{0x2728, 2}, {0x2705, 2}, {0x2B50, 2}, // default-emoji-presentation
		{0x1F600, 2}, {0x1F9FF, 2},
		{0x00E9, 1}, // é precomposed
	}
	for _, c := range cases {
		if got := Rune(c.r); got != c.want {
			t.Fatalf("Rune(%#x) = %d, want %d", c.r, got, c.want)
		}
	}
}

func TestClusterWidthUsesTheFirstVisibleBaseAndTheVariationSelector(t *testing.T) {
	cases := []struct {
		gc   string
		want int
	}{
		{"a", 1},
		{"é", 1}, // e + combining acute
		{"é", 1},  // precomposed é
		{"界", 2},
		{"⚠", 1},       // text presentation
		{"⚠️", 2},      // U+FE0F promotes the whole cluster
		{"👩‍💻", 2},     // ZWJ sequence
		{"👨‍👩‍👧‍👦", 2}, // four-person ZWJ family
		{"́", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := Cluster(c.gc); got != c.want {
			t.Fatalf("Cluster(%q) = %d, want %d", c.gc, got, c.want)
		}
	}
}

func TestClustersSegmentsByExtendedGraphemeCluster(t *testing.T) {
	got := Clusters("a界👩‍💻é")
	want := []string{"a", "界", "👩‍💻", "é"}
	if len(got) != len(want) {
		t.Fatalf("Clusters = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Clusters = %#v, want %#v", got, want)
		}
	}
	if Clusters("") != nil {
		t.Fatal("empty string must yield no clusters")
	}
}

func TestFirstClusterWalksTheString(t *testing.T) {
	gc, rest := FirstCluster("👩‍💻tail")
	if gc != "👩‍💻" || rest != "tail" {
		t.Fatalf("FirstCluster = %q, %q", gc, rest)
	}
	if gc, rest := FirstCluster(""); gc != "" || rest != "" {
		t.Fatalf("FirstCluster(empty) = %q, %q", gc, rest)
	}
}

func TestStringWidthSumsClusters(t *testing.T) {
	cases := map[string]int{
		"":               0,
		"hello":          5,
		"界界界":            6,
		"Inbox empty. ✨": 15,
		"é界👩‍💻":          5,
	}
	for s, want := range cases {
		if got := String(s); got != want {
			t.Fatalf("String(%q) = %d, want %d", s, got, want)
		}
	}
}
