package record

import "testing"

func TestDelegationSchemaBoundaries(t *testing.T) {
	valid := map[string]any{"kind": "agent", "mode": "research", "status": "ready", "at": "2026-06-02T08:30:00Z"}
	if !DelegationValid(valid) || !DelegationReady(valid) {
		t.Fatalf("valid ready delegation was rejected: %v", DelegationErrors(valid))
	}
	if DelegationTimestamp("2026-02-31T00:00:00Z") {
		t.Fatal("impossible date was accepted")
	}
	if got := DelegationErrors(map[string]any{"kind": "robot", "at": "nope"}); len(got) != 2 || got[0] != "delegation.kind \"robot\" must be human or agent" {
		t.Fatalf("bad-kind cascade = %#v", got)
	}
}

func FuzzDelegationSchemaIsTotal(f *testing.F) {
	f.Add("agent", "research", "ready", "2026-06-02T08:30:00Z")
	f.Add("human", "", "delegated", "2026-02-31T00:00:00Z")
	f.Fuzz(func(t *testing.T, kind, mode, status, at string) {
		value := map[string]any{"kind": kind, "mode": mode, "status": status, "at": at}
		_ = DelegationErrors(value)
		_ = DelegationUnknownKeys(value)
		_ = DelegationOrderedKeys(value, []string{"kind", "mode", "status", "at"})
	})
}
