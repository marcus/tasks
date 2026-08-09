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
