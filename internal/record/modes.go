package record

import "strings"

// The delegation mode vocabulary lives behind ONE seam, and it is carried as a
// VALUE, never as a location.
//
// A mode is the kind of delegation this is — the authority the receiver has.
// The built-in vocabulary is refine/research/implement, but the set is a user
// decision, not a schema fact, so everything that has to answer "is this a
// mode" or "what does a refusal quote back" takes a ModeVocabulary from its
// caller and defaults to BuiltinModes when given nil.
//
// There is deliberately no package-level setter. A process-wide vocabulary
// would make this package's answers depend on start-up order (a package-level
// var in some surface, evaluated during init, would silently keep the built-in
// set) and would let one test's configuration leak into another's. Instead the
// store and the checker each hold the vocabulary they were constructed with,
// which is what makes configuring it later one field rather than a refactor.
type ModeVocabulary interface {
	// Valid reports whether one mode string is a member.
	Valid(mode string) bool
	// Modes lists the members in the order a human should read them.
	Modes() []string
	// Quoted renders the set the way a refusal quotes it back
	// ("refine/research/implement").
	Quoted() string
}

// ModeSet is the ordinary implementation: an ordered, closed list of modes.
type ModeSet []string

func (s ModeSet) Valid(mode string) bool {
	for _, candidate := range s {
		if candidate == mode {
			return true
		}
	}
	return false
}

func (s ModeSet) Modes() []string {
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func (s ModeSet) Quoted() string { return strings.Join(s, "/") }

// BuiltinModes is the vocabulary this build ships with, and the value every
// seam falls back to when a caller supplies none.
func BuiltinModes() ModeSet { return ModeSet{"refine", "research", "implement"} }

// Modes resolves an optional vocabulary to the one to use.
func Modes(vocabulary ModeVocabulary) ModeVocabulary {
	if vocabulary == nil {
		return BuiltinModes()
	}
	return vocabulary
}
