package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ToolSearchName is the name of the built-in tool that loads deferred tools.
const ToolSearchName = "tool_search"

type toolSearchArgs struct {
	Query      string `json:"query" jsonschema:"required" jsonschema_description:"Either 'select:<name>[,<name>...]' to load specific deferred tools by exact name, or keywords to search tool names and descriptions. Prefix a keyword with + to require it in the tool name, e.g. '+github issue'."`
	MaxResults int    `json:"max_results" jsonschema_description:"Maximum number of keyword matches to return (default 5, max 20). Ignored for 'select:'."`
}

// searchResult is both what tool_search returns and what Session.Restore parses
// back out of the transcript on later turns.
type searchResult struct {
	Query         string        `json:"query"`
	Matches       []searchMatch `json:"matches"`
	NotFound      []string      `json:"not_found,omitempty"`
	TotalDeferred int           `json:"total_deferred_tools"`
	Note          string        `json:"note"`
}

type searchMatch struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// newToolSearchTool builds tool_search. It works on the turn's Session found in
// ctx; called outside an agent turn it falls back to a throwaway snapshot of
// the registry (nothing is remembered, but searching still works).
func newToolSearchTool(reg *Registry) (tool.InvokableTool, error) {
	return utils.InferTool(
		ToolSearchName,
		"Find and load deferred tools. Some tools (from MCP servers and other external sources) are only listed by name in the system prompt and cannot be called until loaded. Call this with 'select:<name>' for a known name, or keywords to search; the result contains each matched tool's description and input schema, and those tools become callable on your next step. Never call a deferred tool without loading it first.",
		func(ctx context.Context, a toolSearchArgs) (searchResult, error) {
			q := strings.TrimSpace(a.Query)
			if q == "" {
				return searchResult{}, fmt.Errorf("query is required")
			}
			s := SessionFrom(ctx)
			if s == nil {
				s = reg.NewSession()
			}

			found, missing := s.Search(q, a.MaxResults)
			res := searchResult{
				Query:         q,
				Matches:       make([]searchMatch, 0, len(found)),
				NotFound:      missing,
				TotalDeferred: len(s.catalog),
			}
			names := make([]string, 0, len(found))
			for _, e := range found {
				schemaJSON, err := inputSchemaJSON(e)
				if err != nil {
					return searchResult{}, fmt.Errorf("tool %q: %w", e.Name, err)
				}
				res.Matches = append(res.Matches, searchMatch{Name: e.Name, Description: e.Desc, InputSchema: schemaJSON})
				names = append(names, e.Name)
			}
			s.Discover(names...)

			switch {
			case len(res.Matches) > 0:
				res.Note = "The tools above are now loaded: call them directly on your next step, using the input_schema for their arguments."
			case len(missing) > 0:
				res.Note = "No deferred tool has that exact name. Check the names listed in the system prompt, or search with keywords."
			default:
				res.Note = "No deferred tool matched. Try different or fewer keywords, or use 'select:<name>' with a name from the system prompt."
			}
			return res, nil
		},
	)
}

// inputSchemaJSON renders a tool's parameter schema as JSON, or an empty object
// schema for tools that take no arguments.
func inputSchemaJSON(e *Entry) (json.RawMessage, error) {
	if e.Info == nil || e.Info.ParamsOneOf == nil {
		return json.RawMessage(`{"type":"object","properties":{}}`), nil
	}
	js, err := e.Info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		return nil, err
	}
	return json.Marshal(js)
}
