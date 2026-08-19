package application

import (
	"fmt"
	"strings"

	"github.com/marcus/tasks/internal/links"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
)

// This file ports the operation objects: lib/tasks/create_task.rb,
// delete_task.rb, proposal_decision.rb, delegation_command.rb,
// task_placement.rb and operation_context.rb.
//
// Ruby spells all six the same way: a frozen object whose constructor deep-dups
// every String and Array so a caller cannot mutate an accepted command. Go has
// no freeze, so the same guarantee is kept differently and in one place — each
// command is a plain value, and the application COPIES its slices at the
// boundary where it accepts one. A caller may keep and reuse the value it
// built; what it cannot do is change a command the application already took.
//
// Two Ruby-only shapes are deliberately dropped:
//
//   - the keyword aliases (`text`/`title`, `due`/`deadline`, `under`/`parent_id`,
//     `recur`/`recurrence`). They exist because Ruby callers pass a keyword
//     soup; a Go caller names a struct field, and two names for one field is a
//     way to set both and lose one.
//   - the "must be true or false" validations on `deferred` and
//     `apply_host_context`. Those are runtime type checks in Ruby and compile
//     errors here.

// CreateCommand is one immutable, transport-neutral request to create a live
// task — lib/tasks/create_task.rb.
//
// `Body` and `Notes` are the two spellings of the initial note: one string with
// embedded newlines, or an ordered list. Supplying both is refused, so the
// persisted order is never implicit.
type CreateCommand struct {
	Title      string
	Priority   string
	Tags       []string
	Deferred   bool
	Scheduled  string
	Deadline   string
	State      string
	Project    string
	ParentID   string
	Recurrence string
	Lead       string
	Body       string
	Notes      []string

	// Links are formal links the create installs in its own transaction, in
	// caller order. They exist so a context URL is part of the create rather
	// than a follow-up `link add` a caller can forget — and because they land in
	// the same write, one undo removes the task and its links together.
	Links []links.FormalLink

	// ScheduledValue and DeadlineValue carry a COMPLETE temporal value — the
	// date, the local time, its zone and its fold — for a caller that has
	// already parsed one.
	//
	// `Scheduled` and `Deadline` above stay the primary spelling, because a
	// transport supplies text and turning text into a value is this layer's job.
	// What they cannot express is a time of DAY: `createTemporalValues` builds
	// `temporal.Value{Date: date}` from them and there is nowhere to put 17:00,
	// Europe/London, or a fold. So an HTTP client asking for a 17:00 deadline
	// used to get an all-day one, and the API had to refuse the request outright
	// rather than store a value the client did not ask for.
	//
	// These are optional and pointer-typed so absent stays distinguishable from
	// "the zero value", and when one is set it WINS over the string for its
	// field: it is strictly more information about the same thing. The pointee is
	// copied when the command is accepted, so a caller that reuses its value
	// cannot change what was submitted.
	ScheduledValue *temporal.Value
	DeadlineValue  *temporal.Value

	// SkipHostContext opts out of the configured host context. Ruby spells this
	// `apply_host_context: true`, whose default is the interesting value; the
	// polarity is flipped here so the ZERO VALUE is the default behavior and a
	// caller building a command with a struct literal gets the host context
	// without having to know it must ask for it.
	SkipHostContext bool
}

// clone is the copy the application takes when it accepts a command, so a
// caller that reuses and mutates its slice — or the value behind one of its
// pointers — cannot change what was submitted.
func (c CreateCommand) clone() CreateCommand {
	c.Tags = copyOf(c.Tags)
	c.Notes = copyOf(c.Notes)
	c.Links = append([]links.FormalLink(nil), c.Links...)
	c.ScheduledValue = copyValue(c.ScheduledValue)
	c.DeadlineValue = copyValue(c.DeadlineValue)
	return c
}

// copyValue is the pointer half of clone. A temporal.Value holds no reference
// types, so copying the pointee is the whole of the defence.
func copyValue(value *temporal.Value) *temporal.Value {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// Contexts and ordinary tags, partitioned in stored order.
func (c CreateCommand) partitionTags() (contexts, ordinary []string) {
	contexts, ordinary = []string{}, []string{}
	for _, tag := range c.Tags {
		if strings.HasPrefix(tag, "@") {
			contexts = append(contexts, tag)
			continue
		}
		ordinary = append(ordinary, tag)
	}
	return contexts, ordinary
}

// DeleteCommand is one immutable request to hard-delete a live task —
// lib/tasks/delete_task.rb.
//
// ExpectedRevision is an opaque store-produced value: nil-equivalent ("") skips
// the optimistic-concurrency check, which is the CLI convenience, while a
// supplied value guards the whole subtree. Cascade opts into removing a task
// that still has descendants; deleting never reparents children.
type DeleteCommand struct {
	ID               string
	Cascade          bool
	ExpectedRevision string
	HistoryLabel     string
}

// ProposalAction is the closed approve/reject vocabulary.
type ProposalAction string

// The two decisions.
const (
	ProposalApprove ProposalAction = "approve"
	ProposalReject  ProposalAction = "reject"
)

// ProposalDecision is one immutable intent to accept or decline a proposal —
// lib/tasks/proposal_decision.rb. Notes are only meaningful on reject, where
// they are withdrawal rationale appended to the body in the same write.
type ProposalDecision struct {
	ID               string
	Action           ProposalAction
	ExpectedRevision string
	Notes            []string
}

func (d ProposalDecision) validate() []string {
	if strings.TrimSpace(d.ID) == "" {
		return []string{"task id is required"}
	}
	if d.Action != ProposalApprove && d.Action != ProposalReject {
		return []string{fmt.Sprintf("unknown proposal action: %s", d.Action)}
	}
	return nil
}

// DelegationAction is the closed five-verb vocabulary — DelegationCommand::ACTIONS.
type DelegationAction string

// The five delegation verbs.
const (
	ActionDelegate   DelegationAction = "delegate"
	ActionUndelegate DelegationAction = "undelegate"
	ActionClaim      DelegationAction = "claim"
	ActionRelease    DelegationAction = "release"
	ActionWorkRef    DelegationAction = "work_ref"
	// ActionDelegationNote rewrites the receiver-facing briefing on a delegation
	// that already exists, without restating who holds it or in what mode. The
	// note gets its own verb for the same reason work_ref has one: an owner
	// correcting a briefing should not have to re-send the delegation.
	ActionDelegationNote DelegationAction = "delegation_note"
)

var delegationActions = []DelegationAction{
	ActionDelegate, ActionUndelegate, ActionClaim, ActionRelease, ActionWorkRef,
	ActionDelegationNote,
}

// workRefClearWords are the words every surface spells "clear this reference"
// with. They normalize here, once: when each surface kept its own list, the CLI
// stored the literal string "none" while the TUI cleared.
//
// The delegation NOTE clears with the same two words, deliberately. They are
// already reserved mode names, so no vocabulary can ever contain them and no
// surface has to disambiguate a clear instruction from a mode; and one clearing
// spelling across the delegation surface is one thing to remember rather than
// two. A briefing whose entire text is the word "off" is not expressible, which
// is the same trade work_ref already makes.
var workRefClearWords = []string{"off", "none"}

// DelegationCommand is one immutable delegation intent — lib/tasks/delegation_command.rb.
//
// KeepState and Note are the two composition flags, and they are the reason
// this object belongs to the application rather than to the store: a human
// delegation moves the task to WAITING unless the owner keeps the state, and a
// release may carry a blocker note appended in the same undo step. Both compose
// two writes; everything else here the store owns.
type DelegationCommand struct {
	ID     string
	Action DelegationAction

	Kind     string
	Mode     string
	Assignee string
	Worker   string

	// WorkRef is the reference to record. An empty string CLEARS it — that is
	// the same instruction Ruby spells as nil, `off`, or `none`, and the two
	// clear words normalize to it. There is deliberately no way to set a work
	// ref to the empty string: the store refuses a blank reference anyway, so
	// the only meaning "" could carry is the one it has here.
	WorkRef string

	// Note carries text whose meaning depends on the verb, and the two meanings
	// are deliberately not merged into one field: they are both "the note the
	// caller typed", and a surface that has one text box should not have to pick
	// a field name based on the verb behind it.
	//
	//   delegate / delegation_note — the receiver-facing briefing STORED in the
	//     marker. SetNote must be true for it to be written at all, so a
	//     delegation that says nothing about the note leaves the existing one
	//     alone rather than erasing it. "off" and "none" clear.
	//   release — the blocker line APPENDED to the task body in the release's
	//     own undo step. It is not stored in the marker and needs no SetNote:
	//     an empty note simply appends nothing.
	Note    string
	SetNote bool

	KeepState bool
	Force     bool

	ExpectedRevision string
}

// Human reports a delegation to a named person.
func (c DelegationCommand) Human() bool { return c.Kind == "human" }

// Agent reports a delegation to the agent pool.
func (c DelegationCommand) Agent() bool { return c.Kind == "agent" }

// ClearsWorkRef reports the clearing form of the work_ref verb.
func (c DelegationCommand) ClearsWorkRef() bool {
	return c.Action == ActionWorkRef && c.normalizedWorkRef() == ""
}

// normalizedWorkRef applies the clear-word rule. Comparison is
// case-insensitive and ignores surrounding space, exactly as Ruby's
// `text.strip.casecmp?(word)` does.
func (c DelegationCommand) normalizedWorkRef() string {
	trimmed := strings.TrimSpace(c.WorkRef)
	for _, word := range workRefClearWords {
		if strings.EqualFold(trimmed, word) {
			return ""
		}
	}
	// The untrimmed text is what reaches the store: the store owns whether a
	// reference with surrounding space is acceptable, and trimming here would
	// quietly answer a question that is not this layer's.
	return c.WorkRef
}

// ClearsNote reports a stored-note instruction that removes the briefing.
func (c DelegationCommand) ClearsNote() bool {
	return c.SetNote && c.normalizedNote() == ""
}

// normalizedNote applies the same clear-word rule the work reference uses, and
// trims: a stored briefing is compared for equality by the store, so leading
// and trailing whitespace differences must not read as a new instruction.
func (c DelegationCommand) normalizedNote() string {
	trimmed := strings.TrimSpace(c.Note)
	for _, word := range workRefClearWords {
		if strings.EqualFold(trimmed, word) {
			return ""
		}
	}
	return trimmed
}

func (c DelegationCommand) validate() []string {
	if strings.TrimSpace(c.ID) == "" {
		return []string{"task id is required"}
	}
	for _, action := range delegationActions {
		if c.Action == action {
			return nil
		}
	}
	return []string{fmt.Sprintf("unknown delegation action: %s", c.Action)}
}

// Placement is an immutable structural destination for one task subtree —
// lib/tasks/task_placement.rb. The store resolves these stable ids under its
// mutation lock; an adapter must not translate them into record indexes. An
// empty BeforeID means append as the destination parent's last child.
type Placement struct {
	ParentID string
	BeforeID string
}

// Appends reports the append form: no anchor, so the subtree lands last.
func (p Placement) Appends() bool { return p.BeforeID == "" }

// -- operation context --------------------------------------------------------

// OperationSource names where a domain operation originated.
type OperationSource string

// The three surfaces.
const (
	SourceCLI OperationSource = "cli"
	SourceTUI OperationSource = "tui"
	SourceAPI OperationSource = "api"
)

var operationSources = []OperationSource{SourceCLI, SourceTUI, SourceAPI}

// OperationContext is immutable metadata identifying where an operation came
// from — lib/tasks/operation_context.rb — plus the temporal context that
// operation reads its clock from.
//
// Passing one is optional at every entry point: a nil *OperationContext is the
// "no context supplied" case Ruby spells as `context: nil`. What Ruby cannot
// express and this can is that an invalid context is refused at CONSTRUCTION
// rather than at every call site, so no application method needs a
// validate_operation_context of its own.
type OperationContext struct {
	operationID string
	source      OperationSource
	actor       string
	temporal    *temporal.Context
}

// NewOperationContext validates and builds a context. The operation id is
// required and the source must be one of cli/tui/api.
func NewOperationContext(operationID string, source OperationSource, actor string) (*OperationContext, error) {
	id := strings.TrimSpace(operationID)
	if id == "" {
		return nil, fmt.Errorf("operation_id is required")
	}
	name := strings.TrimSpace(string(source))
	if name == "" {
		return nil, fmt.Errorf("source is required")
	}
	known := false
	for _, candidate := range operationSources {
		if OperationSource(name) == candidate {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("unknown operation source: %s", name)
	}
	return &OperationContext{
		operationID: id, source: OperationSource(name), actor: strings.TrimSpace(actor),
	}, nil
}

// WithTemporalContext returns a copy that pins the clock this operation reads.
func (c *OperationContext) WithTemporalContext(context temporal.Context) *OperationContext {
	if c == nil {
		return nil
	}
	copied := *c
	copied.temporal = &context
	return &copied
}

// OperationID is the caller-supplied correlation id.
func (c *OperationContext) OperationID() string {
	if c == nil {
		return ""
	}
	return c.operationID
}

// Source is the originating surface.
func (c *OperationContext) Source() OperationSource {
	if c == nil {
		return ""
	}
	return c.source
}

// Actor is the optional acting identity.
func (c *OperationContext) Actor() string {
	if c == nil {
		return ""
	}
	return c.actor
}

// TemporalContext is the pinned clock, and false when this context carries none.
func (c *OperationContext) TemporalContext() (temporal.Context, bool) {
	if c == nil || c.temporal == nil {
		return temporal.Context{}, false
	}
	return *c.temporal, true
}

func copyOf(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

// invalid is the typed refusal shape every argument error in this package
// produces. Ruby raises ArgumentError for programming errors and returns an
// :invalid result for input errors; the distinction does not survive a
// transport boundary, and an application layer that panics on a malformed HTTP
// body is a worse answer than one that refuses it.
func invalid(messages ...string) Outcome {
	return Outcome{MutationResult: store.MutationResult{
		Status: store.MutationInvalid, Errors: messages,
	}}
}
