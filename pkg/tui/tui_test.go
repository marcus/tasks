package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func embeddedFixture(t *testing.T, options EmbeddedOptions) (*Model, string) {
	t.Helper()
	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "valid", "small-gtd", "store", "tasks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks.jsonl"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	options.SessionNamespace = "fixture-host"
	options.Environment = map[string]string{
		"TASKS_DIR": dir, "XDG_STATE_HOME": state, "XDG_CONFIG_HOME": filepath.Join(dir, "config"),
		"HOME": dir, "PATH": filepath.Join(dir, "no-provider-bin"),
	}
	model, err := NewEmbedded(options)
	if err != nil {
		t.Fatal(err)
	}
	return model, state
}

func key(text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(text)[0], Text: text}
}

func TestEmbeddedInitialPresentationAndHostSizedView(t *testing.T) {
	model, _ := embeddedFixture(t, EmbeddedOptions{
		InitialView: ViewNext, InitialContexts: []string{"home"}, SuppressFooter: true,
	})
	defer model.Close()
	model.Init()
	if model.CurrentView() != ViewNext || strings.Join(model.Contexts(), ",") != "@home" {
		t.Fatalf("initial view=%q contexts=%v", model.CurrentView(), model.Contexts())
	}
	frame := model.View(60, 18)
	if !strings.Contains(frame, "Measure the existing slab") || strings.Contains(frame, "navigate") {
		t.Fatalf("host-sized/footer-suppressed frame:\n%s", frame)
	}
	if got := len(strings.Split(frame, "\n")); got != 18 {
		t.Fatalf("frame height=%d, want 18", got)
	}
}

func TestEmbeddedQuitLatchesForTheHostWithoutTeaQuit(t *testing.T) {
	model, _ := embeddedFixture(t, EmbeddedOptions{})
	model.Init()
	_, cmd := model.Update(key("q"))
	if cmd != nil || !model.QuitRequested() {
		t.Fatalf("surfaced quit cmd=%v requested=%v", cmd, model.QuitRequested())
	}
	if frame := model.View(50, 14); frame == "" {
		t.Fatal("nested quit blanked the host view")
	}
	model.ClearQuitRequest()
	if model.QuitRequested() {
		t.Fatal("quit acknowledgement was ignored")
	}
	if err := model.Close(); err != nil {
		t.Fatal(err)
	}

	// SuppressQuit means the HOST owns the affordance, not that Tasks refuses:
	// the request must still latch, or QuitRequested is unreachable API.
	suppressed, _ := embeddedFixture(t, EmbeddedOptions{SuppressQuit: true})
	defer suppressed.Close()
	suppressed.Init()
	_, cmd = suppressed.Update(key("q"))
	if cmd != nil || !suppressed.QuitRequested() {
		t.Fatalf("suppressed quit cmd=%v requested=%v", cmd, suppressed.QuitRequested())
	}
	if frame := suppressed.View(80, 14); strings.Contains(frame, "managed by the host") {
		t.Fatalf("suppressed quit still flashes a refusal:\n%s", frame)
	}
	if frame := suppressed.View(80, 14); strings.Contains(frame, "q quit") {
		t.Fatalf("suppressed quit still advertises Tasks' own affordance:\n%s", frame)
	}
	suppressed.ClearQuitRequest()
	if suppressed.QuitRequested() {
		t.Fatal("suppressed quit acknowledgement was ignored")
	}
	if _, cmd = suppressed.Update(key("q")); !suppressed.QuitRequested() {
		t.Fatal("a second suppressed quit did not re-latch")
	}
}

func TestEmbeddedFocusAndHostSessionStaySeparate(t *testing.T) {
	model, state := embeddedFixture(t, EmbeddedOptions{})
	model.Init()
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if model.FocusContext() != FocusPrompt || !model.ConsumesTextInput() {
		t.Fatalf("focus=%q consumes=%v", model.FocusContext(), model.ConsumesTextInput())
	}
	model.Update(key("draft"))
	if err := model.Close(); err != nil {
		t.Fatal(err)
	}
	hostPath := filepath.Join(state, "tasks", "hosts", "fixture-host", "tui.json")
	if _, err := os.Stat(hostPath); err != nil {
		t.Fatalf("host session: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "tasks", "tui.json")); !os.IsNotExist(err) {
		t.Fatalf("standalone session was touched: %v", err)
	}
}

func TestEmbeddedSpatialFocusContractAndStandaloneTab(t *testing.T) {
	model, _ := embeddedFixture(t, EmbeddedOptions{InitialView: ViewNext, SuppressFooter: true})
	defer model.Close()
	model.Init()
	model.View(100, 24)

	stops := model.VisibleSpatialFocusStops()
	if len(stops) != 1 || stops[0] != (SpatialFocusStop{
		ID: SpatialFocusList, Rect: Rect{X: 2, Y: 2, Width: 96, Height: 21},
	}) {
		t.Fatalf("initial stops = %#v", stops)
	}
	if model.SetSpatialFocus(SpatialFocusDetail) || model.SetSpatialFocus(SpatialFocus("bogus")) {
		t.Fatal("invalid or hidden direct focus was accepted")
	}

	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	stops = model.VisibleSpatialFocusStops()
	if len(stops) != 2 || stops[0] != (SpatialFocusStop{
		ID: SpatialFocusList, Rect: Rect{X: 2, Y: 2, Width: 58, Height: 21},
	}) || stops[1] != (SpatialFocusStop{
		ID: SpatialFocusDetail, Rect: Rect{X: 62, Y: 2, Width: 36, Height: 21},
	}) {
		t.Fatalf("detail stops = %#v", stops)
	}
	if model.CurrentSpatialFocus() != SpatialFocusDetail || model.FocusContext() != FocusDetail {
		t.Fatalf("current=%q context=%q", model.CurrentSpatialFocus(), model.FocusContext())
	}

	// This is intentionally NOT an embedded-only behavior switch. If a host
	// does not intercept Tab, Tasks still opens Ask exactly as standalone does,
	// from either passive spatial stop.
	if model.TabOwnsFocus() {
		t.Fatal("passive detail unexpectedly owns Tab")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if model.FocusContext() != FocusPrompt || !model.TabOwnsFocus() {
		t.Fatalf("detail Tab focus=%q owns=%v", model.FocusContext(), model.TabOwnsFocus())
	}
	if model.SetSpatialFocus(SpatialFocusList) {
		t.Fatal("direct spatial focus reached behind prompt input")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if model.FocusContext() != FocusDetail {
		t.Fatalf("closing prompt lost detail focus: %q", model.FocusContext())
	}
	if !model.SetSpatialFocus(SpatialFocusList) || model.FocusContext() != FocusList {
		t.Fatalf("direct list focus=%q context=%q", model.CurrentSpatialFocus(), model.FocusContext())
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if model.FocusContext() != FocusPrompt || !model.TabOwnsFocus() {
		t.Fatalf("list Tab focus=%q owns=%v", model.FocusContext(), model.TabOwnsFocus())
	}
}

func TestEveryExportedContextHasConservativeTabOwnership(t *testing.T) {
	nonOwning := map[FocusContext]bool{
		FocusList: true, FocusDetail: true, FocusResponse: true, FocusResponseDetail: true,
	}
	seen := map[FocusContext]bool{}
	for _, context := range ExportContexts() {
		seen[context.Name] = true
		if got, want := focusContextOwnsTab(context.Name), !nonOwning[context.Name]; got != want {
			t.Errorf("context %q owns Tab=%v, want %v", context.Name, got, want)
		}
		if context.ConsumesTextInput && !focusContextOwnsTab(context.Name) {
			t.Errorf("text input context %q yielded Tab to its host", context.Name)
		}
	}
	for _, context := range []FocusContext{
		FocusList, FocusDetail, FocusTaskEdit, FocusModal, FocusModalFilter,
		FocusForm, FocusFieldModal, FocusPicker, FocusContextPicker, FocusFilter, FocusPrompt,
		FocusResponse, FocusResponseDetail, FocusAgentActivity, FocusAgentActivityFilter,
	} {
		if !seen[context] {
			t.Errorf("missing ownership assertion for %q", context)
		}
	}
}

func TestEmbeddedRejectsUnsafeNamespaceAndUnconfiguredStore(t *testing.T) {
	if _, err := NewEmbedded(EmbeddedOptions{SessionNamespace: "../sidecar"}); err == nil {
		t.Fatal("unsafe namespace accepted")
	}
	if _, err := NewEmbedded(EmbeddedOptions{
		SessionNamespace: "host",
		Environment:      map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()},
	}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unconfigured error=%v", err)
	}
}

func TestPublicMetadataUsesTasksContextsAndCompleteCommands(t *testing.T) {
	contexts := map[FocusContext]bool{}
	for _, context := range ExportContexts() {
		contexts[context.Name] = true
	}
	for _, wanted := range []FocusContext{
		FocusList, FocusDetail, FocusTaskEdit, FocusModal, FocusModalFilter,
		FocusForm, FocusFieldModal, FocusPicker, FocusContextPicker, FocusPrompt, FocusResponse,
		FocusResponseDetail,
		FocusAgentActivity, FocusAgentActivityFilter,
	} {
		if !contexts[wanted] {
			t.Errorf("missing context %q", wanted)
		}
	}
	for _, command := range ExportCommands() {
		if !strings.HasPrefix(string(command.Context), "tasks-") || command.ID == "" ||
			command.FooterLabel == "" || command.Description == "" || command.FooterPriority <= 0 {
			t.Fatalf("invalid public command: %#v", command)
		}
	}
	if len(ExportBindings()) == 0 {
		t.Fatal("no public bindings")
	}
}
