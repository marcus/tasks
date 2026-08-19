package tui

import (
	"sort"
	"strings"

	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
	"github.com/marcus/tasks/internal/tui/term/shortcuts"
)

// The delegation modal (`D`), the work-reference prompt (`W`), and the
// availability prompt (`z`).
//
// All three go through the application's delegation commands rather than
// composing store writes here — the WAITING default behind a human delegation
// and the blocker note behind a release are composed there, in one undo step,
// and duplicating that would give the TUI a second, subtly different delegation.
//
// `D` is a THREE-FIELD modal, not a word grammar. A delegation has three
// orthogonal parts — who holds it, what kind of work it is, and what the
// receiver is being asked to do — and the one-line prompt that used to carry
// all three made the SHORTEST inputs the most destructive ones: `o` and `n`
// revoked a live claim with no confirmation, and `i` delegated at `implement`.
// That grammar is retired rather than reproduced: the escape hatches are
// buttons that name themselves and their key, and the destructive one confirms.

// delegateClearWords are the spellings that mean "remove the reference".
var delegateClearWords = []string{"off", "none", "clear"}

// delegateAgentValue is the assignee field's spelling of "the agent pool". It
// is a RESERVED mode name and contains no "@", so neither a configured
// vocabulary nor an address can ever collide with it.
const delegateAgentValue = "agent"

// delegateRecentAssignees bounds the offered address list. It is an aid to
// picking, not a directory: typing an address nobody has used before is still
// the fastest path for a new person.
const delegateRecentAssignees = 8

// The delegate modal's field keys, its two extra affordances, and the
// confirmation the destructive one arms.
const (
	delegateFieldAssignee = "assignee"
	delegateFieldMode     = "mode"
	delegateFieldNote     = "note"

	delegateReleaseAction    = "release"
	delegateUndelegateAction = "undelegate"

	delegateUndelegateConfirm = "undelegate clears the delegation and revokes any live claim " +
		"— ctrl-x again confirms"
	delegateReleaseConfirm = "release forces a claimed task back to the pool, ending work " +
		"a worker may be mid-way through — ctrl-r again confirms"
)

// delegateConfirmations is the message each destructive affordance arms before
// it acts. BOTH of them arm one: this modal exists to retire a grammar where
// the shortest inputs performed the widest actions, and a button that revokes
// someone else's in-flight work on a single keystroke would put that hazard
// straight back — the more so for Release, whose key is the one a user's
// fingers already spend on `redo` everywhere else in the app. A first press
// that explains itself makes that collision harmless instead of expensive.
var delegateConfirmations = map[string]string{
	delegateReleaseAction:    delegateReleaseConfirm,
	delegateUndelegateAction: delegateUndelegateConfirm,
}

// describe is the ONE way a registry entry becomes text a user reads. Entries
// are a static catalogue built during init, so any of them may carry the
// delegation-mode placeholder; rendering one without filling it puts a literal
// "{modes}" on screen. Every path that shows an entry's own words — the help
// overlay, the palette, and these flashes — goes through a filler like this.
func (m *Model) describe(entry shortcuts.Entry) string {
	return shortcuts.WithModes(entry, m.app.DelegationModes()).Description
}

// delegationAction is what one delegate gesture asked for.
type delegationAction string

const (
	delegateHuman      delegationAction = "human"
	delegateAgent      delegationAction = "agent"
	delegateRelease    delegationAction = "release"
	delegateUndelegate delegationAction = "undelegate"
	delegateWorkRef    delegationAction = "work_ref"
)

// DelegateSelected is `D`.
//
// Every part of the delegation is stated in its own control and submitted as
// ONE application.DelegationCommand: the modal composes nothing, so who holds
// the task, in what mode, and what they were asked to do are one write and one
// undo step.
func (m *Model) DelegateSelected() {
	item, ok := m.delegationTarget()
	if !ok {
		return
	}
	id, title := item.ID, item.Title
	marker := delegationOf(item)

	var modal *FieldModal
	modal = NewFieldModal(FieldModalOptions{
		Kind:     FieldModalDelegate,
		Title:    "Delegate — " + m.delegationStateLabel(item),
		MinWidth: 72,
		TargetID: id, ReturnMode: ReturnList,
		SubmitLabel: "Delegate",
		Fields: []FieldSpec{
			{
				// One field, two ways to answer it: pick somebody this list has
				// handed work to before, or type an address that is new.
				Key: delegateFieldAssignee, Label: "Delegate to", Kind: FieldChoice,
				FreeText: true, VisibleOptions: 5,
				Hint:     "an email address, or " + delegateAgentValue + " for the agent pool",
				Initial:  delegateInitialAssignee(marker),
				Options:  func() []FieldOption { return m.delegateAssigneeOptions() },
				Validate: delegateAssigneeError,
			},
			{
				Key: delegateFieldMode, Label: "Mode", Kind: FieldChoice, VisibleOptions: 5,
				Hint:    "what kind of hand-off this is",
				Initial: delegateInitialMode(marker, m.app.DelegationModes()),
				// The vocabulary is read at PAINT time and never captured here:
				// a configured set is the store's, and the modal open over it
				// must offer what that store will actually accept.
				Options: func() []FieldOption { return delegateModeOptions(m.app.DelegationModes()) },
				Validate: func(value string) string {
					return delegateModeError(value, m.app.DelegationModes())
				},
			},
			{
				Key: delegateFieldNote, Label: "Note", Kind: FieldTextArea, Rows: 4,
				Hint:     "instructions for the receiver — how to work it, where the work lands",
				Initial:  marker[delegateFieldNote],
				Validate: delegateNoteError,
			},
		},
		Actions: []FieldModalAction{
			{ID: delegateReleaseAction, Label: "Release", Key: "\x12", KeyLabel: "ctrl-r"},
			{ID: delegateUndelegateAction, Label: "Undelegate", Key: "\x18", KeyLabel: "ctrl-x"},
		},
		Submit: func(values map[string]string) string {
			command := application.DelegationCommand{
				ID: id, Action: application.ActionDelegate,
				Mode: strings.TrimSpace(values[delegateFieldMode]),
				// SetNote is always true: this field IS the briefing, so
				// emptying it means "clear it" exactly as typing in it means
				// "write it", and a note left untouched submits itself back
				// unchanged.
				Note: values[delegateFieldNote], SetNote: true,
			}
			action := delegateAgent
			if to := strings.TrimSpace(values[delegateFieldAssignee]); to == delegateAgentValue {
				command.Kind = "agent"
			} else {
				command.Kind, command.Assignee = "human", to
				action = delegateHuman
			}
			return m.runDelegation(id, title, action, command)
		},
	})
	// The escape hatches are affordances, not words typed into a text field.
	// Release is the OWNER's forced release: this modal exists to clear a claim
	// the owner does not hold, and a worker releasing its own claim uses the CLI
	// with its worker id.
	modal.OnAction = func(actionID string) FieldModalOutcome {
		// Two deliberate gestures, the same shape as the discard latch and for
		// the same reason: these are the buttons that throw away someone else's
		// live work. Editing anything clears the message, which re-arms the
		// confirmation.
		if confirm, destructive := delegateConfirmations[actionID]; destructive {
			if modal.Error() != confirm {
				modal.SetError(confirm)
				return FieldModalOutcome{Result: FieldModalError}
			}
		}
		switch actionID {
		case delegateReleaseAction:
			return m.runDelegationFromModal(modal, id, title, delegateRelease,
				application.DelegationCommand{
					ID: id, Action: application.ActionRelease, Force: true})
		case delegateUndelegateAction:
			return m.runDelegationFromModal(modal, id, title, delegateUndelegate,
				application.DelegationCommand{ID: id, Action: application.ActionUndelegate})
		}
		return fieldModalHandled()
	}
	m.OpenFieldModal(modal)
}

// runDelegationFromModal runs one affordance and says what the modal does next:
// a refusal stays in the box, in the status row the paint always reserves, and
// a success closes it and runs the staged flash.
func (m *Model) runDelegationFromModal(modal *FieldModal, id, title string,
	action delegationAction, command application.DelegationCommand) FieldModalOutcome {

	if message := m.runDelegation(id, title, action, command); message != "" {
		modal.SetError(message)
		return FieldModalOutcome{Result: FieldModalError}
	}
	return FieldModalOutcome{Result: FieldModalSubmitted}
}

// delegateInitialAssignee prefills who holds the task now, so re-delegating
// starts from the delegation that exists rather than from a blank field.
func delegateInitialAssignee(marker map[string]string) string {
	// KIND is asked first, because an agent delegation's `assignee` is the
	// WORKER id of whoever claimed it, not a person: prefilling that would
	// offer to re-delegate the task to a worker's name.
	switch {
	case marker["kind"] == "agent":
		return delegateAgentValue
	case marker["assignee"] != "":
		return marker["assignee"]
	}
	return ""
}

func delegateInitialMode(marker map[string]string, modes record.ModeVocabulary) string {
	if mode := marker[delegateFieldMode]; mode != "" {
		return mode
	}
	if available := record.Modes(modes).Modes(); len(available) > 0 {
		return available[0]
	}
	return ""
}

func delegateModeOptions(modes record.ModeVocabulary) []FieldOption {
	out := []FieldOption{}
	for _, mode := range record.Modes(modes).Modes() {
		out = append(out, FieldOption{Value: mode, Label: mode})
	}
	return out
}

// delegateAssigneeOptions is the agent pool plus the people this list has handed
// work to before, most recent first. They are OFFERED, not demanded: the field
// is free text, so a new address is typed straight in.
func (m *Model) delegateAssigneeOptions() []FieldOption {
	out := []FieldOption{{Value: delegateAgentValue, Label: delegateAgentValue + " — the agent pool"}}
	for _, assignee := range m.recentAssignees(delegateRecentAssignees) {
		out = append(out, FieldOption{Value: assignee, Label: assignee})
	}
	return out
}

// recentAssignees reads the addresses off the live delegations, newest stamp
// first. It is a read of the list in front of the user rather than a directory:
// an address that survives only on an archived task is typed, not picked.
func (m *Model) recentAssignees(limit int) []string {
	newest := map[string]string{}
	for _, item := range m.read.Items() {
		marker := delegationOf(item)
		if marker["kind"] != "human" || marker["assignee"] == "" {
			continue
		}
		if marker["at"] > newest[marker["assignee"]] {
			newest[marker["assignee"]] = marker["at"]
		}
	}
	ordered := make([]string, 0, len(newest))
	for assignee := range newest {
		ordered = append(ordered, assignee)
	}
	sort.Slice(ordered, func(a, b int) bool {
		if newest[ordered[a]] != newest[ordered[b]] {
			return newest[ordered[a]] > newest[ordered[b]]
		}
		return ordered[a] < ordered[b]
	})
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	return ordered
}

// delegateAssigneeError is the field's own rule. A near-miss address is named as
// a typo here rather than reaching the user as a store refusal about a field
// they never knew they were writing.
func delegateAssigneeError(value string) string {
	text := strings.TrimSpace(value)
	switch {
	case text == "":
		return "name a person's email address, or " + delegateAgentValue + " for the agent pool"
	case text == delegateAgentValue:
		return ""
	case record.DelegationEmail(text):
		return ""
	case strings.Contains(text, "@"):
		return "“" + text + "” isn't an email address — use pat@example.com"
	}
	return "can't parse “" + text + "”; use an email address, or " +
		delegateAgentValue + " for the agent pool"
}

// delegateModeError refuses a mode the store would refuse, in the store's own
// vocabulary. The field is a choice over that vocabulary, so this fires when the
// configured set changed underneath an open modal — and in a test that says so.
func delegateModeError(value string, modes record.ModeVocabulary) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return "pick a mode — " + record.Modes(modes).Quoted()
	}
	if !record.Modes(modes).Valid(text) {
		return "“" + text + "” is not a configured mode — use " + record.Modes(modes).Quoted()
	}
	return ""
}

// delegateNoteError applies the record's own note rule, so the modal refuses
// exactly what the store would. An empty note is not an error: it is the
// instruction to carry no briefing at all.
func delegateNoteError(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	if problems := record.DelegationNoteErrors(value); len(problems) > 0 {
		return problems[0]
	}
	return ""
}

// SetWorkRefSelected is `W`.
//
// It refuses an undelegated task UP FRONT rather than opening a prompt whose
// every input must fail — the same shape as `r` refusing an undated task.
func (m *Model) SetWorkRefSelected() {
	item, ok := m.delegationTarget()
	if !ok {
		return
	}
	marker := delegationOf(item)
	if marker == nil {
		m.Flash("delegate the task first — a work reference belongs to a delegation")
		return
	}
	current := marker["work_ref"]
	suffix := "(none)"
	if current != "" {
		suffix = "(now " + current + ")"
	}
	id, title := item.ID, item.Title
	m.SetForm(NewQuickForm(QuickFormOptions{
		Kind: QuickFormWorkRef, Title: "Work reference", Prompt: "work ref",
		Hint: "url / ticket / session id · off clears · esc cancels", MinWidth: 60,
		ReturnMode: ReturnList, Initial: current, Suffix: suffix, TargetID: id,
		Submit: func(raw string) string {
			text := strings.TrimSpace(raw)
			if text == "" {
				return "can't parse “" + raw + "”; give a URL or id, or off to clear it"
			}
			reference := text
			if containsString(delegateClearWords, strings.ToLower(text)) {
				reference = ""
			}
			return m.runDelegation(id, title, delegateWorkRef, application.DelegationCommand{
				ID: id, Action: application.ActionWorkRef, WorkRef: reference,
			})
		},
	}))
	_ = m.SetMode(ModeForm)
}

// delegationTarget is the selected task, or nil after flashing why this row
// cannot be delegated.
func (m *Model) delegationTarget() (store.Item, bool) {
	if m.CurrentProject() != nil {
		m.needsTask()
		return store.Item{}, false
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return store.Item{}, false
	}
	fresh, found := m.read.TaskFor(item.ID)
	if !found {
		m.Flash("task no longer exists")
		return store.Item{}, false
	}
	if !isOpenState(fresh.State) {
		m.refuseDelegation()
		return store.Item{}, false
	}
	return fresh, true
}

// runDelegation is the shared submit body behind the modal and the work-ref
// prompt: run against a FROZEN task id, then stage the flash and reselect for
// after the popup closes.
func (m *Model) runDelegation(id, title string, action delegationAction,
	command application.DelegationCommand) string {

	var outcome application.Outcome
	switch command.Action {
	case application.ActionDelegate:
		outcome = m.app.DelegateTask(command, m.operation())
	case application.ActionUndelegate:
		outcome = m.app.UndelegateTask(command, m.operation())
	case application.ActionRelease:
		outcome = m.app.ReleaseTask(command, m.operation())
	case application.ActionWorkRef:
		outcome = m.app.SetWorkRef(command, m.operation())
	}
	if !(outcome.OK() || outcome.NoChange()) {
		message := delegationFailureMessage(outcome)
		m.Refresh()
		return message
	}
	message := delegationFlash(action, outcome, title)
	m.formSuccess(func() {
		m.Flash(message)
		m.Refresh()
		m.reselect(id)
	})
	return ""
}

// delegationFailureMessage surfaces the store's own sentences verbatim — they
// are already user-facing ("task is DONE; only accepted live tasks can be
// delegated") — and reserves the TUI's own wording for the two statuses that
// mean "something changed underneath you".
func delegationFailureMessage(outcome application.Outcome) string {
	if outcome.Conflict() && outcome.Delegation != nil && outcome.Delegation.Holder != "" {
		return "already claimed by " + outcome.Delegation.Holder +
			" at " + outcome.Delegation.At + " — Undelegate (ctrl-x) revokes it"
	}
	if outcome.Stale() {
		return "file changed underneath — reopen"
	}
	if outcome.NotFound() {
		return "task no longer exists"
	}
	return outcomeMessage(outcome, "could not delegate the task")
}

// delegationFlash is one vocabulary shared with the CLI's delegation headline,
// so the two surfaces describe the same write the same way.
func delegationFlash(action delegationAction, outcome application.Outcome, title string) string {
	summary := outcome.Delegation
	if action == delegateWorkRef {
		reference := ""
		if summary != nil && summary.Delegation != nil {
			reference = summary.Delegation["work_ref"]
		}
		if reference == "" {
			return "work ref cleared: " + title
		}
		arrow := " →"
		if outcome.NoChange() {
			arrow = " already"
		}
		return "work ref" + arrow + " " + reference + ": " + title
	}

	var marker map[string]string
	state := ""
	if summary != nil {
		marker, state = summary.Delegation, summary.State
	}
	if len(marker) == 0 {
		if outcome.NoChange() {
			return "not delegated: " + title
		}
		return "undelegated: " + title
	}
	if outcome.NoChange() {
		return "already " + delegationLabel(marker, state) + ": " + title
	}
	prefix := ""
	if action == delegateRelease {
		prefix = "released · "
	}
	return prefix + delegationLabel(marker, state) + ": " + title
}

func delegationLabel(marker map[string]string, state string) string {
	switch marker["status"] {
	case store.DelegationDelegated:
		suffix := ""
		if state != "" {
			suffix = " (" + state + ")"
		}
		return "delegated → " + marker["assignee"] + suffix
	case store.DelegationReady:
		return "agent-ready (" + marker["mode"] + ")"
	case store.DelegationClaimed:
		return "claimed by " + marker["assignee"] + " (" + marker["mode"] + ")"
	}
	return "delegated"
}

// delegationStateLabel is the `— …` suffix on the delegate modal's title,
// matching the recurrence popup's shape.
func (m *Model) delegationStateLabel(item store.Item) string {
	marker := delegationOf(item)
	if len(marker) == 0 {
		return "not delegated"
	}
	return "now " + delegationLabel(marker, "")
}

// -- the availability prompt ----------------------------------------------------------

// DeferSelected is `z`, the OmniFocus-style availability action.
//
// A fuzzy date atomically sets the available-from date AND clears this task's
// own hold; `someday` adds the indefinite marker; `now` clears only the blockers
// this task itself owns. All three are one changeset, so one keystroke costs one
// undo and no intermediate state is observable.
func (m *Model) DeferSelected() {
	if m.CurrentProject() != nil {
		m.needsTask()
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	id, title := item.ID, item.Title
	m.SetForm(NewQuickForm(QuickFormOptions{
		Kind: QuickFormDeferUntil, Title: "Defer until", Prompt: "date / choice",
		Hint: "fri · +3 · 07-15 · someday · now · esc cancels", MinWidth: 50,
		ReturnMode: ReturnList, TargetID: id,
		Submit: func(raw string) string {
			choice := strings.ToLower(strings.TrimSpace(raw))
			var value *temporal.Value
			if choice != "someday" && choice != "now" {
				parsed, err := ParseTemporal(raw, m.currentDate(), m.temporalContext())
				if err != nil {
					return "can't parse “" + raw + "”; use a date/time, someday, or now"
				}
				value = parsed.(*temporal.Value)
			}

			fresh, found := m.read.TaskFor(id)
			if !found {
				return "task no longer exists"
			}
			// Rule 3: the lead already owns "hide until", so a one-off date
			// here would fight it. `someday` and `now` stay available.
			if value != nil && lead.Span(fresh.Lead) {
				described := fresh.Lead
				if text, ok := lead.Describe(fresh.Lead); ok {
					described = text
				}
				return "already hides until " + described + " its date — " +
					"edit Lead time, or clear it first"
			}

			changes, label := deferChanges(choice, value, title)
			outcome, supported := m.app.UpdateTask(id, changes, label, m.operation())
			if !supported {
				return "this store cannot change availability"
			}
			if !(outcome.OK() || outcome.NoChange()) {
				if outcome.Conflict() {
					return "file changed underneath — reopen"
				}
				return outcomeMessage(outcome, "could not change availability")
			}
			m.formSuccess(func() {
				m.Refresh()
				message := m.availabilityFlash(id)
				// A task that is no longer available disappears from a view
				// that hides unavailable work; the selection then reconciles
				// rather than chasing a row that is gone.
				if task, present := m.read.TaskFor(id); present &&
					(m.showDeferred || m.read.Queries().AvailabilityFor(task).Available()) {
					m.reselect(id)
				} else {
					m.RefreshRows()
				}
				m.Flash(message)
			})
			return ""
		},
	}))
	_ = m.SetMode(ModeForm)
}

func deferChanges(choice string, value *temporal.Value, title string) ([]store.Change, string) {
	switch choice {
	case "someday":
		return []store.Change{{Field: store.FieldDeferred, Value: store.BoolValue(true)}},
			"on hold: " + title
	case "now":
		return []store.Change{{Field: store.FieldActivate, Value: store.BoolValue(true)}},
			"activate: " + title
	}
	return []store.Change{
		{Field: store.FieldDeferred, Value: store.BoolValue(false)},
		{Field: store.FieldScheduled, Value: store.TemporalValue(*value)},
	}, "defer until " + FormatTemporal(value) + ": " + title
}

// availabilityFlash says what the task's availability now IS, which is the only
// way a user can tell `someday` from `now` from a dated hold without re-reading
// the row.
func (m *Model) availabilityFlash(id string) string {
	task, found := m.read.TaskFor(id)
	if !found {
		return "task no longer exists"
	}
	queries := m.read.Queries()
	availability := queries.AvailabilityFor(task)
	if availability.Available() {
		return "▸ available now: " + task.Title
	}
	blockerTitle := ""
	if availability.BlockerID != "" {
		if blocker, present := m.read.TaskFor(availability.BlockerID); present {
			blockerTitle = " " + blocker.Title
		}
	}
	switch availability.Reason {
	case taskquery.ReasonScheduled:
		gate := "its lead window"
		if value, present := queries.ScheduledValue(task); present {
			gate = FormatTemporal(&value)
		}
		return "⏳ " + task.Title + " unavailable until " + gate
	case taskquery.ReasonAncestorScheduled:
		date := "a parent date"
		if availability.BlockerID != "" {
			if blocker, present := m.read.TaskFor(availability.BlockerID); present && blocker.Scheduled != "" {
				date = blocker.Scheduled
			}
		}
		return "⏳ " + task.Title + " unavailable until " + date + " via parent" + blockerTitle
	case taskquery.ReasonOnHold:
		return "⏸ on hold: " + task.Title
	case taskquery.ReasonAncestorOnHold:
		return "⏸ " + task.Title + " on hold via parent" + blockerTitle
	}
	return task.Title + " unavailable"
}
