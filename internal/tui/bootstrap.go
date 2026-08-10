package tui

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"sync"
	"time"

	"github.com/marcus/tasks/internal/agentcontext"
	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/journal"
	"github.com/marcus/tasks/internal/llm"
	"github.com/marcus/tasks/internal/promptfacts"
	"github.com/marcus/tasks/internal/runtimepath"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
	"github.com/marcus/tasks/internal/tui/term"
	"github.com/marcus/tasks/internal/tui/term/agent"
	"github.com/marcus/tasks/internal/updatestamp"
)

// RuntimeOptions configures the shared shipping constructor used by both the
// standalone executable and public embedding adapter.
type RuntimeOptions struct {
	Paths   config.Paths
	Env     determinism.Env
	Session SessionState
	Styler  Styler

	Embedded             bool
	SuppressFooter       bool
	SuppressKeyHints     bool
	SuppressViewKeyHints bool
	SuppressQuit         bool
	SaveSession          func(SessionState) error
}

// NewRuntime wires the application, store, renderer, opener, provider list,
// and agent queue used by every shipping TUI host.
func NewRuntime(options RuntimeOptions) (*Model, error) {
	paths, env := options.Paths, options.Env
	writeOptions := store.Options{
		JournalDir:    journal.DirFor(paths.Org, env),
		Device:        updatestamp.Device(env),
		MaxDepth:      paths.MaxDepth,
		CoalesceScope: runtimeCoalesceScope(env),
	}
	if clock := determinism.Clock(env); clock != nil {
		writeOptions.Now = clock
	}
	if sequence, err := determinism.SharedIDSource(env); err == nil && sequence != nil {
		writeOptions.IDSource = sequence.Call
	}

	temporalContext := func() temporal.Context {
		built, err := temporal.NewContext(time.Now().UTC(), paths.Timezone, paths.TimeFormat)
		if err != nil {
			return temporal.Context{Now: time.Now().UTC(), Timezone: time.UTC, TimezoneID: "Etc/UTC"}
		}
		return built
	}
	app, err := application.New(application.Options{
		Factory: func() application.Store {
			built := writeOptions
			return store.NewWriter(paths.Org, paths.Archive, built)
		},
		TemporalContext: temporalContext,
		HostContext:     paths.HostContext,
		QueryOptions:    []taskquery.Option{taskquery.WithLinkConfig(paths.Links, paths.LinkSystems)},
	})
	if err != nil {
		return nil, err
	}

	entries, queue, err := BuildAgentQueue(paths, env)
	if err != nil {
		return nil, err
	}
	styler := options.Styler
	if styler == nil {
		styler = term.NewStyler(paths.Theme, paths.Colors)
	}
	return New(Options{
		App: app, Paths: paths, Env: env, Session: options.Session,
		Styler: styler, Entries: entries, Queue: queue,
		Opener:               SystemOpener{Env: env},
		Embedded:             options.Embedded,
		SuppressFooter:       options.SuppressFooter,
		SuppressKeyHints:     options.SuppressKeyHints,
		SuppressViewKeyHints: options.SuppressViewKeyHints,
		SuppressQuit:         options.SuppressQuit,
		SaveSession:          options.SaveSession,
	}), nil
}

var runtimeScope struct {
	sync.Once
	value string
}

func runtimeCoalesceScope(env determinism.Env) string {
	if pinned, ok := determinism.CoalesceScope(env); ok {
		return pinned
	}
	runtimeScope.Do(func() {
		buffer := make([]byte, 16)
		if _, err := rand.Read(buffer); err != nil {
			runtimeScope.value = "tasks-tui"
			return
		}
		runtimeScope.value = hex.EncodeToString(buffer)
	})
	return runtimeScope.value
}

// BuildAgentQueue constructs the Tasks-owned provider/model registry and queue.
func BuildAgentQueue(paths config.Paths, env determinism.Env) ([]AgentEntry, *agent.Queue, error) {
	conf := llm.LoadConfig(env, "")
	entries := []AgentEntry{}
	for _, entry := range llm.Entries(conf) {
		entries = append(entries, AgentEntry{
			ProviderName: entry.Provider, ModelName: entry.Model, Label: entry.UILabel(),
		})
	}
	dataDir := filepath.Dir(paths.Org)
	queue, err := agent.NewQueue(agent.Options{
		Factory:      AgentFactory(paths, env, conf, dataDir, runtimepath.TasksCLI()),
		Availability: AgentAvailability(env, conf, dataDir),
	})
	if err != nil {
		return nil, nil, err
	}
	return entries, queue, nil
}

// AgentFactory creates a provider adapter with fresh Tasks system context when
// a queued request starts.
func AgentFactory(paths config.Paths, env determinism.Env, conf llm.Config,
	dataDir, cliPath string) func(agent.Entry) (agent.Adapter, error) {
	return func(entry agent.Entry) (agent.Adapter, error) {
		system, err := agentcontext.Build(paths, cliPath, promptfacts.Sources{})
		if err != nil {
			return nil, err
		}
		built, err := llm.Build(
			llm.Entry{Provider: entry.Provider(), Model: entry.Model()},
			llm.BuildOptions{Root: dataDir, System: system, Path: llm.PathFrom(env)}, conf)
		if err != nil {
			return nil, err
		}
		return NewAgentAdapter(built), nil
	}
}

// AgentAvailability is the context-free submit-time provider probe.
func AgentAvailability(env determinism.Env, conf llm.Config, dataDir string) func(agent.Entry) bool {
	return func(entry agent.Entry) bool {
		built, err := llm.Build(
			llm.Entry{Provider: entry.Provider(), Model: entry.Model()},
			llm.BuildOptions{Root: dataDir, Path: llm.PathFrom(env)}, conf)
		return err == nil && built.Available()
	}
}
