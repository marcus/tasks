package tui

import "testing"

// The mode machine is what keeps an overlay from being half-open: a mode whose
// backing object is nil is a screen the keyboard does nothing to.

func TestOverlayModesRequireTheirBackingObject(t *testing.T) {
	cases := []Mode{ModeModal, ModeForm, ModePalette, ModeContextPalette, ModeLinkPicker, ModeTaskEdit}
	for _, mode := range cases {
		harness := newModelHarness(t, harnessOptions{})
		if err := harness.model.SetMode(mode); err == nil {
			t.Errorf("entered %s with no backing object", mode)
		}
		if harness.model.Mode() != ModeList {
			t.Errorf("a refused transition still moved the mode to %s", harness.model.Mode())
		}
	}
}

func TestIllegalModeEdgesAreRefused(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SetModal(NewModal(ModalOptions{Title: "t", Lines: []string{"x"}, Kind: ModalHelp}))
	if err := harness.model.SetMode(ModeModal); err != nil {
		t.Fatal(err)
	}
	// modal -> task_edit is not an edge Ruby declares.
	harness.model.SetTaskEditor(&TaskEditorSession{})
	if err := harness.model.SetMode(ModeTaskEdit); err == nil {
		t.Error("modal -> task_edit was allowed")
	}
}

func TestModalFilterRequiresAFilterableModal(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SetModal(NewModal(ModalOptions{
		Title: "t", Lines: []string{"x"}, Kind: ModalArchiveConfirm, Filterable: false,
	}))
	if err := harness.model.SetMode(ModeModal); err != nil {
		t.Fatal(err)
	}
	if err := harness.model.SetMode(ModeModalFilter); err == nil {
		t.Error("an unfilterable modal accepted filter mode")
	}
}

func TestANonFilterableModalCannotReplaceAFilteredOne(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SetModal(NewModal(ModalOptions{
		Title: "t", Lines: []string{"x"}, Kind: ModalHelp, Filterable: true,
	}))
	_ = harness.model.SetMode(ModeModal)
	_ = harness.model.SetMode(ModeModalFilter)
	harness.model.SetModal(NewModal(ModalOptions{
		Title: "u", Lines: []string{"y"}, Kind: ModalArchiveConfirm,
	}))
	if !harness.model.Modal().Filterable() {
		t.Error("an unfilterable modal replaced the one the keyboard is filtering")
	}
}

// Removing an overlay can happen after an external reload. None of these may
// leave the mode pointing at an object that no longer exists.
func TestRemovingAnActiveOverlayRecoversToTheList(t *testing.T) {
	t.Run("modal", func(t *testing.T) {
		harness := newModelHarness(t, harnessOptions{})
		harness.model.SetModal(NewModal(ModalOptions{Title: "t", Lines: []string{"x"}, Kind: ModalHelp}))
		_ = harness.model.SetMode(ModeModal)
		harness.model.SetModal(nil)
		if harness.model.Mode() != ModeList {
			t.Errorf("mode %s after the modal vanished", harness.model.Mode())
		}
	})
	t.Run("form", func(t *testing.T) {
		harness := newModelHarness(t, harnessOptions{})
		harness.model.SetForm(NewQuickForm(QuickFormOptions{Kind: QuickFormDate, Title: "t"}))
		_ = harness.model.SetMode(ModeForm)
		harness.model.SetForm(nil)
		if harness.model.Mode() != ModeList {
			t.Errorf("mode %s after the form vanished", harness.model.Mode())
		}
	})
	t.Run("context palette", func(t *testing.T) {
		harness := newModelHarness(t, harnessOptions{})
		harness.model.SetContextPalette(NewContextPalette([]string{"@home"}, nil))
		_ = harness.model.SetMode(ModeContextPalette)
		harness.model.SetContextPalette(nil)
		if harness.model.Mode() != ModeList {
			t.Errorf("mode %s after the palette vanished", harness.model.Mode())
		}
	})
}

// A form or palette that RETURNS to a modal cannot outlive that modal, or
// dismissing it would land the user in a mode with nothing behind it.
func TestRemovingARetainedModalInvalidatesItsDependentOverlay(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SetModal(NewModal(ModalOptions{Title: "t", Lines: []string{"x"}, Kind: ModalHelp}))
	_ = harness.model.SetMode(ModeModal)
	harness.model.SetForm(NewQuickForm(QuickFormOptions{
		Kind: QuickFormDate, Title: "t", ReturnMode: ReturnModal,
	}))
	_ = harness.model.SetMode(ModeForm)

	harness.model.SetModal(nil)
	if harness.model.Form() != nil {
		t.Error("the dependent form survived its modal")
	}
	if harness.model.Mode() != ModeList {
		t.Errorf("mode %s after both vanished", harness.model.Mode())
	}
}

func TestAFormReturningToAModalNeedsThatModalToExist(t *testing.T) {
	harness := newModelHarness(t, harnessOptions{})
	harness.model.SetForm(NewQuickForm(QuickFormOptions{
		Kind: QuickFormDate, Title: "t", ReturnMode: ReturnModal,
	}))
	if harness.model.Form() != nil {
		t.Error("a modal-returning form was accepted with no modal retained")
	}
}

func TestContextFilterNormalizationIsStableAndSorted(t *testing.T) {
	got := NormalizeContextFilters([]string{"work", "@home", "@home", " ", "@"})
	if len(got) != 2 || got[0] != "@home" || got[1] != "@work" {
		t.Errorf("normalized to %v, want sorted unique sigils with the bare @ dropped", got)
	}
}
