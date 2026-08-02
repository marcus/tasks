// Review-only sweep harness. Not part of the module build: drop it into
// go/internal/query/ as zz_sweep_review_test.go to re-run, after producing the
// two captures with review-2026-08-02-coercion/capture-ruby-inspect.rb and the
// downcase capture described in the review note.
package query

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
)

func loadCapture(t *testing.T, path string) map[rune]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	captured := map[rune]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 2)
		cp, _ := strconv.Atoi(parts[0])
		var sb strings.Builder
		for _, piece := range strings.Split(parts[1], ",") {
			out, _ := strconv.Atoi(piece)
			sb.WriteRune(rune(out))
		}
		captured[rune(cp)] = sb.String()
	}
	return captured
}

func TestReviewExhaustiveDowncaseSweep(t *testing.T) {
	expected := loadCapture(t, "/tmp/qfp-ses5d055f/ruby-downcase.txt")
	mismatches := 0
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		source := string(cp)
		want, ok := expected[cp]
		if !ok {
			want = source
		}
		filter, err := NewFilter(FilterOptions{Text: []string{source}})
		if err != nil {
			t.Fatal(err)
		}
		if got := filter.TextQuery(); got != want {
			mismatches++
			if mismatches <= 5 {
				t.Errorf("downcase U+%04X: ruby %q go %q", cp, want, got)
			}
		}
	}
	t.Logf("downcase mismatches: %d", mismatches)
}

func TestReviewExhaustiveInspectSweep(t *testing.T) {
	expected := loadCapture(t, "/tmp/qfp-ses5d055f/ruby-inspect.txt")
	mismatches := 0
	examples := []string{}
	for cp := rune(0); cp <= 0x10FFFF; cp++ {
		if cp >= 0xD800 && cp <= 0xDFFF {
			continue
		}
		source := string(cp)
		want, ok := expected[cp]
		if !ok {
			want = `"` + source + `"`
		}
		if got := inspectString(source); got != want {
			mismatches++
			if len(examples) < 6 {
				examples = append(examples, strconv.FormatInt(int64(cp), 16)+": ruby "+want+" go "+got)
			}
		}
	}
	t.Logf("inspect mismatches: %d", mismatches)
	for _, example := range examples {
		t.Logf("  U+%s", example)
	}
}
