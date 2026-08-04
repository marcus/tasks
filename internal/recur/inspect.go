package recur

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// rubyInspect matches Ruby String#inspect, which is how Recur spells the echoed
// input in every rejection and in the not-a-cookie ArgumentError. strconv.Quote
// and %q are not substitutes: Go spells ESC as \x1b and NUL as \x00 where Ruby
// writes \e and \u0000, Go leaves `#{` unescaped where Ruby writes `\#{`, and Go
// escapes format and private-use characters that Ruby prints verbatim. These
// strings reach the user, so the spelling is part of the ported behavior.
//
// internal/check carries its own copy for JSON diagnostics; that one only ever
// sees decoder-validated UTF-8, while this one is handed raw argv and so must
// also render invalid bytes. Merging them is a refactor, not a translation, and
// is left for Marcus to decide.
func rubyInspect(value string) string {
	var result strings.Builder
	result.WriteByte('"')
	for index := 0; index < len(value); {
		char, size := utf8.DecodeRuneInString(value[index:])
		if char == utf8.RuneError && size == 1 {
			// Ruby prints a byte that is not valid in the string's encoding as
			// \xHH rather than replacing it.
			fmt.Fprintf(&result, `\x%02X`, value[index])
			index++
			continue
		}
		index += size
		switch char {
		case '\\':
			result.WriteString(`\\`)
		case '"':
			result.WriteString(`\"`)
		case '\a':
			result.WriteString(`\a`)
		case '\b':
			result.WriteString(`\b`)
		case '\t':
			result.WriteString(`\t`)
		case '\n':
			result.WriteString(`\n`)
		case '\v':
			result.WriteString(`\v`)
		case '\f':
			result.WriteString(`\f`)
		case '\r':
			result.WriteString(`\r`)
		case 0x1b:
			result.WriteString(`\e`)
		case '#':
			// Only an interpolation-introducing # is escaped; a bare # is not.
			if next, _ := utf8.DecodeRuneInString(value[index:]); next == '{' || next == '$' || next == '@' {
				result.WriteString(`\#`)
			} else {
				result.WriteByte('#')
			}
		default:
			switch {
			case rubyPrintable(char):
				result.WriteRune(char)
			case char <= 0xFFFF:
				fmt.Fprintf(&result, `\u%04X`, char)
			default:
				fmt.Fprintf(&result, `\u{%X}`, char)
			}
		}
	}
	result.WriteByte('"')
	return result.String()
}

// rubyPrintable reports whether Ruby prints a character verbatim inside
// String#inspect. Ruby's rule is Onigmo's Unicode print property, which covers
// the graphic categories plus format (Cf) and private use (Co) — so a zero-width
// space, a tag character and a private-use codepoint all pass through — while
// control (Cc), line and paragraph separators (Zl, Zp) and unassigned codepoints
// (Cn, which is every rune no Go category table claims) are escaped.
func rubyPrintable(char rune) bool {
	return unicode.IsGraphic(char) || unicode.Is(unicode.Cf, char) || unicode.Is(unicode.Co, char)
}
