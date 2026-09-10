package transport

import (
	"context"
	"encoding/json"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func MCP(tools agent.Toolset, user string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "campustrace", Version: "1.0.0"}, nil)
	allowed := map[string]bool{"search_jobs": true, "get_job": true, "get_job_evidence": true, "get_job_eligibility": true, "search_knowledge": true}
	for _, def := range tools.Definitions() {
		if !allowed[def.Name] {
			continue
		}
		name := def.Name
		s.AddTool(&mcp.Tool{Name: name, Description: def.Description, InputSchema: def.Parameters}, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			raw := json.RawMessage(r.Params.Arguments)
			if err := agent.Validate(name, raw); err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Invalid arguments"}}}, nil
			}
			v, err := tools.Execute(ctx, user, name, raw)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Tool failed; no facts established"}}}, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: d.JSON(v)}}}, nil
		})
	}
	return s
}
