package record

import (
	"fmt"
	"regexp"
	"strings"
)

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

// modeShape is what a mode may be SPELLED like, wherever one is written down:
// a lowercase word, digits and underscores allowed after the first letter.
//
// Shape and membership are deliberately separate questions. A mode of the
// wrong shape is a schema fact and is always an error; a well-shaped mode the
// active vocabulary does not list is a CONFIGURATION fact, and configuration
// changes — so on disk that is a warning, never a reason to invalidate a file
// somebody's tasks live in.
var modeShape = regexp.MustCompile(`\A[a-z][a-z0-9_]*\z`)

// ValidModeName reports whether a string is spelled like a mode. It says
// nothing about membership; that is the vocabulary's question.
func ValidModeName(mode string) bool { return modeShape.MatchString(mode) }

// ParseModeList reads the configured mode list — a comma-separated list of
// bare words, and nothing more. A mode carries NO label or description on
// purpose: the label a user would write is the word itself, every surface
// already renders the word, and a `mode:Label` syntax would need escaping
// rules, a display column, and a second answer to "what does a refusal quote
// back". The syntax stays boring so the vocabulary stays one list.
//
// The second result explains the refusal; when it is non-empty the caller must
// fall back to the built-in set rather than run on a half-understood list.
func ParseModeList(value string) (ModeSet, string) {
	modes := ModeSet{}
	seen := map[string]bool{}
	for _, field := range strings.Split(value, ",") {
		mode := strings.TrimSpace(field)
		if mode == "" {
			continue
		}
		if !ValidModeName(mode) {
			return nil, fmt.Sprintf("%q is not a mode name (lowercase letters, digits and underscores, starting with a letter)", mode)
		}
		if seen[mode] {
			return nil, fmt.Sprintf("%q is listed twice", mode)
		}
		seen[mode] = true
		modes = append(modes, mode)
	}
	if len(modes) == 0 {
		return nil, "the list is empty"
	}
	return modes, ""
}

// anyWellShapedMode accepts every well-shaped mode while still QUOTING the
// vocabulary it wraps. It is how the on-disk validator separates the two
// questions without a second copy of the marker walk.
type anyWellShapedMode struct{ ModeVocabulary }

func (anyWellShapedMode) Valid(mode string) bool { return ValidModeName(mode) }

// Modes resolves an optional vocabulary to the one to use.
func Modes(vocabulary ModeVocabulary) ModeVocabulary {
	if vocabulary == nil {
		return BuiltinModes()
	}
	return vocabulary
}
