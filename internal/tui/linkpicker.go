package tui

import (
	"github.com/marcus/tasks/internal/links"
)

// LinkPicker is the single-choice adapter over a task's openable link union.
type LinkPicker struct {
	targetID string
	links    []links.Link
	picker   *ChoicePicker
}

// NewLinkPicker builds a searchable picker whose visible row favors a label
// while retaining the system and URL needed to distinguish similar choices.
func NewLinkPicker(styler Styler, targetID string, found []links.Link) *LinkPicker {
	if styler == nil {
		styler = PlainStyler{}
	}
	options := make([]PickerOption, 0, len(found))
	for _, link := range found {
		label := link.URL + styler.Paint("muted", "  "+link.System)
		if link.Label != nil && *link.Label != "" {
			label = *link.Label + styler.Paint("muted", "  "+link.System+"  "+link.URL)
		}
		options = append(options, PickerOption{
			ID: link.URL, Label: label,
			SearchText: []string{link.URL, link.System, labelText(link.Label)},
			Kind:       PickerChoice, Metadata: link,
		})
	}
	return &LinkPicker{targetID: targetID, links: append([]links.Link(nil), found...), picker: NewChoicePicker(ChoicePickerOptions{
		Title: "open link", Options: options, Mode: SelectSingle,
		AcceptLabel: "open", EmptyLabel: "no matching links", MaxVisible: paletteMaxResults,
	})}
}

func labelText(label *string) string {
	if label == nil {
		return ""
	}
	return *label
}

// Picker exposes the shared picker for rendering and tests.
func (p *LinkPicker) Picker() *ChoicePicker { return p.picker }

// TargetID is the stable task the choices came from.
func (p *LinkPicker) TargetID() string { return p.targetID }

// HandleKey adds bounded, query-free digit selection to the shared picker.
// A digit that does not name a result remains ordinary searchable input.
func (p *LinkPicker) HandleKey(key string) PickerResult {
	if p.picker.Input() == "" && len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if result, accepted := p.picker.AcceptVisible(int(key[0] - '0')); accepted {
			return result
		}
	}
	return p.picker.HandleKey(key)
}
func (p *LinkPicker) Hit(rowOffset int) PickerResult { return p.picker.Hit(rowOffset) }
func (p *LinkPicker) Paste(text string) PickerResult { return p.picker.Paste(text) }
func (p *LinkPicker) Move(delta int) PickerResult    { return p.picker.Move(delta) }

// Link resolves an accepted URL back to its classified link payload.
func (p *LinkPicker) Link(result PickerResult) (links.Link, bool) {
	if result.Kind != PickerAccepted || len(result.IDs) == 0 {
		return links.Link{}, false
	}
	for _, link := range p.links {
		if link.URL == result.IDs[0] {
			return link, true
		}
	}
	return links.Link{}, false
}
