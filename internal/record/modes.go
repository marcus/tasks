package record

import (
	"strings"
	"sync/atomic"
)

// The delegation mode vocabulary lives behind ONE seam.
//
// A mode is the kind of delegation this is — the authority the receiver has.
// The built-in vocabulary is refine/research/implement, but the set is a user
// decision, not a schema fact. Everything that has to answer "is this a mode"
// or "what does a refusal quote back" asks this seam, so making the set
// configurable is a WIRING change (one UseDelegationModes call at process
// start) rather than a hunt for literals.
//
// internal/record and internal/store deliberately read no configuration file:
// the seam is fed from the outside by whoever owns config.
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

// BuiltinModes is the vocabulary this build ships with. It is the value the
// seam holds until something wires a different one in.
func BuiltinModes() ModeSet { return ModeSet{"refine", "research", "implement"} }

var activeModes atomic.Value // ModeVocabulary

// DelegationModes returns the vocabulary in force.
func DelegationModes() ModeVocabulary {
	if held, ok := activeModes.Load().(ModeVocabulary); ok && held != nil {
		return held
	}
	return BuiltinModes()
}

// UseDelegationModes installs a vocabulary process-wide. This is the single
// wiring point for user-configured modes; passing nil restores the built-in
// set. Call it once during start-up, before any store or check work.
func UseDelegationModes(vocabulary ModeVocabulary) {
	if vocabulary == nil {
		activeModes.Store(ModeVocabulary(BuiltinModes()))
		return
	}
	activeModes.Store(vocabulary)
}
