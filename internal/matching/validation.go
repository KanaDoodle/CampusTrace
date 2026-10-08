package matching

// Only fixed codes and numeric positions leave the validator. Never attach
// provider text, excerpts, candidate facts or credentials to an error.
type ValidationError struct {
	Reason           string
	Scope            string
	JobIndex         int
	ItemIndex        int
	RelatedItemIndex int
	Expected         int
	Actual           int
}

func (e *ValidationError) Error() string    { return "unverifiable matching output: " + e.Reason }
func (e *ValidationError) Unwrap() error    { return ErrInvalid }
func invalid(reason string, item int) error { return &ValidationError{Reason: reason, ItemIndex: item} }

// Chat imports report independent failures together. The first error remains
// discoverable with errors.As for callers using the original single-error API.
type ChatValidationErrors struct {
	Issues []ValidationError
}

func (e *ChatValidationErrors) Error() string { return "unverifiable chat matching output" }
func (e *ChatValidationErrors) Unwrap() []error {
	out := make([]error, len(e.Issues))
	for i := range e.Issues {
		out[i] = &e.Issues[i]
	}
	return out
}
