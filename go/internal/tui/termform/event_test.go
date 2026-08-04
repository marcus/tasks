package termform

import (
	"testing"

	"tasks-go/internal/temporal"
)

func TestCopyValueClonesTemporalPointers(t *testing.T) {
	original := &temporal.Value{Date: temporal.Date{Year: 2026, Month: 7, Day: 14}, LocalTime: "09:00"}
	copied := copyValue(original).(*temporal.Value)
	copied.Date.Day = 20
	if original.Date.Day != 14 {
		t.Fatal("mutating the copy changed the original temporal value")
	}
	var absent *temporal.Value
	if got := copyValue(absent).(*temporal.Value); got != nil {
		t.Fatalf("nil temporal copy = %#v", got)
	}
}
