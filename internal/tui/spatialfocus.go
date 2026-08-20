package tui

// SpatialFocus names the stable, spatially arranged regions an embedding host
// may include in its own focus ring. It is deliberately separate from
// FocusContext: list/detail describe where focus sits in the rendered frame,
// while prompt, editor, picker, and modal contexts describe who owns keys.
type SpatialFocus string

const (
	SpatialFocusList   SpatialFocus = "list"
	SpatialFocusDetail SpatialFocus = "detail"
)

// ScreenRect is one half-open rectangle in the rendered terminal frame.
type ScreenRect struct {
	X, Y          int
	Width, Height int
}

// SpatialFocusStop is one visible focus destination and its exact rendered
// geometry. IDs are stable; order is the visual reading order.
type SpatialFocusStop struct {
	ID   SpatialFocus
	Rect ScreenRect
}

// VisibleSpatialFocusStops reports the list and, when it actually has rendered
// columns, the detail panel. Geometry comes from the same ScreenLayout sampled
// by rendering and mouse routing.
func (m *Model) VisibleSpatialFocusStops() []SpatialFocusStop {
	layout := m.layout()
	bodyBegin, bodyEnd := layout.BodyRows()
	listBegin, listEnd := layout.ListCols()
	stops := []SpatialFocusStop{{
		ID: SpatialFocusList,
		Rect: ScreenRect{
			X: listBegin, Y: bodyBegin,
			Width: listEnd - listBegin, Height: bodyEnd - bodyBegin,
		},
	}}
	if m.spatialDetailVisible(layout) {
		panelBegin, panelEnd := layout.PanelCols()
		stops = append(stops, SpatialFocusStop{
			ID: SpatialFocusDetail,
			Rect: ScreenRect{
				X: panelBegin, Y: bodyBegin,
				Width: panelEnd - panelBegin, Height: bodyEnd - bodyBegin,
			},
		})
	}
	return stops
}

// CurrentSpatialFocus reports a visible stop. A closed or responsively hidden
// detail panel always projects to list rather than exposing a stale target.
func (m *Model) CurrentSpatialFocus() SpatialFocus {
	if m.spatialFocus == SpatialFocusDetail && m.spatialDetailVisible(m.layout()) {
		return SpatialFocusDetail
	}
	return SpatialFocusList
}

// SetSpatialFocus focuses one visible stop directly. An input or overlay keeps
// ownership until it closes; a host must not use this seam to reach behind it.
func (m *Model) SetSpatialFocus(focus SpatialFocus) bool {
	if m.TabOwnsFocus() {
		return false
	}
	switch focus {
	case SpatialFocusList:
		m.spatialFocus = focus
		return true
	case SpatialFocusDetail:
		if !m.spatialDetailVisible(m.layout()) {
			return false
		}
		m.spatialFocus = focus
		return true
	default:
		return false
	}
}

// TabOwnsFocus reports whether Tasks must receive Tab itself. Every input and
// blocking overlay owns it; passive list/detail/response contexts do not, so an
// outer deck may intercept Tab there. Standalone Tasks still receives Tab and
// retains its established "Ask" behavior.
func (m *Model) TabOwnsFocus() bool {
	if m.mode != ModeList || m.agentQuitPending || m.fieldModalQuitPending {
		return true
	}
	editor := m.taskEditor
	if editor == nil {
		editor = m.suspendedTaskEditor
	}
	return editor != nil && editor.PendingQuit()
}

func (m *Model) spatialDetailVisible(layout ScreenLayout) bool {
	if m.panel == nil || !layout.HasPanel() {
		return false
	}
	switch m.panel.Kind {
	case PanelDetail, PanelProjectDetail:
		return true
	default:
		return false
	}
}

func (m *Model) reconcileSpatialFocus() {
	if m.spatialFocus == SpatialFocusDetail && !m.spatialDetailVisible(m.layout()) {
		m.spatialFocus = SpatialFocusList
	}
}
