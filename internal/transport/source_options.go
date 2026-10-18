package transport

import d "github.com/KanaDoodle/CampusTrace/internal/domain"

// Scheduling constraints are response metadata, not stored source configuration.
type sourceOption struct {
	d.Source
	MinimumInterval int `json:"minimum_interval"`
}

func sourceOptions(sources []d.Source) []sourceOption {
	out := make([]sourceOption, 0, len(sources))
	for _, src := range sources {
		out = append(out, sourceOption{Source: src, MinimumInterval: d.SourceMinimumInterval(src.Adapter)})
	}
	return out
}
