package recur

import "testing"

// A stored calendar schedule is exactly one spelling. Recognizing a value that
// MEANS something but is spelled differently would let two spellings of one
// schedule sit in the same file, so the round trip is the whole test.
func TestCalendarRecognizesOnlyCanonicalSpellings(t *testing.T) {
	for _, canonical := range []string{
		"w:mon", "w:mon,wed", "2w:sat", "+w:fri",
		"m:15", "m:last", "m:1,15", "m:last,2tue", "3m:2tue",
		"y:07-04", "y:11:3thu", "2y:02:5fri",
	} {
		if !Calendar(canonical) {
			t.Errorf("Calendar(%q) = false, want true", canonical)
		}
	}
	for _, rejected := range []string{
		"w:monday",    // readable input, not the stored spelling
		"w:wed,mon",   // stored order is calendar order
		"1w:mon",      // an interval of 1 is spelled by omission
		".+w:mon",     // an interval prefix on a calendar schedule
		"++m:15",      // likewise
		"m:0", "m:32", // days of the month run 1-31
		"m:6mon",       // ordinals run 1-5 or "last"
		"y:13-01",      // months run 1-12
		"y:02-30",      // February has no 30th
		"w:", "m:", "", // no body at all
		".+1w", // an interval cookie is the other grammar
	} {
		if Calendar(rejected) {
			t.Errorf("Calendar(%q) = true, want false", rejected)
		}
	}
}
