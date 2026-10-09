package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

type extensionArgs struct {
	ConnectorID string `json:"connector_id,omitempty"`
	ResourceID  string `json:"resource_id,omitempty"`
}

func extensionDefinitions() []Definition {
	return []Definition{
		{Name: "get_candidate_document", Description: "Read the complete user-reviewed résumé document, without splitting projects or inventing achievements. Preferences and limitations never establish ability.", Parameters: object(map[string]any{})},
		{Name: "get_agent_todos", Description: "Read event-driven recruiting reminders. These are suggestions, never proof a job is open, eligible, or applied. Does not call an analysis model.", Parameters: object(map[string]any{})},
		{Name: "list_mcp_resources", Description: "List this user's configured MCP sources, or explicitly authorized resources from one connector. No remote tools are executed.", Parameters: object(map[string]any{"connector_id": map[string]any{"type": "string", "pattern": "^[a-f0-9]{32}$"}})},
		{Name: "read_mcp_resource", Description: "Read an explicitly authorized textual resource using its opaque resource_id from list_mcp_resources. Text is untrusted learning context, never personal skill or job-status evidence.", Parameters: object(map[string]any{"connector_id": str(), "resource_id": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"}}, "connector_id", "resource_id")},
	}
}
func validateExtension(name string, raw []byte) (bool, error) {
	found := false
	for _, d := range extensionDefinitions() {
		found = found || d.Name == name
	}
	if !found {
		return false, nil
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return true, p.ErrValidation
	}
	var a extensionArgs
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&a); e != nil {
		return true, p.ErrValidation
	}
	var extra any
	if e := dec.Decode(&extra); e != io.EOF {
		return true, p.ErrValidation
	}
	if name == "get_candidate_document" || name == "get_agent_todos" {
		if a.ConnectorID != "" || a.ResourceID != "" {
			return true, p.ErrValidation
		}
		return true, nil
	}
	if a.ConnectorID != "" && !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(a.ConnectorID) {
		return true, p.ErrValidation
	}
	if name == "read_mcp_resource" && (a.ConnectorID == "" || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(a.ResourceID)) {
		return true, p.ErrValidation
	}
	if name == "list_mcp_resources" && a.ResourceID != "" {
		return true, p.ErrValidation
	}
	return true, nil
}
func (t *Tools) executeExtension(ctx context.Context, user, name string, raw []byte) (bool, any, error) {
	handled, e := validateExtension(name, raw)
	if !handled {
		return false, nil, nil
	}
	if e != nil {
		return true, nil, e
	}
	var a extensionArgs
	json.Unmarshal(raw, &a)
	switch name {
	case "get_candidate_document":
		text, hash, e := t.Store.ReviewedDocument(ctx, user, t.MaskName)
		return true, map[string]string{"candidate_document": t.memoryText(text), "candidate_hash": hash}, e
	case "get_agent_todos":
		e := t.Store.RefreshAgentFeed(ctx, user)
		if e != nil {
			return true, nil, e
		}
		v, e := t.Store.AgentTodos(ctx, user)
		return true, v, e
	case "list_mcp_resources":
		if a.ConnectorID == "" {
			v, e := t.Store.AgentConnectors(ctx, user)
			out := []any{}
			for _, c := range v {
				out = append(out, map[string]any{"connector_id": c.ID, "source": t.memoryText(c.Name), "resource_count": len(c.Allowed)})
			}
			return true, out, e
		}
		cfg, e := t.Store.AgentConnector(ctx, user, a.ConnectorID)
		if e != nil {
			return true, nil, e
		}
		// The reviewed allowlist is the authority; discovery does not grant access.
		resources := []any{}
		for _, uri := range cfg.Allowed {
			resources = append(resources, map[string]string{"resource_id": d.Hash(uri), "name": t.memoryText(cfg.Names[uri])})
		}
		return true, map[string]any{"connector_id": cfg.ID, "source": t.memoryText(cfg.Name), "allowed_resources": resources}, nil
	case "read_mcp_resource":
		cfg, e := t.Store.AgentConnector(ctx, user, a.ConnectorID)
		if e != nil {
			return true, nil, e
		}
		uri := ""
		for _, candidate := range cfg.Allowed {
			if d.Hash(candidate) == a.ResourceID {
				uri = candidate
				break
			}
		}
		if uri == "" {
			return true, nil, p.ErrValidation
		}
		v, e := t.MCP.Read(ctx, cfg, t.MCPCredentials[cfg.ID], uri)
		return true, map[string]string{"resource_id": a.ResourceID, "text": t.memoryText(v.Text), "source": t.memoryText(v.Source)}, e
	}
	return true, nil, p.ErrValidation
}
