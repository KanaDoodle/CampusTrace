package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
	"github.com/KanaDoodle/CampusTrace/internal/searchservice"
)

// The isolated coordinator tests drive the real index without a broker. This
// fixture route is served only by test processes, including kill/restart tests.
const fixtureBatchPath = "/fixture/search-batch"

type fixtureSearchClient struct{ *a.Client }

func newFixtureSearchClient(endpoint, queryToken, writeToken string) (*fixtureSearchClient, error) {
	client, err := a.NewClient(endpoint, queryToken, writeToken)
	if err != nil {
		return nil, err
	}
	return &fixtureSearchClient{client}, nil
}

func (c *fixtureSearchClient) Apply(ctx context.Context, batch a.Batch) error {
	raw, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+fixtureBatchPath, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.WriteToken)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return a.ErrUnavailable
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusBadRequest:
		return a.ErrInvalid
	case http.StatusConflict:
		return a.ErrConflict
	default:
		return a.ErrUnavailable
	}
}

func searchFixtureHandler(server *searchservice.Server) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", server.Handler())
	mux.HandleFunc("POST "+fixtureBatchPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+server.WriteToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var batch a.Batch
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := server.Engine.Apply(r.Context(), batch); err != nil {
			if err == a.ErrConflict {
				w.WriteHeader(http.StatusConflict)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
