package tui

import tea "charm.land/bubbletea/v2"

// ModeFieldModal is the multi-field popup's mode.
//
// It is a mode of its own rather than a flag on ModeForm because the key
// routing genuinely differs: tab moves between fields, Return is text inside a
// note, and escape has a latch in front of it. Sharing ModeForm would mean the
// single-field popups inherited all three.
const ModeFieldModal Mode = "field_modal"

// OpenFieldModal opens a multi-field modal.
//
// It is the ONE entry point a feature needs: build a FieldModal from specs and
// hand it over. What the fields MEAN is known only to the caller that declared
// them, which is what makes the component reusable rather than one feature's
// prompt wearing a general-sounding name.
func (m *Model) OpenFieldModal(modal *FieldModal) bool {
	if modal == nil {
		return false
	}
	m.fieldModal = modal
	if err := m.SetMode(ModeFieldModal); err != nil {
		m.fieldModal = nil
		return false
	}
	return true
}

// FieldModal is the open multi-field modal, or nil.
func (m *Model) FieldModal() *FieldModal { return m.fieldModal }

// SetFieldModal opens or clears the overlay without changing the mode, for the
// same reasons SetForm exists: an external reload may remove it.
func (m *Model) SetFieldModal(modal *FieldModal) {
	m.fieldModal = modal
	if modal == nil && m.mode == ModeFieldModal {
		m.mode = ModeList
	}
}

// CloseFieldModal dismisses it, running the success effect on a successful
// submit exactly as CloseForm does.
func (m *Model) CloseFieldModal(success bool) {
	if m.fieldModal == nil {
		return
	}
	destination := m.fieldModal.ReturnMode()
	if destination == ReturnModal && m.modal == nil {
		destination = ReturnList
	}
	effect := m.fieldModal.Success
	m.fieldModal = nil
	if destination == ReturnModal {
		m.mode = ModeModal
	} else {
		m.mode = ModeList
	}
	if success && effect != nil {
		effect()
	}
}

// fieldModalKey routes one key, and is the only place the component's outcomes
// become model state.
func (m *Model) fieldModalKey(sequence string) {
	if m.fieldModal == nil {
		m.mode = ModeList
		return
	}
	m.resolveFieldModalOutcome(m.fieldModal.HandleKey(sequence))
}

func (m *Model) resolveFieldModalOutcome(outcome FieldModalOutcome) {
	modal := m.fieldModal
	if modal == nil {
		return
	}
	switch outcome.Result {
	case FieldModalCancelled:
		m.CloseFieldModal(false)
	case FieldModalSubmitted:
		m.CloseFieldModal(true)
	case FieldModalGuarded:
		m.Flash("unsaved changes — esc again discards · anything else keeps editing")
	case FieldModalActioned:
		if modal.OnAction != nil {
			m.resolveFieldModalOutcome(modal.OnAction(outcome.ActionID))
		}
	}
}

// fieldModalOverlay paints the box and, in the same pass, records the hit map
// the pointer reads. A frame that was never painted therefore has no hit map,
// which is the correct answer to a click that arrived before the first paint.
func (m *Model) fieldModalOverlay(layout ScreenLayout) *OverlayBox {
	if m.fieldModal == nil {
		return nil
	}
	// The same budget the single-field popup takes, with the same floor of 1:
	// there is no reason a multi-field box should reserve more rows than the
	// quick form does in a terminal too small for either.
	render := m.fieldModal.Render(m.styler,
		max(layout.Width-4, 8), min(m.fieldModal.Height(), max(layout.BodyHeight, 1)))
	box := m.center(layout, render.Lines, render.FocusedContentRow)
	if box != nil {
		box.Backdrop = true
	}
	return box
}

// fieldModalMouse routes a click or wheel tick that landed on the open modal.
//
// The row is taken against the box's own top row, which is the number the paint
// recorded. A click OUTSIDE the box is consumed and inert: the modal keeps the
// pointer like every other overlay, and a stray click never discards a
// half-filled form.
func (m *Model) fieldModalMouse(box *OverlayBox, event tea.MouseMsg) bool {
	if m.fieldModal == nil {
		return false
	}
	mouse := event.Mouse()
	row, column := mouse.Y-box.Row, mouse.X-box.Col
	switch typed := event.(type) {
	case tea.MouseWheelMsg:
		direction := -1
		switch typed.Button {
		case tea.MouseWheelUp:
		case tea.MouseWheelDown:
			direction = 1
		default:
			return false
		}
		m.resolveFieldModalOutcome(m.fieldModal.Wheel(row, column, direction))
		return true
	case tea.MouseMotionMsg:
		// Motion only means anything while a scrollbar drag is in flight; a
		// drag that has left the box keeps scrolling its surface (clamped),
		// which is what makes a one-row thumb on a long list grabbable.
		if m.fieldModal.scrollDrag == nil {
			return false
		}
		m.resolveFieldModalOutcome(m.fieldModal.Drag(row))
		return true
	case tea.MouseReleaseMsg:
		return m.fieldModal.EndDrag()
	case tea.MouseClickMsg:
		if typed.Button != tea.MouseLeft {
			return false
		}
		m.resolveFieldModalOutcome(m.fieldModal.Click(row, column))
		return true
	}
	return false
}
