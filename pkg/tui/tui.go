// Package tui exposes the Tasks terminal UI as an embeddable Bubble Tea v2
// component. Tasks continues to own configuration, persistence, rendering, and
// agent processes; a host owns placement, lifecycle, and any surrounding UI.
package tui

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/record"
	internal "github.com/marcus/tasks/internal/tui"
	"github.com/marcus/tasks/internal/tui/term"
	"github.com/marcus/tasks/internal/tui/term/shortcuts"
)

// View is a stable Tasks view key used for initial presentation only.
type View string

const (
	ViewAgenda    View = "agenda"
	ViewNext      View = "next"
	ViewQuadrants View = "quadrants"
	ViewInbox     View = "inbox"
	ViewProjects  View = "projects"
	ViewOutline   View = "outline"
)

// FocusContext names the interaction layer currently consuming keys.
type FocusContext string

const (
	FocusList                FocusContext = "tasks-list"
	FocusDetail              FocusContext = "tasks-detail"
	FocusTaskEdit            FocusContext = "tasks-task-edit"
	FocusModal               FocusContext = "tasks-modal"
	FocusModalFilter         FocusContext = "tasks-modal-filter"
	FocusForm                FocusContext = "tasks-form"
	FocusFieldModal          FocusContext = "tasks-field-modal"
	FocusPicker              FocusContext = "tasks-picker"
	FocusContextPicker       FocusContext = "tasks-context-picker"
	FocusFilter              FocusContext = "tasks-filter"
	FocusPrompt              FocusContext = "tasks-prompt"
	FocusResponse            FocusContext = "tasks-response"
	FocusResponseDetail      FocusContext = "tasks-response-detail"
	FocusAgentActivity       FocusContext = "tasks-agent-activity"
	FocusAgentActivityFilter FocusContext = "tasks-agent-activity-filter"
)

// SpatialFocus names one stable region in Tasks' rendered list/detail layout.
// It is separate from FocusContext, which reports input and overlay ownership.
type SpatialFocus string

const (
	SpatialFocusList   SpatialFocus = "list"
	SpatialFocusDetail SpatialFocus = "detail"
)

// Rect is one half-open rectangle in 0-based terminal cells.
type Rect struct {
	X, Y          int
	Width, Height int
}

// SpatialFocusStop is one visible host focus destination. Stops are returned
// in current visual order and their rectangles are taken from the exact layout
// Tasks renders and hit-tests.
type SpatialFocusStop struct {
	ID   SpatialFocus
	Rect Rect
}

// Binding is a default Bubble Tea key name mapped to a Tasks command.
type Binding struct {
	Key       string
	CommandID string
	Context   FocusContext
}

// Command is Tasks-owned command metadata for host palettes and footers.
type Command struct {
	ID              string
	FooterLabel     string
	Description     string
	Context         FocusContext
	FooterPriority  int
	DefaultBindings []string
}

// ContextMetadata describes a host key-routing context. Contexts backed by an
// input widget may contain only the global shortcut bindings.
type ContextMetadata struct {
	Name              FocusContext
	ConsumesTextInput bool
}

// ExportBindings projects the single Tasks shortcut registry for host keymaps.
func ExportBindings() []Binding {
	exported := shortcuts.ExportBindings()
	out := make([]Binding, 0, len(exported))
	for _, binding := range exported {
		out = append(out, Binding{
			Key: binding.Key, CommandID: binding.CommandID,
			Context: hostFocusContext(binding.Context),
		})
	}
	return out
}

// ExportCommands projects the same registry for host palettes and footers,
// describing delegation with the BUILT-IN mode vocabulary. A host running
// against a store that configures its own set passes it to ExportCommandsWith,
// so its palette names the words that store will actually accept.
func ExportCommands() []Command { return ExportCommandsWith(nil) }

// ExportCommandsWith is ExportCommands for one delegation mode vocabulary, in
// the order it should be read. An empty list means the built-in set. It is
// []string rather than an interface because an embedder resolves the
// vocabulary from `tasks config --json` or the API's meta document, and neither
// hands back a Go type.
func ExportCommandsWith(modes []string) []Command {
	var vocabulary record.ModeVocabulary
	if len(modes) > 0 {
		vocabulary = record.ModeSet(modes)
	}
	exported := shortcuts.ExportCommandsWith(vocabulary)
	out := make([]Command, 0, len(exported))
	for _, command := range exported {
		out = append(out, Command{
			ID: command.ID, FooterLabel: command.FooterLabel,
			Description: command.Description, Context: hostFocusContext(command.Context),
			FooterPriority:  command.FooterPriority,
			DefaultBindings: append([]string{}, command.DefaultBindings...),
		})
	}
	return out
}

// ExportContexts lists every stable Tasks host context, including input-only
// layers whose keystrokes are interpreted by their owning editor widget.
func ExportContexts() []ContextMetadata {
	exported := shortcuts.ExportContexts()
	out := make([]ContextMetadata, 0, len(exported))
	for _, context := range exported {
		out = append(out, ContextMetadata{
			Name: hostFocusContext(context.Name), ConsumesTextInput: context.ConsumesTextInput,
		})
	}
	return out
}

func hostFocusContext(internalName string) FocusContext {
	return FocusContext("tasks-" + strings.ReplaceAll(internalName, "_", "-"))
}

// ThemeOptions are Tasks semantic theme values, independent of any host UI.
//
// Colors is an OVERLAY by default: each host-supplied slot wins, every slot the
// host does not name keeps the value the user configured in their own Tasks
// config, and anything neither names falls back to the named base theme. A host
// can therefore align three slots with its own chrome without silently
// destroying the palette its user chose.
//
// ReplaceColors opts into the older wholesale behaviour: the host's map becomes
// the complete set of overrides and the user's configured colors are dropped.
// It exists for a host that must guarantee an exact palette; it is never the
// default, because discarding user configuration is not a reasonable default.
type ThemeOptions struct {
	Name          string
	Colors        map[string]string
	ReplaceColors bool
}

// EmbeddedOptions configures a host-owned Tasks component. SessionNamespace is
// required so embedding can never overwrite the standalone tasks-tui session.
// Environment is primarily a deterministic/test seam; nil snapshots os.Environ.
type EmbeddedOptions struct {
	SessionNamespace string
	InitialView      View
	InitialContexts  []string
	// SuppressFooter is the blunt switch: Tasks paints NO footer at all. That
	// stack is not only the key hints — it is also the agent transcript, the
	// store-read banner, the flash, the filter and context-filter lines, and the
	// prompt input itself. A host that suppresses it owns rendering all of that,
	// and the prompt becomes an invisible caret unless the host draws one. Use it
	// only for a host that genuinely re-implements the whole footer.
	//
	// SuppressKeyHints is the fine switch, and is what a host building a unified
	// key-hint bar wants: Tasks drops ONLY its ordinary key-hint row, on the same
	// reading as SuppressQuit — the HOST owns that affordance — and keeps the
	// prompt, agent transcript, store-read banner, flash, and filter lines.
	//
	// SuppressFooter wins where both are set; there is nothing left to hint.
	//
	// SuppressViewKeyHints is the header counterpart, for a host that has taken
	// the number row (Sidecar switches tabs with 1-9). Tasks drops the "1 ".."6 "
	// prefixes from its view tab strip and keeps the view names, the Inbox badge,
	// and the current-view highlight. It is an ADVERTISEMENT switch only: 1-6
	// still jump views, exactly as SuppressQuit still acts on the quit key. It is
	// independent of the two footer switches. Hosts should surface the views in
	// their own chrome — prev_view/next_view stay bound to left/right arrows.
	SuppressFooter       bool
	SuppressKeyHints     bool
	SuppressViewKeyHints bool
	SuppressQuit         bool
	Theme                ThemeOptions
	Environment          map[string]string
}

// Model is an embeddable Tasks TUI. It intentionally does not expose the
// internal model or store; hosts exchange only Bubble Tea messages and strings.
type Model struct {
	inner     *internal.Model
	env       determinism.Env
	namespace string
	warnings  []string

	closeOnce sync.Once
	closeErr  error
}

var namespacePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// NewEmbedded resolves the normal Tasks configuration and builds the same
// application, renderer, provider registry, and agent queue as tasks-tui.
func NewEmbedded(options EmbeddedOptions) (*Model, error) {
	if !namespacePattern.MatchString(options.SessionNamespace) {
		return nil, fmt.Errorf("session namespace must contain only letters, digits, dot, underscore, or hyphen")
	}
	env := determinism.OSEnv()
	if options.Environment != nil {
		env = make(determinism.Env, len(options.Environment))
		for key, value := range options.Environment {
			env[key] = value
		}
	}
	paths := config.Resolve("", env, nil)
	if !paths.Configured {
		return nil, fmt.Errorf("%s", config.ConfigurationRequiredMessage(paths))
	}
	if options.Theme.Name != "" {
		paths.Theme = options.Theme.Name
	}
	if options.Theme.ReplaceColors {
		paths.Colors = cloneStrings(options.Theme.Colors)
	} else if options.Theme.Colors != nil {
		paths.Colors = overlayStrings(paths.Colors, options.Theme.Colors)
	}

	session := internal.LoadEmbeddedSession(env, options.SessionNamespace)
	if options.InitialView != "" {
		if !validView(options.InitialView) {
			return nil, fmt.Errorf("unknown initial view %q", options.InitialView)
		}
		session.View = string(options.InitialView)
	}
	if options.InitialContexts != nil {
		session.ContextFilters = internal.NormalizeContextFilters(options.InitialContexts)
		session.ContextFilter = ""
	}
	namespace := options.SessionNamespace
	save := func(state internal.SessionState) error {
		return internal.SaveEmbeddedSession(state, env, namespace)
	}
	inner, err := internal.NewRuntime(internal.RuntimeOptions{
		Paths: paths, Env: env, Session: session,
		Styler:               term.NewStyler(paths.Theme, paths.Colors),
		Embedded:             true,
		SuppressFooter:       options.SuppressFooter,
		SuppressKeyHints:     options.SuppressKeyHints,
		SuppressViewKeyHints: options.SuppressViewKeyHints,
		SuppressQuit:         options.SuppressQuit,
		SaveSession:          save,
	})
	if err != nil {
		return nil, err
	}
	return &Model{
		inner: inner, env: env, namespace: namespace,
		warnings: append([]string{}, paths.Warnings...),
	}, nil
}

func validView(view View) bool {
	for _, candidate := range []View{ViewAgenda, ViewNext, ViewQuadrants, ViewInbox, ViewProjects, ViewOutline} {
		if view == candidate {
			return true
		}
	}
	return false
}

// overlayStrings layers host slots over the user's configured slots. The base
// map is never mutated: it belongs to the resolved configuration.
func overlayStrings(base, over map[string]string) map[string]string {
	merged := cloneStrings(base)
	for key, value := range over {
		merged[key] = value
	}
	return merged
}

func cloneStrings(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// Init starts the Tasks file-watch tick and loads the first read model.
func (m *Model) Init() tea.Cmd { return m.inner.Init() }

// Update passes a Bubble Tea v2 message into Tasks. Nested quit never returns
// tea.Quit; use QuitRequested to observe a non-suppressed request.
func (m *Model) Update(message tea.Msg) (*Model, tea.Cmd) {
	_, cmd := m.inner.Update(message)
	return m, cmd
}

// CommandAvailable evaluates one exported command against the model's current
// focus and live selection. It distinguishes conditional commands that share a
// default key.
func (m *Model) CommandAvailable(commandID string) (bool, error) {
	return m.inner.CommandAvailable(commandID)
}

// Invoke executes one available exported command by stable ID, without
// replaying a possibly ambiguous default key.
func (m *Model) Invoke(commandID string) (tea.Cmd, error) {
	return m.inner.InvokeCommand(commandID)
}

// View renders Tasks at the host's allotted size without alternate-screen or
// mouse-mode ownership leaking into the host.
func (m *Model) View(width, height int) string {
	m.inner.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m.inner.Render()
}

// Close saves the host-specific session and shuts down the Tasks agent queue.
// It is the correct call for the model the host actually presented, and it is
// safe to call more than once.
//
// Close and Discard share one guard: the FIRST of the two to run wins and the
// other becomes a no-op returning that first call's error. A host must never
// rely on calling both to mean anything.
func (m *Model) Close() error {
	m.closeOnce.Do(func() {
		m.closeErr = internal.SaveEmbeddedSession(m.inner.SessionState(), m.env, m.namespace)
		m.shutdown()
	})
	return m.closeErr
}

// Discard releases a model's resources WITHOUT persisting its session.
//
// It exists because every model a host builds for one namespace shares a single
// session file. A host that builds models speculatively — a project switch, an
// epoch race, a build that lands after the user moved on — must still shut the
// agent queue of the model it throws away, but calling Close there would write
// the discarded model's stale view state over the live model's session. Discard
// is the call for a model that was never presented, or whose state the host has
// decided not to keep.
//
// It shuts the agent queue and provider processes exactly as Close does, and it
// shares Close's guard: whichever runs first wins, each is idempotent, and the
// two are mutually exclusive.
func (m *Model) Discard() error {
	m.closeOnce.Do(func() { m.shutdown() })
	return m.closeErr
}

func (m *Model) shutdown() {
	if queue := m.inner.Queue(); queue != nil {
		queue.Shutdown()
	}
}

// FocusContext is the stable Tasks interaction context for host key routing.
func (m *Model) FocusContext() FocusContext { return hostFocusContext(m.inner.FocusContext()) }

// VisibleSpatialFocusStops reports Tasks' visible list/detail focus ring in
// visual order, with geometry from the same sampled layout used to render.
func (m *Model) VisibleSpatialFocusStops() []SpatialFocusStop {
	internalStops := m.inner.VisibleSpatialFocusStops()
	stops := make([]SpatialFocusStop, 0, len(internalStops))
	for _, stop := range internalStops {
		stops = append(stops, SpatialFocusStop{
			ID: SpatialFocus(stop.ID),
			Rect: Rect{
				X: stop.Rect.X, Y: stop.Rect.Y,
				Width: stop.Rect.Width, Height: stop.Rect.Height,
			},
		})
	}
	return stops
}

// CurrentSpatialFocus reports the current visible list/detail stop.
func (m *Model) CurrentSpatialFocus() SpatialFocus {
	return SpatialFocus(m.inner.CurrentSpatialFocus())
}

// SetSpatialFocus focuses one visible stop directly. It refuses unknown,
// hidden, or currently input/overlay-owned targets without changing focus.
func (m *Model) SetSpatialFocus(focus SpatialFocus) bool {
	return m.inner.SetSpatialFocus(internal.SpatialFocus(focus))
}

// TabOwnsFocus reports whether an input or overlay must receive Tab. A host
// with an outer focus ring may intercept Tab only when this returns false.
func (m *Model) TabOwnsFocus() bool {
	return m.inner.TabOwnsFocus() || focusContextOwnsTab(m.FocusContext())
}

func focusContextOwnsTab(context FocusContext) bool {
	switch context {
	case FocusList, FocusDetail, FocusResponse, FocusResponseDetail:
		return false
	default:
		return true
	}
}

// ConsumesTextInput reports whether printable keys belong to Tasks input.
func (m *Model) ConsumesTextInput() bool { return m.inner.ConsumesTextInput() }

// CurrentView reports the active Tasks view.
func (m *Model) CurrentView() View { return View(m.inner.CurrentView()) }

// Contexts reports the current presentation-only context filters.
func (m *Model) Contexts() []string { return m.inner.ContextFilters() }

// QuitRequested reports a nested quit request for the host to handle.
func (m *Model) QuitRequested() bool { return m.inner.QuitRequested() }

// ClearQuitRequest acknowledges a nested quit request without closing Tasks.
func (m *Model) ClearQuitRequest() { m.inner.ClearQuitRequest() }

// LoadError reports a real failure to read the task store, as of the most
// recent Tasks read. It is nil when Tasks read the store successfully — which
// includes a store that is genuinely empty, and a store file that does not
// exist yet inside an existing tasks directory (a first run).
//
// It is non-nil when the store cannot be read or cannot be trusted: unreadable
// permissions, a path that is a directory, a file that is not valid UTF-8 or
// not valid Tasks JSONL, or a tasks directory that does not exist at all. A
// host uses it to distinguish "Tasks is fine and there is nothing to do" from
// "Tasks could not read anything" and to render its own diagnostic; Tasks also
// renders its own banner in the non-suppressed footer.
//
// The value only becomes meaningful once the model has performed a read, so
// call it after Init's command has run or after any Update.
func (m *Model) LoadError() error { return m.inner.ReadError() }

// Warnings returns non-fatal messages produced by normal Tasks configuration.
func (m *Model) Warnings() []string { return append([]string{}, m.warnings...) }
