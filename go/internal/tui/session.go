package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tasks-go/internal/atomic"
	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
)

// SessionVersion is the on-disk schema of the saved TUI state. A file naming
// any other version is ignored wholesale, exactly as Ruby ignores it.
const SessionVersion = 1

// SessionState is the small bit of UI preference that survives a restart: the
// active view, the collapsed set, the panel geometry, and the context filters.
// It is the port of lib/tui/session.rb plus UiState's restore/serialize halves.
//
// It is deliberately dumb. This is UI preference, not task data — one file per
// user, and losing it costs nothing but a default. EVERY read tolerates a
// missing, corrupt, or foreign-version file by returning the zero value:
// session state must never be able to keep the TUI from starting.
type SessionState struct {
	View           string   `json:"view,omitempty"`
	Collapsed      []string `json:"collapsed,omitempty"`
	PanelMode      string   `json:"panel_mode,omitempty"`
	PanelOffset    int      `json:"panel_offset,omitempty"`
	ContextFilters []string `json:"context_filters,omitempty"`

	// ContextFilter is the retired singular spelling. It is read so an existing
	// state file keeps working, and never written.
	ContextFilter string `json:"context_filter,omitempty"`
}

// SessionPath is $XDG_STATE_HOME/tasks/tui.json, resolved against the caller's
// environment rather than the process environment — the same threading Wave 0
// had to fix in config.parseFile, for the same reason: a harness that pins HOME
// must be obeyed.
func SessionPath(env determinism.Env) string {
	return filepath.Join(config.XDGBase(env, "XDG_STATE_HOME", ".local", "state"), "tasks", "tui.json")
}

// LoadSession reads the saved state, or the zero value when there is none to
// be had.
func LoadSession(env determinism.Env) SessionState {
	raw, err := os.ReadFile(SessionPath(env))
	if err != nil {
		return SessionState{}
	}
	var envelope struct {
		Version int `json:"version"`
		SessionState
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Version != SessionVersion {
		return SessionState{}
	}
	return envelope.SessionState
}

// SaveSession overwrites the saved state. Best-effort: a read-only state
// directory must not crash TUI exit, so the error is reported and ignorable.
func SaveSession(state SessionState, env determinism.Env) error {
	path := SessionPath(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// The version stamp is ours alone. Ruby strips a state key named "version"
	// for the same reason: it would emit a duplicate JSON key and poison every
	// future load. Go's struct embedding cannot produce one.
	envelope := struct {
		Version int `json:"version"`
		SessionState
	}{Version: SessionVersion, SessionState: state}
	encoded, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	return atomic.Write(path, string(encoded)+"\n")
}

// NormalizeContextFilters is UiState.normalize_context_filters: keep the
// non-empty strings, give each a leading "@", drop anything shorter than two
// characters, deduplicate, and sort. Sorting is what makes the saved value
// stable across runs, so an unchanged filter set does not rewrite the file.
func NormalizeContextFilters(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		token := strings.TrimSpace(value)
		if token == "" {
			continue
		}
		if !strings.HasPrefix(token, "@") {
			token = "@" + token
		}
		if len([]rune(token)) < 2 || seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	sort.Strings(out)
	return out
}
