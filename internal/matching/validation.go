package matching

// Only fixed codes and numeric positions leave the validator. Never attach
// provider text, excerpts, candidate facts or credentials to an error.
type ValidationError struct {
	Reason    string
	JobIndex  int
	ItemIndex int
	Expected  int
	Actual    int
}

func (e *ValidationError) Error() string    { return "unverifiable matching output: " + e.Reason }
func (e *ValidationError) Unwrap() error    { return ErrInvalid }
func invalid(reason string, item int) error { return &ValidationError{Reason: reason, ItemIndex: item} }
