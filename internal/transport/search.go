package transport

import (
	"net/http"

	"github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

func (a *API) searchJobs(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text    string `json:"text"`
		Mode    string `json:"mode"`
		Exclude string `json:"exclude"`
		Company string `json:"company"`
		City    string `json:"city"`
		JobType string `json:"job_type"`
		Limit   int    `json:"limit"`
	}
	if err := decode(r, &in); err != nil {
		write(w, nil, err)
		return
	}
	out, err := a.Store.SearchJobs(r.Context(), user(r), searchapi.Query{Text: in.Text, Mode: in.Mode, Exclude: in.Exclude, Company: in.Company, City: in.City, JobType: in.JobType, Limit: in.Limit})
	write(w, out, err)
}
