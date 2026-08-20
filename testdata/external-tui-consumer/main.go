package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	taskstui "github.com/marcus/tasks/pkg/tui"
)

func main() {
	bindings, commands, contexts := taskstui.ExportBindings(), taskstui.ExportCommands(), taskstui.ExportContexts()
	if len(bindings) == 0 || len(commands) == 0 || len(contexts) < 10 {
		fail(fmt.Errorf("incomplete exported metadata: %d bindings, %d commands, %d contexts",
			len(bindings), len(commands), len(contexts)))
	}
	if commands[0].ID == "" || commands[0].FooterLabel == "" ||
		len(commands[0].DefaultBindings) == 0 {
		fail(fmt.Errorf("incomplete first command: %#v", commands[0]))
	}
	contextSeen := map[taskstui.FocusContext]bool{}
	for _, context := range contexts {
		contextSeen[context.Name] = true
	}
	if !contextSeen[taskstui.FocusResponse] || !contextSeen[taskstui.FocusResponseDetail] {
		fail(fmt.Errorf("response contexts missing from metadata: %#v", contextSeen))
	}
	commandSeen := map[string]bool{}
	for _, command := range commands {
		if command.Context == taskstui.FocusModal {
			commandSeen[command.ID] = true
		}
	}
	if !commandSeen["modal-confirm"] || !commandSeen["modal-confirm-default"] ||
		!commandSeen["modal-cancel"] || !commandSeen["close-modal"] {
		fail(fmt.Errorf("semantic modal commands missing: %#v", commandSeen))
	}
	model, err := taskstui.NewEmbedded(taskstui.EmbeddedOptions{
		SessionNamespace: "external-proof",
		InitialView:      taskstui.ViewNext,
		InitialContexts:  []string{"home"},
		SuppressFooter:   true,
	})
	if err != nil {
		fail(err)
	}
	model.Init()
	model.View(72, 20)
	stops := model.VisibleSpatialFocusStops()
	if len(stops) != 1 || stops[0].ID != taskstui.SpatialFocusList || stops[0].Rect.Width <= 0 {
		fail(fmt.Errorf("invalid initial spatial focus stops: %#v", stops))
	}
	if model.SetSpatialFocus(taskstui.SpatialFocusDetail) || model.TabOwnsFocus() {
		fail(fmt.Errorf("hidden detail or passive list focus contract is wrong"))
	}
	if available, err := model.CommandAvailable("view-agenda"); err != nil || !available {
		fail(fmt.Errorf("view-agenda availability=%v err=%v", available, err))
	}
	if _, err := model.Invoke("view-agenda"); err != nil || model.CurrentView() != taskstui.ViewAgenda {
		fail(fmt.Errorf("invoke view-agenda: view=%s err=%v", model.CurrentView(), err))
	}
	if _, err := model.Invoke("view-next"); err != nil {
		fail(fmt.Errorf("invoke view-next: %v", err))
	}
	model.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	frame := model.View(72, 20)
	if !strings.Contains(frame, "Measure the existing slab") || len(strings.Split(frame, "\n")) != 20 {
		fail(fmt.Errorf("unexpected composed frame"))
	}
	model.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !model.QuitRequested() {
		fail(fmt.Errorf("quit was not surfaced from focus %q", model.FocusContext()))
	}
	model.ClearQuitRequest()
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !model.TabOwnsFocus() || model.SetSpatialFocus(taskstui.SpatialFocusList) {
		fail(fmt.Errorf("prompt did not retain Tab/direct focus ownership"))
	}
	model.Update(tea.KeyPressMsg{Code: 'x', Text: "external prompt"})
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	pid := waitForPID(os.Getenv("FAKE_PID_FILE"))
	if err := model.Close(); err != nil {
		fail(err)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		fail(fmt.Errorf("provider process %d survived Close", pid))
	}
	if err := model.Close(); err != nil {
		fail(fmt.Errorf("second close: %w", err))
	}
	fmt.Println("external consumer: constructed, drove keys, rendered, saved, closed")
}

func waitForPID(path string) int {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	fail(fmt.Errorf("provider did not start"))
	return 0
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
