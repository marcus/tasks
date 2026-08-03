package main

// Placeholders for the surfaces landing in the following commits. They refuse
// rather than answer, so a case that reaches one fails loudly instead of
// reporting an empty list as a successful read.
func (s *surfaceContext) agenda(args []string) int { return notImplemented("agenda") }
func (s *surfaceContext) undo(args []string) int   { return notImplemented("undo") }
func (s *surfaceContext) done(args []string) int   { return notImplemented("done") }

func notImplemented(name string) int {
	return abort(name + ": not implemented in the Go port yet")
}
