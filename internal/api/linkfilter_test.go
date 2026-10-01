package api

import (
	"net/url"
	"testing"
)

// GET /tasks?link= (issue #37) and the doc link system (issue #38).

const linkFilterOrg = `{"type":"meta","version":2}
{"type":"section","id":"ffff0001","title":"Work"}
{"type":"task","id":"ffff0002","parent":"ffff0001","state":"NEXT","title":"Reply in the thread","links":[{"url":"https://acme.slack.com/archives/C042/p1700000000123456?thread_ts=1700000000.000100&cid=C042"}]}
{"type":"task","id":"ffff0003","parent":"ffff0001","state":"NEXT","title":"Other thread","body":"https://acme.slack.com/archives/C042/p1700000000999999"}
{"type":"task","id":"ffff0004","parent":"ffff0001","state":"DONE","title":"Closed with the same thread","closed":"2026-07-01","body":"See [[https://acme.slack.com/archives/C042/p1700000000123456/][thread]]."}
{"type":"task","id":"ffff0005","parent":"ffff0001","state":"NEXT","title":"From the review notes","links":[{"url":"http://127.0.0.1:8080/open?p=/home/user/notes/review.md"}]}
`

const linkFilterArchive = `{"type":"meta","version":2}
{"type":"section","id":"ffff0010","title":"Archive"}
{"type":"task","id":"ffff0011","parent":"ffff0010","state":"DONE","title":"Archived with the thread","closed":"2026-06-01","body":"https://acme.slack.com/archives/C042/p1700000000123456#x"}
`

func TestListFiltersByLink(t *testing.T) {
	h := newHarnessWith(t, linkFilterOrg, linkFilterArchive, "")
	thread := url.QueryEscape("https://acme.slack.com/archives/C042/p1700000000123456")
	for query, want := range map[string][]string{
		"link=" + thread:                     {"ffff0002"},
		"link=" + thread + "&scope=done":     {"ffff0004"},
		"link=" + thread + "&scope=archived": {"ffff0011"},
		"link=" + thread + "&scope=all":      {"ffff0002", "ffff0004", "ffff0011"},
		"link=" + url.QueryEscape("https://acme.slack.com/archives/C042/p1700000000999999/") + "&scope=all": {"ffff0003"},
		"link=" + url.QueryEscape("https://example.com/nothing") + "&scope=all":                             {},
		"link=" + url.QueryEscape("http://127.0.0.1:8080/open?p=%2Fhome%2Fuser%2Fnotes%2Freview.md"):        {"ffff0005"},
		"link=" + thread + "&available=true":                                                                {"ffff0002"},
	} {
		answered := h.get("/api/v1/tasks?" + query)
		assertStatus(t, answered, 200)
		assertStrings(t, answered.ids(), want, query)
	}
	assertError(t, h.get("/api/v1/tasks?link="), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?link=%20"), 422, "validation_failed")
	assertError(t, h.get("/api/v1/tasks?url=x"), 422, "validation_failed")
}

func TestDocLinkPatternsClassifyAsDoc(t *testing.T) {
	h := buildWithModes(t, t.TempDir(), linkFilterOrg, linkFilterArchive, "", true, nil, func(h *harness) {
		h.docPatterns = []string{"http://127.0.0.1:8080/open?p=*"}
		// A pattern names a whole URL shape, so it beats a host row.
		h.linkSystems = map[string]string{"local": "127.0.0.1"}
	})
	links, _ := h.get("/api/v1/tasks/ffff0005").dig("data", "links").([]any)
	if len(links) != 1 || links[0].(map[string]any)["system"] != "doc" {
		t.Fatalf("links = %v", links)
	}
	patterns, _ := h.get("/api/v1/meta").dig("data", "doc_link_patterns").([]any)
	if len(patterns) != 1 || patterns[0] != "http://127.0.0.1:8080/open?p=*" {
		t.Fatalf("meta doc_link_patterns = %v", patterns)
	}

	// Unconfigured, the same URL falls back to its host.
	plain := newHarnessWith(t, linkFilterOrg, linkFilterArchive, "")
	links, _ = plain.get("/api/v1/tasks/ffff0005").dig("data", "links").([]any)
	if len(links) != 1 || links[0].(map[string]any)["system"] != "127.0.0.1" {
		t.Fatalf("unconfigured links = %v", links)
	}
}
