package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
)

// JSON escaping can make a valid 60 KB text document exceed the ordinary
// request limit. Bound its encoded body separately without raising every API.
func decodeKnowledgeDocument(r *http.Request, v *rag.Document) error {
	raw, e := io.ReadAll(io.LimitReader(r.Body, 400001))
	if e != nil {
		return e
	}
	if len(raw) > 400000 {
		return p.ErrValidation
	}
	return d.StrictLimit(raw, v, 400000)
}

func (a *API) requestRAG(options rag.Options) (*rag.Service, error) {
	if e := options.Validate(); e != nil {
		return nil, e
	}
	if a.Tools == nil || a.Tools.RAG == nil {
		if options.Embedding == nil && options.Rerank == nil {
			return &rag.Service{Store: a.Store}, nil
		}
		return nil, p.ErrBackendUnavailable
	}
	copy := *a.Tools.RAG
	copy.Options = options
	copy.Provider.Before = func(ctx context.Context) error {
		if copy.Allow != nil {
			ok, e := copy.Allow(ctx)
			if e != nil {
				return e
			}
			if !ok {
				return &rag.ProviderError{Code: "RETRIEVAL_RATE_LIMIT"}
			}
		}
		return nil
	}
	return &copy, nil
}
func knowledgeWrite(w http.ResponseWriter, v any, e error) {
	w.Header().Set("Cache-Control", "no-store")
	if errors.Is(e, p.ErrStaleInput) {
		codedError(w, http.StatusConflict, "RETRIEVAL_INPUT_CHANGED")
		return
	}
	var provider *rag.ProviderError
	if errors.As(e, &provider) {
		codedError(w, http.StatusBadRequest, provider.Code)
		return
	}
	if errors.Is(e, modelconfig.ErrInvalid) {
		codedError(w, http.StatusBadRequest, "MODEL_CONFIG_INVALID")
		return
	}
	write(w, v, e)
}
func (a *API) knowledgeRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/knowledge/state", func(w http.ResponseWriter, r *http.Request) {
		version, e := a.Store.KnowledgeIdentity(r.Context(), user(r))
		knowledgeWrite(w, map[string]string{"version": version}, e)
	})
	on("POST /api/documents/import", func(w http.ResponseWriter, r *http.Request) {
		var v rag.Document
		if decodeKnowledgeDocument(r, &v) != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		doc, reused, e := a.Tools.RAG.ImportDocument(r.Context(), user(r), v)
		knowledgeWrite(w, map[string]any{"id": doc.ID, "title": doc.Title, "reused": reused}, e)
	})
	on("GET /api/documents", func(w http.ResponseWriter, r *http.Request) {
		offset := 0
		var e error
		if v := r.URL.Query().Get("offset"); v != "" {
			offset, e = strconv.Atoi(v)
		}
		if e != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		out, e := a.Tools.RAG.Documents(r.Context(), user(r), offset)
		knowledgeWrite(w, out, e)
	})
	on("GET /api/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		out, e := p.One[rag.Document](r.Context(), a.Store.DB, "SELECT body FROM documents WHERE id=? AND user_id=?", r.PathValue("id"), user(r))
		knowledgeWrite(w, out, e)
	})
	on("DELETE /api/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		e := a.Tools.RAG.DeleteDocument(r.Context(), user(r), r.PathValue("id"))
		knowledgeWrite(w, map[string]bool{"deleted": e == nil}, e)
	})
	on("DELETE /api/knowledge/index", func(w http.ResponseWriter, r *http.Request) {
		e := a.Tools.RAG.ClearVectors(r.Context(), user(r))
		knowledgeWrite(w, map[string]bool{"cleared": e == nil}, e)
	})
	on("POST /api/knowledge/index/preview", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Retrieval rag.Options `json:"retrieval"`
		}
		if decode(r, &v) != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		service, e := a.requestRAG(v.Retrieval)
		if e != nil {
			knowledgeWrite(w, nil, e)
			return
		}
		out, e := service.PreviewIndex(r.Context(), user(r))
		knowledgeWrite(w, out, e)
	})
	on("POST /api/knowledge/index", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Retrieval rag.Options `json:"retrieval"`
			Key       string      `json:"key"`
			Confirm   bool        `json:"confirm"`
		}
		if decode(r, &v) != nil || !v.Confirm || len(v.Key) != 64 {
			write(w, nil, p.ErrValidation)
			return
		}
		service, e := a.requestRAG(v.Retrieval)
		if e != nil {
			knowledgeWrite(w, nil, e)
			return
		}
		out, e := service.Index(r.Context(), user(r), v.Key)
		knowledgeWrite(w, out, e)
	})
	on("POST /api/knowledge/search", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Query     string      `json:"query"`
			K         int         `json:"k"`
			Retrieval rag.Options `json:"retrieval"`
		}
		if decode(r, &v) != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		if v.K == 0 {
			v.K = 5
		}
		service, e := a.requestRAG(v.Retrieval)
		if e != nil {
			knowledgeWrite(w, nil, e)
			return
		}
		out, e := service.SearchDetailed(r.Context(), user(r), v.Query, v.K)
		knowledgeWrite(w, out, e)
	})
}
