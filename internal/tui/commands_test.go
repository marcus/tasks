package tui

import (
	"strings"
	"testing"
)

func TestConditionalSameKeyCommandsHaveIndependentAvailabilityAndInvocation(t *testing.T) {
	normal := newModelHarness(t, harnessOptions{})
	normal.model.SwitchView(ViewNext)
	normal.selectRowByID(fixFlight) // deadline makes recurrence available
	if available, _ := normal.model.CommandAvailable("reject-proposal"); available {
		t.Fatal("reject was available on a normal task")
	}
	if available, err := normal.model.CommandAvailable("open-recur-popup"); err != nil || !available {
		t.Fatalf("recur availability=%v err=%v", available, err)
	}
	if _, err := normal.model.InvokeCommand("reject-proposal"); err == nil {
		t.Fatal("unavailable reject invoked")
	}
	if _, err := normal.model.InvokeCommand("open-recur-popup"); err != nil || normal.model.Form() == nil || normal.model.Form().Kind != QuickFormRecurrence {
		t.Fatalf("invoke recur mode=%s form=%v err=%v", normal.model.Mode(), normal.model.Form(), err)
	}

	proposalStore := strings.Replace(fixtureStore,
		`"state":"NEXT","priority":"A","title":"Book flight`,
		`"state":"PROPOSED","priority":"A","title":"Book flight`, 1)
	proposal := newModelHarness(t, harnessOptions{live: proposalStore})
	proposal.model.SwitchView(ViewInbox)
	proposal.selectRowByID(fixFlight)
	if available, _ := proposal.model.CommandAvailable("reject-proposal"); !available {
		t.Fatal("reject was unavailable on proposal")
	}
	if available, _ := proposal.model.CommandAvailable("open-recur-popup"); available {
		t.Fatal("recur was available on proposal")
	}
	if _, err := proposal.model.InvokeCommand("reject-proposal"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(proposal.content(), `"id":"aaaa0004","parent":"aaaa0003","state":"CANCELLED"`) {
		t.Fatal("invoked reject did not reject the selected proposal")
	}
}

func TestProjectDropAndReopenCommandsConfirmThenMutate(t *testing.T) {
	drop := newModelHarness(t, harnessOptions{live: projectsDoubleNestedStore})
	drop.model.SwitchView(ViewProjects)
	drop.selectRowByID("aaaa0002")
	if available, err := drop.model.CommandAvailable("drop-project"); err != nil || !available {
		t.Fatalf("drop availability=%v err=%v", available, err)
	}
	if _, err := drop.model.InvokeCommand("drop-project"); err != nil {
		t.Fatal(err)
	}
	if drop.model.Modal() == nil || drop.model.Modal().Kind() != ModalProjectDropConfirm {
		t.Fatalf("drop modal = %#v", drop.model.Modal())
	}
	drop.pressKeys("y")
	dropped, found := drop.model.ReadModel().Queries().ProjectView("aaaa0002")
	if !found || dropped.State != "CANCELLED" || !dropped.HasClosed {
		t.Fatalf("dropped project = %+v found=%v", dropped, found)
	}
	var droppedTaskState string
	for _, item := range drop.model.ReadModel().Queries().LiveItems() {
		if item.ID == "bbbb0001" {
			droppedTaskState = item.State
		}
	}
	if droppedTaskState != "CANCELLED" {
		t.Fatalf("dropped task state = %q", droppedTaskState)
	}

	reopen := newModelHarness(t, harnessOptions{live: closedProjectLifecycleStore})
	reopen.model.SwitchView(ViewProjects)
	reopen.model.ToggleClosed()
	reopen.selectRowByID("aaaa0002")
	reopen.pressKeys("r")
	if reopen.model.Modal() == nil || reopen.model.Modal().Kind() != ModalProjectReopenConfirm {
		t.Fatalf("reopen modal = %#v", reopen.model.Modal())
	}
	stillClosed, _ := reopen.model.ReadModel().Queries().ProjectView("aaaa0002")
	if !stillClosed.HasClosed {
		t.Fatal("reopen mutated before confirmation")
	}
	reopen.pressKeys("y")
	reopened, found := reopen.model.ReadModel().Queries().ProjectView("aaaa0002")
	if !found || reopened.HasClosed || reopened.State != "" || reopened.Closed != "" {
		t.Fatalf("reopened project = %+v found=%v", reopened, found)
	}
}

func TestDirectWidgetKeyAndCommandInvocationAgree(t *testing.T) {
	direct := newModelHarness(t, harnessOptions{})
	direct.pressKeys("/")
	direct.pressKeys("garden", "\r")

	invoked := newModelHarness(t, harnessOptions{})
	invoked.pressKeys("/", "garden")
	if available, err := invoked.model.CommandAvailable("filter-apply"); err != nil || !available {
		t.Fatalf("filter apply availability=%v err=%v", available, err)
	}
	if _, err := invoked.model.InvokeCommand("filter-apply"); err != nil {
		t.Fatal(err)
	}
	if direct.model.Mode() != invoked.model.Mode() || direct.model.filter != invoked.model.filter || len(direct.model.Rows()) != len(invoked.model.Rows()) {
		t.Fatalf("direct mode/filter/rows=%s/%q/%d invoke=%s/%q/%d",
			direct.model.Mode(), direct.model.filter, len(direct.model.Rows()),
			invoked.model.Mode(), invoked.model.filter, len(invoked.model.Rows()))
	}

	directPrompt := newModelHarness(t, harnessOptions{})
	directPrompt.pressKeys("\t", "\t")
	invokedPrompt := newModelHarness(t, harnessOptions{})
	invokedPrompt.pressKeys("\t")
	if _, err := invokedPrompt.model.InvokeCommand("prompt-close"); err != nil {
		t.Fatal(err)
	}
	if directPrompt.model.Mode() != invokedPrompt.model.Mode() {
		t.Fatalf("direct prompt mode=%s invoke=%s", directPrompt.model.Mode(), invokedPrompt.model.Mode())
	}
}

func TestModalConfirmationCommandsMatchDirectDispatch(t *testing.T) {
	tests := []struct {
		name      string
		open      func(*modelHarness)
		directKey string
	}{
		{
			name: "project complete return",
			open: func(h *modelHarness) {
				h.model.SwitchView(ViewProjects)
				if !selectFirstProject(h) {
					t.Fatal("fixture has no project")
				}
				h.pressKeys("c")
			},
			directKey: "\r",
		},
		{
			name: "project archive return",
			open: func(h *modelHarness) {
				h.model.SwitchView(ViewProjects)
				if !selectFirstProject(h) {
					t.Fatal("fixture has no project")
				}
				h.pressKeys("x")
			},
			directKey: "\r",
		},
		{
			name: "archive sweep",
			open: func(h *modelHarness) {
				h.model.SwitchView(ViewNext)
				h.pressKeys("x")
			},
			directKey: "y",
		},
		{
			name: "delete",
			open: func(h *modelHarness) {
				h.model.SwitchView(ViewNext)
				h.selectRowByID(fixPR)
				h.pressKeys("#")
			},
			directKey: "y",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			direct := newModelHarness(t, harnessOptions{})
			invoked := newModelHarness(t, harnessOptions{})
			test.open(direct)
			test.open(invoked)
			commandID := "modal-confirm"
			if test.directKey == "\r" {
				commandID = "modal-confirm-default"
			}
			if available, err := invoked.model.CommandAvailable(commandID); err != nil || !available {
				t.Fatalf("confirm availability=%v err=%v", available, err)
			}
			if available, _ := invoked.model.CommandAvailable("close-modal"); available {
				t.Fatal("ordinary close was available on a confirmation")
			}
			direct.pressKeys(test.directKey)
			if _, err := invoked.model.InvokeCommand(commandID); err != nil {
				t.Fatal(err)
			}
			if direct.content() != invoked.content() || direct.model.Mode() != invoked.model.Mode() ||
				direct.model.FlashMessage() != invoked.model.FlashMessage() {
				t.Fatalf("direct and invoke differ: mode=%s/%s flash=%q/%q",
					direct.model.Mode(), invoked.model.Mode(),
					direct.model.FlashMessage(), invoked.model.FlashMessage())
			}
		})
	}
}

func TestModalCancelAndOrdinaryCloseAreDistinctCommands(t *testing.T) {
	direct := newModelHarness(t, harnessOptions{})
	invoked := newModelHarness(t, harnessOptions{})
	for _, h := range []*modelHarness{direct, invoked} {
		h.model.SwitchView(ViewNext)
		h.selectRowByID(fixPR)
		h.pressKeys("#")
	}
	if available, err := invoked.model.CommandAvailable("modal-cancel"); err != nil || !available {
		t.Fatalf("cancel availability=%v err=%v", available, err)
	}
	direct.pressKeys("n")
	if _, err := invoked.model.InvokeCommand("modal-cancel"); err != nil {
		t.Fatal(err)
	}
	if direct.content() != invoked.content() || direct.model.FlashMessage() != invoked.model.FlashMessage() {
		t.Fatal("direct and invoked cancellation differ")
	}

	ordinary := newModelHarness(t, harnessOptions{})
	ordinary.model.OpenHelp()
	if available, _ := ordinary.model.CommandAvailable("modal-confirm"); available {
		t.Fatal("confirm was available on an ordinary modal")
	}
	if available, _ := ordinary.model.CommandAvailable("modal-cancel"); available {
		t.Fatal("confirmation cancel was available on an ordinary modal")
	}
	if available, err := ordinary.model.CommandAvailable("close-modal"); err != nil || !available {
		t.Fatalf("ordinary close availability=%v err=%v", available, err)
	}
}

func TestReturnDoesNotCloseConfirmationsThatRequireExplicitYes(t *testing.T) {
	tests := []struct {
		name string
		open func(*modelHarness)
	}{
		{
			name: "archive sweep",
			open: func(h *modelHarness) {
				h.model.SwitchView(ViewNext)
				h.pressKeys("x")
			},
		},
		{
			name: "delete",
			open: func(h *modelHarness) {
				h.model.SwitchView(ViewNext)
				h.selectRowByID(fixPR)
				h.pressKeys("#")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newModelHarness(t, harnessOptions{})
			test.open(h)
			kind := h.model.Modal().Kind()
			h.pressKeys("\r")
			if h.model.Modal() == nil || h.model.Modal().Kind() != kind {
				t.Fatal("Return closed or confirmed an explicit-yes modal")
			}
			if available, _ := h.model.CommandAvailable("close-modal"); available {
				t.Fatal("close-modal available on explicit-yes confirmation")
			}
			if available, _ := h.model.CommandAvailable("modal-confirm-default"); available {
				t.Fatal("Return confirmation available on explicit-yes confirmation")
			}
			if _, err := h.model.InvokeCommand("close-modal"); err == nil {
				t.Fatal("close-modal invoked on explicit-yes confirmation")
			}
		})
	}
}

func TestQueuedAgentConfirmationCommandMatchesDirectDispatch(t *testing.T) {
	newQueued := func() *agentHarness {
		h := newAgentHarness(t,
			&fakeAdapter{available: true, chunks: 99, output: "running"},
			scripted("second", true))
		h.submit("one")
		h.submit("two")
		h.model.CancelQueuedAgentRequests()
		return h
	}
	direct, invoked := newQueued(), newQueued()
	direct.pressKeys("\r")
	if available, err := invoked.model.CommandAvailable("modal-confirm-default"); err != nil || !available {
		t.Fatalf("confirm availability=%v err=%v", available, err)
	}
	if _, err := invoked.model.InvokeCommand("modal-confirm-default"); err != nil {
		t.Fatal(err)
	}
	if direct.model.pendingCount() != invoked.model.pendingCount() ||
		direct.model.FlashMessage() != invoked.model.FlashMessage() {
		t.Fatal("direct and invoked queue confirmation differ")
	}
}

func TestResponseFocusPreservesListAndDetailDispatchPrecedence(t *testing.T) {
	listDirect := newModelHarness(t, harnessOptions{})
	listInvoke := newModelHarness(t, harnessOptions{})
	for _, h := range []*modelHarness{listDirect, listInvoke} {
		h.model.respOpen = true
		h.model.resp = []string{"done"}
	}
	if got := listInvoke.model.FocusContext(); got != "response" {
		t.Fatalf("list response context=%q", got)
	}
	if available, _ := listInvoke.model.CommandAvailable("start-task-edit"); available {
		t.Fatal("detail edit available on list-origin response")
	}
	listDirect.pressKeys("/")
	if _, err := listInvoke.model.InvokeCommand("start-filter"); err != nil {
		t.Fatal(err)
	}
	if listDirect.model.Mode() != listInvoke.model.Mode() {
		t.Fatalf("list response direct/invoke mode=%s/%s", listDirect.model.Mode(), listInvoke.model.Mode())
	}

	detailDirect := newModelHarness(t, harnessOptions{})
	detailInvoke := newModelHarness(t, harnessOptions{})
	for _, h := range []*modelHarness{detailDirect, detailInvoke} {
		h.model.SwitchView(ViewNext)
		h.selectRowByID(fixFlight)
		h.pressKeys("\r", "\t")
		h.model.respOpen = true
		h.model.resp = []string{"done"}
		h.model.mode = ModeList
	}
	if got := detailInvoke.model.FocusContext(); got != "response_detail" {
		t.Fatalf("detail response context=%q", got)
	}
	if available, err := detailInvoke.model.CommandAvailable("start-task-edit"); err != nil || !available {
		t.Fatalf("detail edit availability=%v err=%v", available, err)
	}
	detailDirect.pressKeys("e")
	if _, err := detailInvoke.model.InvokeCommand("start-task-edit"); err != nil {
		t.Fatal(err)
	}
	if detailDirect.model.Mode() != ModeTaskEdit || detailInvoke.model.Mode() != ModeTaskEdit {
		t.Fatalf("detail response direct/invoke mode=%s/%s", detailDirect.model.Mode(), detailInvoke.model.Mode())
	}
}
