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
