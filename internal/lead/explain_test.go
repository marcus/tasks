package lead

import "testing"

func TestExplainReportsTheSpanAndTheDateItsWindowOpens(t *testing.T) {
	anchor := date(t, "2026-11-01")

	phrased := Explain("3 weeks", anchor, true)
	if phrased.Canonical != "3w" || phrased.Human != "3 weeks" || !phrased.HasOpens ||
		phrased.Opens.ISO() != "2026-10-11" || phrased.Error != "" {
		t.Errorf("3 weeks: %+v", phrased)
	}

	unanchored := Explain("3w", anchor, false)
	if unanchored.HasOpens || unanchored.Canonical != "3w" {
		t.Errorf("no anchor must mean no gate date: %+v", unanchored)
	}

	clock := Explain("5 hours", anchor, true)
	if clock.Canonical != "5h" || clock.HasOpens {
		t.Errorf("a clock span opens at an instant, not a date: %+v", clock)
	}

	off := Explain("off", anchor, true)
	if !off.HasCanonical || off.Canonical != "" || off.Human != "no lead time" || off.HasOpens {
		t.Errorf("off: %+v", off)
	}

	junk := Explain("soonish", anchor, true)
	if junk.HasCanonical || junk.Error != Parse("soonish").Error || junk.Input != "soonish" {
		t.Errorf("soonish: %+v", junk)
	}
}
