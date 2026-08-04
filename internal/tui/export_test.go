package tui

import (
	"strings"
	"testing"
)

const exportFixture = `{"type":"meta","version":2}
{"type":"section","id":"eeee0001","title":"Work"}
{"type":"task","id":"eeee0002","parent":"eeee0001","state":"TODO","title":"Timed blocked task","tags":["@computer","defer"],"deadline":"2026-07-20","deadline_time":{"local":"17:00","timezone":"Europe/London"},"lead":"1w","body":"A note."}
{"type":"task","id":"eeee0003","parent":"eeee0002","state":"NEXT","title":"Inherited blocker"}
`

func TestYankMarkdownActionUsesHeldQueriesWithoutARealClipboard(t *testing.T) {
	copied := ""
	h := newModelHarness(t, harnessOptions{live: exportFixture})
	h.model.copyToClipboard = func(text string) bool { copied = text; return true }
	h.model.showDeferred = true
	h.model.RefreshRows()
	h.selectRowByID("eeee0002")
	h.model.YankMarkdown()
	for _, wanted := range []string{
		"- deadline: 2026-07-20 17:00 [Europe/London]",
		"- lead time: 1 week before",
		"- on hold: yes",
		"- availability: on hold via eeee0002",
		"A note.",
	} {
		if !strings.Contains(copied, wanted) {
			t.Errorf("markdown omitted %q:\n%s", wanted, copied)
		}
	}
	if h.model.FlashMessage() != "yanked: “Timed blocked task”" {
		t.Fatalf("action said %q", h.model.FlashMessage())
	}
	h.selectRowByID("eeee0003")
	h.model.YankMarkdown()
	if !strings.Contains(copied, "- availability: ancestor on hold via eeee0002") {
		t.Fatalf("inherited blocker was omitted:\n%s", copied)
	}
}
