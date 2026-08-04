package recur

import "testing"

// The expectations below are transcribed from the Ruby oracle capture at
// the compatibility oracle archived at ruby-final-2026-08-04, whose output
// is recorded in oracle-inspect.txt in the same directory. Both Recur error
// sites echo the caller's spelling through String#inspect, so the quoted form
// is observable output, not an implementation detail.
func TestRejectionSpellingMatchesRubyStringInspect(t *testing.T) {
	cases := []struct {
		name, input, quoted string
	}{
		{"plain", "zz", "\"zz\""},
		{"dquote", "he said \"weekly\"", "\"he said \\\"weekly\\\"\""},
		{"backslash", "back\\slash", "\"back\\\\slash\""},
		{"tab", "no\tcookie", "\"no\\tcookie\""},
		{"newline", "no\ncookie", "\"no\\ncookie\""},
		{"carriage", "no\rcookie", "\"no\\rcookie\""},
		{"escape", "\u001b[0mzz", "\"\\e[0mzz\""},
		{"bell", "zz\u0007", "\"zz\\a\""},
		{"vtab", "zz\u000bzz", "\"zz\\vzz\""},
		{"backspace", "zz\bzz", "\"zz\\bzz\""},
		{"formfeed", "zz\fzz", "\"zz\\fzz\""},
		{"nul", "zz\u0000zz", "\"zz\\u0000zz\""},
		{"soh", "zz\u0001", "\"zz\\u0001\""},
		{"us", "zz\u001f", "\"zz\\u001F\""},
		{"del", "zz\u007F", "\"zz\\u007F\""},
		{"c1_80", "zz\u0080", "\"zz\\u0080\""},
		{"c1_9f", "zz\u009F", "\"zz\\u009F\""},
		{"interp_brace", "zz#{1}", "\"zz\\#{1}\""},
		{"interp_dollar", "zz#$x", "\"zz\\#$x\""},
		{"interp_at", "zz#@x", "\"zz\\#@x\""},
		{"hash_plain", "zz#zz", "\"zz#zz\""},
		{"hash_trailing", "zz#", "\"zz#\""},
		{"nbsp", "zz\u00A0zz", "\"zz\u00A0zz\""},
		{"accent", "wöchentlich", "\"wöchentlich\""},
		{"cjk", "毎週", "\"毎週\""},
		{"emoji", "😀", "\"😀\""},
		{"soft_hyphen", "zz\u00ADzz", "\"zz\u00ADzz\""},
		{"zwsp", "zz\u200Bzz", "\"zz\u200Bzz\""},
		{"bom", "zz\uFEFFzz", "\"zz\uFEFFzz\""},
		{"combining", "z\u0301z", "\"z\u0301z\""},
		{"line_sep", "zz\u2028zz", "\"zz\\u2028zz\""},
		{"para_sep", "zz\u2029zz", "\"zz\\u2029zz\""},
		{"unassigned", "zz\u0378", "\"zz\\u0378\""},
		{"private_use", "zz\uE000", "\"zz\uE000\""},
		{"supp_private", "zz\U000F0000", "\"zz\U000F0000\""},
		{"tag_char", "zz\U000E0001", "\"zz\U000E0001\""},
		{"musical_ctl", "zz\U0001D173", "\"zz\U0001D173\""},
		{"high_unassign", "zz\U0010FFFF", "\"zz\\u{10FFFF}\""},
		{"supp_unassign", "zz\U0002FFFF", "\"zz\\u{2FFFF}\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := "unrecognized schedule: " + tc.quoted
			if got := Parse(tc.input, ".+"); got.Error != want {
				t.Errorf("Parse error = %s, want %s", got.Error, want)
			}
			want = "not a repeater cookie: " + tc.quoted
			_, err := NextDate(tc.input, CivilDate{}, CivilDate{})
			if err == nil || err.Error() != want {
				t.Errorf("NextDate error = %v, want %s", err, want)
			}
		})
	}
}
