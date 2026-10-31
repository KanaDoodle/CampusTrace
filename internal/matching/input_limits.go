package matching

// Only fixed categories and counts belong in diagnostics, never input text.
type CapacityError struct {
	Reason string
	Actual int
	Limit  int
}

func (e *CapacityError) Error() string { return ErrCapacity.Error() }
func (e *CapacityError) Unwrap() error { return ErrCapacity }
