package tui

import (
	"strings"

	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// The delegation prompts (`D`), the work-reference prompt (`W`), and the
// availability prompt (`z`).
//
// All three are quick forms over one typed input, and all three go through the
// application's delegation commands rather than composing store writes here —
// the WAITING default behind a human delegation and the blocker note behind a
// release are composed there, in one undo step, and duplicating that would give
// the TUI a second, subtly different delegation.

// delegateClearWords are the spellings that mean "remove the delegation".
var delegateClearWords = []string{"off", "none", "clear"}

// delegateWords is the whole `D` vocabulary: the delegation modes, `release`,
// and the clear words. An email is matched separately.
//
// It is a FUNCTION of the vocabulary in force, not a package-level var: a var
// is evaluated during init, before the process has built the store whose modes
// these are, so a configured set would never reach the prompt.
func delegateWords(modes record.ModeVocabulary) []string {
	return append(record.Modes(modes).Modes(),
		append([]string{"release"}, delegateClearWords...)...)
}

// delegatePrefixMin is where prefix matching starts.
//
// The plan promises `ref` / `res` / `imp`, and every word in the vocabulary is
// at least this long, so no promised spelling is lost — but one stray character
// no longer performs the widest or the most destructive action in the grammar.
// `i` used to delegate at `implement`, and `o` / `n` used to revoke a live claim
// with no confirmation. The shortest inputs must not be the ones that cost most.
const delegatePrefixMin = 3

func delegateHint(modes record.ModeVocabulary) string {
	words := append([]string{"pat@example.com"}, record.Modes(modes).Modes()...)
	words = append(words, "release", "off", "esc cancels")
	return strings.Join(words, " · ")
}

// delegationAction is what a parsed `D` input asks for.
type delegationAction string

const (
	delegateHuman       delegationAction = "human"
	delegateAgent       delegationAction = "agent"
	delegateRelease     delegationAction = "release"
	delegateUndelegate  delegationAction = "undelegate"
	delegateWorkRef     delegationAction = "work_ref"
	delegateParseFailed delegationAction = "error"
)

// DelegateSelected is `D`.
func (m *Model) DelegateSelected() {
	item, ok := m.delegationTarget()
	if !ok {
		return
	}
	id, title := item.ID, item.Title
	// Read the vocabulary the store actually enforces, NOW — not at init.
	modes := m.app.DelegationModes()
	m.SetForm(NewQuickForm(QuickFormOptions{
		Kind: QuickFormDelegate, Title: "Delegate", Prompt: "delegate to",
		Hint: delegateHint(modes), MinWidth: 84, ReturnMode: ReturnList, TargetID: id,
		Suffix: "(" + m.delegationStateLabel(item) + ")",
		Submit: func(raw string) string {
			text := strings.TrimSpace(raw)
			action, argument := parseDelegationInput(text, modes)
			if action == delegateParseFailed {
				return argument
			}
			command := application.DelegationCommand{ID: id}
			switch action {
			case delegateHuman:
				command.Action = application.ActionDelegate
				command.Kind, command.Assignee = "human", argument
			case delegateAgent:
				command.Action = application.ActionDelegate
				command.Kind, command.Mode = "agent", argument
			case delegateRelease:
				// The owner's D-release is always a FORCED one: this prompt
				// exists to clear a claim the owner does not hold, and a worker
				// releasing its own claim uses the CLI with its worker id.
				command.Action = application.ActionRelease
				command.Force = true
			case delegateUndelegate:
				command.Action = application.ActionUndelegate
			}
			return m.runDelegation(id, title, action, command)
		},
	}))
	_ = m.SetMode(ModeForm)
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

// parseDelegationInput returns an action and its argument, or the error to
// show. The empty string is rejected BEFORE the prefix scan — every word starts
// with "", so an empty input would otherwise report itself as ambiguous across
// the whole vocabulary.
func parseDelegationInput(text string, modes record.ModeVocabulary) (delegationAction, string) {
	if text == "" {
		return delegateParseFailed, delegationParseError(text, modes)
	}
	if record.DelegationEmail(text) {
		return delegateHuman, text
	}
	// A near-miss address is a typo, not a person, and the user deserves to
	// hear that here rather than as a store refusal about a field they never
	// knew they were writing.
	if strings.Contains(text, "@") {
		return delegateParseFailed, "“" + text + "” isn't an email address — use pat@example.com"
	}
	matches := delegationWordMatches(strings.ToLower(text), modes)
	switch len(matches) {
	case 1:
		word := matches[0]
		switch {
		case word == "release":
			return delegateRelease, ""
		case containsString(delegateClearWords, word):
			return delegateUndelegate, ""
		}
		return delegateAgent, word
	case 0:
		return delegateParseFailed, delegationParseError(text, modes)
	}
	return delegateParseFailed,
		"“" + text + "” matches " + strings.Join(matches, ", ") + " — type more of the word"
}

// delegationWordMatches: nothing shorter than delegatePrefixMin matches
// anything, so `o`, `n`, `i`, `r` and `re` all land on the unparseable message
// instead of silently resolving to a word the user did not type.
func delegationWordMatches(text string, modes record.ModeVocabulary) []string {
	if len([]rune(text)) < delegatePrefixMin {
		return nil
	}
	matches := []string{}
	for _, word := range delegateWords(modes) {
		if strings.HasPrefix(word, text) {
			matches = append(matches, word)
		}
	}
	return matches
}

func delegationParseError(text string, modes record.ModeVocabulary) string {
	return "can't parse “" + text + "”; use an email, " +
		record.Modes(modes).Quoted() + ", release, or off"
}

// runDelegation is the shared submit body behind both prompts: run against a
// FROZEN task id, then stage the flash and reselect for after the form closes.
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
			" at " + outcome.Delegation.At + " — off revokes it"
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

// delegationStateLabel is the `(now …)` suffix on the D prompt, matching the
// recurrence popup's shape.
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
