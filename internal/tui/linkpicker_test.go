package tui

import (
	"fmt"
	"testing"

	"github.com/marcus/tasks/internal/links"
)

func manyPickerLinks(count int) []links.Link {
	result := make([]links.Link, 0, count)
	for index := 1; index <= count; index++ {
		result = append(result, links.Link{
			URL: fmt.Sprintf("https://example.com/%d", index), System: "example.com",
		})
	}
	return result
}

func renderLinkPicker(picker *LinkPicker, height int) {
	picker.Picker().Popup(PlainStyler{}, 80, height, func(text string, _ int) string { return text })
}

func TestLinkPickerDigitCannotAcceptRowHiddenByShortPopup(t *testing.T) {
	picker := NewLinkPicker(PlainStyler{}, "task", manyPickerLinks(10))
	renderLinkPicker(picker, 7) // full layout: 7 - 5 = 2 rendered result rows

	if result := picker.HandleKey("3"); result.Kind == PickerAccepted {
		t.Fatalf("digit 3 accepted hidden row: %+v", result)
	}
	if got := picker.Picker().Input(); got != "3" {
		t.Fatalf("hidden-row digit input = %q, want 3", got)
	}
}

func TestLinkPickerDigitsUseTheScrolledRenderedViewport(t *testing.T) {
	picker := NewLinkPicker(PlainStyler{}, "task", manyPickerLinks(10))
	renderLinkPicker(picker, 7)
	picker.Move(3)
	renderLinkPicker(picker, 7)

	result := picker.HandleKey("1")
	link, ok := picker.Link(result)
	if !ok || link.URL != "https://example.com/3" {
		t.Fatalf("digit 1 in scrolled viewport resolved %+v, ok=%v", link, ok)
	}

	other := NewLinkPicker(PlainStyler{}, "task", manyPickerLinks(10))
	renderLinkPicker(other, 7)
	other.Move(3)
	renderLinkPicker(other, 7)
	if result := other.HandleKey("3"); result.Kind == PickerAccepted {
		t.Fatalf("digit 3 accepted beyond two-row viewport: %+v", result)
	}
}

func TestLinkPickerDigitBeforeFirstRenderRemainsInput(t *testing.T) {
	picker := NewLinkPicker(PlainStyler{}, "task", manyPickerLinks(3))
	if result := picker.HandleKey("1"); result.Kind == PickerAccepted {
		t.Fatalf("pre-render digit accepted: %+v", result)
	}
	if got := picker.Picker().Input(); got != "1" {
		t.Fatalf("pre-render digit input = %q, want 1", got)
	}
}
