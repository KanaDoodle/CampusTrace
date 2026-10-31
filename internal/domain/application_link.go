package domain

// ApplicationLink is an account-scoped destination without résumé/notes.
type ApplicationLink struct {
	ID    string `json:"id"`
	State string `json:"current_state"`
}
