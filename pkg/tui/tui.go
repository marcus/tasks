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
	FocusPicker              FocusContext = "tasks-picker"
	FocusContextPicker       FocusContext = "tasks-context-picker"
	FocusFilter              FocusContext = "tasks-filter"
	FocusPrompt              FocusContext = "tasks-prompt"
	FocusResponse            FocusContext = "tasks-response"
	FocusResponseDetail      FocusContext = "tasks-response-detail"
	FocusAgentActivity       FocusContext = "tasks-agent-activity"
	FocusAgentActivityFilter FocusContext = "tasks-agent-activity-filter"
)

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

// ExportCommands projects the same registry for host palettes and footers.
func ExportCommands() []Command {
	exported := shortcuts.ExportCommands()
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
type ThemeOptions struct {
	Name   string
	Colors map[string]string
}

// EmbeddedOptions configures a host-owned Tasks component. SessionNamespace is
// required so embedding can never overwrite the standalone tasks-tui session.
// Environment is primarily a deterministic/test seam; nil snapshots os.Environ.
type EmbeddedOptions struct {
	SessionNamespace string
	InitialView      View
	InitialContexts  []string
	SuppressFooter   bool
	SuppressQuit     bool
	Theme            ThemeOptions
	Environment      map[string]string
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
	if options.Theme.Colors != nil {
		paths.Colors = cloneStrings(options.Theme.Colors)
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
		Styler:         term.NewStyler(paths.Theme, paths.Colors),
		Embedded:       true,
		SuppressFooter: options.SuppressFooter,
		SuppressQuit:   options.SuppressQuit,
		SaveSession:    save,
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
// It is safe to call more than once.
func (m *Model) Close() error {
	m.closeOnce.Do(func() {
		m.closeErr = internal.SaveEmbeddedSession(m.inner.SessionState(), m.env, m.namespace)
		if queue := m.inner.Queue(); queue != nil {
			queue.Shutdown()
		}
	})
	return m.closeErr
}

// FocusContext is the stable Tasks interaction context for host key routing.
func (m *Model) FocusContext() FocusContext { return hostFocusContext(m.inner.FocusContext()) }

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

// Warnings returns non-fatal messages produced by normal Tasks configuration.
func (m *Model) Warnings() []string { return append([]string{}, m.warnings...) }
