package agent

import (
	"encoding/json"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"strings"
	"unicode/utf8"
)

func (r *Runtime) outputBudgets() (int, int, int, int) {
	value := func(n, def int) int {
		if n <= 0 {
			return def
		}
		return n
	}
	final := value(r.MaxFinalBytes, 163840)
	if final < 512 {
		final = 512
	}
	return value(r.MaxToolResultBytes, 32768), value(r.MaxFactsBytes, 98304), value(r.MaxAnswerBytes, 32768), final
}
func prefixUTF8(s string, n int) string {
	if n >= len(s) {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Include the Event envelope and actual SSE framing, not only the Result JSON.
func (r *Runtime) limitFinal(v *Result, answer, final int) {
	if len(v.Answer) > answer {
		v.Answer = prefixUTF8(v.Answer, answer)
		v.Terminal = "OUTPUT_LIMIT"
	}
	size := func() int { return len(d.JSON(Event{Type: "final", Data: *v})) + len("event: final\ndata: \n\n") }
	if size() <= final {
		return
	}
	v.Terminal = "OUTPUT_LIMIT"
	v.Answer = "Output budget reached; narrow the query."
	for size() > final && len(v.Facts) > 0 {
		v.Facts = v.Facts[:len(v.Facts)-1]
	}
	for size() > final && len(v.Steps) > 0 {
		v.Steps = v.Steps[:len(v.Steps)-1]
	}
	if len(v.Answer) > answer {
		v.Answer = prefixUTF8(v.Answer, answer)
	}
}
func canonicalCall(c Call) string {
	if Validate(c.Name, c.Args) != nil {
		return c.Name + ":" + string(c.Args)
	}
	var v any
	switch c.Name {
	case "create_application":
		var a p.CreateArgs
		json.Unmarshal(c.Args, &a)
		v = a
	case "transition_application":
		var a p.TransitionArgs
		json.Unmarshal(c.Args, &a)
		v = a
	case "record_interview_review":
		var a d.Review
		json.Unmarshal(c.Args, &a)
		v = a
	default:
		decoder := json.NewDecoder(strings.NewReader(string(c.Args)))
		decoder.UseNumber()
		decoder.Decode(&v)
	}
	return c.Name + ":" + d.JSON(v)
}
