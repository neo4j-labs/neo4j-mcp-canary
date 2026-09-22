// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"log/slog"
	"slices"
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/feedback"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/gds"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/search"
)

// registerTools registers all enabled MCP tools and adds them to the provided MCP server.
// Tools are filtered according to the server configuration. For example, when the read-only
// mode is enabled (e.g. via the NEO4J_READ_ONLY environment variable or the Config.ReadOnly flag),
// any tool that performs state mutation will be excluded; only tools annotated as read-only will be registered.
// Note: this read-only filtering relies on the tool annotation "readonly" (ReadOnlyHint). If the annotation
// is not defined or is set to false, the tool will be added (i.e., only tools with readonly=true are filtered in read-only mode).
// Tools can also be statically narrowed by name or category via Config.EnabledTools /
// Config.EnabledToolCategories — see filterBySelection.
func (s *Neo4jMCPServer) registerTools() error {
	filteredTools := s.getEnabledTools()
	s.mcpServer.AddTools(filteredTools...)
	return nil
}

type toolFilter func(defs []ToolDefinition) []ToolDefinition

// ToolDefinition pairs an mcpsdk.ServerTool with the metadata used to decide
// whether it's registered: its Category (mandatory — every tool belongs to
// exactly one) and whether it's readonly.
type ToolDefinition struct {
	Category   tools.Category
	definition mcpsdk.ServerTool
	readonly   bool
}

// Label returns the tool's human-readable label, i.e. its MCP title
// annotation (see mcpsdk.WithTitleAnnotation), falling back to the tool's
// wire name if no title annotation was set.
func (d ToolDefinition) Label() string {
	if d.definition.Tool.Annotations != nil && d.definition.Tool.Annotations.Title != "" {
		return d.definition.Tool.Annotations.Title
	}
	return d.definition.Tool.Name
}

// reregisterOptionalTools re-runs the tool filter pipeline and re-adds
// every currently-enabled tool. Used after a readiness capability (GDS
// installed, or the server version now clearing the search category's
// floor) is newly confirmed available on the HTTP lazy-verification path —
// see verifyOnFirstRequest in server.go. mcpsdk.Server.AddTools replaces by
// name, so this is safe to call even when nothing newly qualified.
func (s *Neo4jMCPServer) reregisterOptionalTools() {
	filteredTools := s.getEnabledTools()
	s.mcpServer.AddTools(filteredTools...)
}

func (s *Neo4jMCPServer) getEnabledTools() []mcpsdk.ServerTool {
	filters := make([]toolFilter, 0)

	// Narrow to a caller-selected set of tools/categories first, if configured.
	names, categories := s.enabledToolSelection()
	if len(names) > 0 || len(categories) > 0 {
		filters = append(filters, filterBySelection(names, categories))
	}
	// If read-only mode is enabled, expose only tools annotated as read-only.
	if s.config != nil && s.config.ReadOnly {
		filters = append(filters, filterWriteTools)
	}
	// If GDS is not installed, disable GDS tools.
	if !s.gdsInstalled {
		filters = append(filters, filterGDSTools)
	}
	// If the connected server doesn't clear the search category's version
	// floor (its SEARCH-clause syntax won't parse below it), disable search
	// tools.
	if !s.searchVersionSupported {
		filters = append(filters, filterSearchTools)
	}

	deps := s.buildToolDependencies()
	toolDefs := s.getAllToolsDefs(deps)

	warnOnUnknownSelection(toolDefs, names, categories)

	for _, filter := range filters {
		toolDefs = filter(toolDefs)
	}
	enabledTools := make([]mcpsdk.ServerTool, 0)
	for _, toolDef := range toolDefs {
		enabledTools = append(enabledTools, toolDef.definition)
	}
	return enabledTools
}

// enabledToolSelection parses Config.EnabledTools / Config.EnabledToolCategories
// into trimmed, comma-separated lists. Either or both may be empty, meaning
// no restriction on that dimension.
func (s *Neo4jMCPServer) enabledToolSelection() (names, categories []string) {
	if s.config == nil {
		return nil, nil
	}
	return parseCommaList(s.config.EnabledTools), parseCommaList(s.config.EnabledToolCategories)
}

// warnOnUnknownSelection logs a warning for any configured tool name or
// category that doesn't match a known tool, so a typo is visible in the
// logs rather than silently disabling nothing.
func warnOnUnknownSelection(defs []ToolDefinition, names, categories []string) {
	knownNames := make(map[string]bool, len(defs))
	knownCategories := make(map[string]bool, len(defs))
	for _, d := range defs {
		knownNames[d.definition.Tool.Name] = true
		knownCategories[string(d.Category)] = true
	}
	for _, n := range names {
		if !knownNames[n] {
			slog.Warn("NEO4J_MCP_ENABLED_TOOLS contains an unknown tool name", "name", n)
		}
	}
	for _, c := range categories {
		if !knownCategories[c] {
			slog.Warn("NEO4J_MCP_ENABLED_TOOL_CATEGORIES contains an unknown category", "category", c)
		}
	}
}

// filterBySelection keeps a tool if it matches either the configured tool
// names or the configured categories (union semantics — either match is
// sufficient). It is a no-op (returns defs unchanged) when both names and
// categories are empty.
func filterBySelection(names, categories []string) toolFilter {
	return func(defs []ToolDefinition) []ToolDefinition {
		if len(names) == 0 && len(categories) == 0 {
			return defs
		}
		selected := make([]ToolDefinition, 0, len(defs))
		for _, d := range defs {
			if slices.Contains(names, d.definition.Tool.Name) || slices.Contains(categories, string(d.Category)) {
				selected = append(selected, d)
			}
		}
		return selected
	}
}

func filterWriteTools(defs []ToolDefinition) []ToolDefinition {
	readOnlyTools := make([]ToolDefinition, 0, len(defs))
	for _, t := range defs {
		if t.readonly {
			readOnlyTools = append(readOnlyTools, t)
		}
	}
	return readOnlyTools
}

func filterGDSTools(defs []ToolDefinition) []ToolDefinition {
	nonGDSTools := make([]ToolDefinition, 0, len(defs))
	for _, t := range defs {
		if t.Category != tools.CategoryGDS {
			nonGDSTools = append(nonGDSTools, t)
		}
	}
	return nonGDSTools
}

func filterSearchTools(defs []ToolDefinition) []ToolDefinition {
	nonSearchTools := make([]ToolDefinition, 0, len(defs))
	for _, t := range defs {
		if t.Category != tools.CategorySearch {
			nonSearchTools = append(nonSearchTools, t)
		}
	}
	return nonSearchTools
}

// buildToolDependencies creates a ToolDependencies with all config wired through.
//
// CypherMaxBytes is halved here: read-cypher/write-cypher now always attach
// the canonical JSON as structuredContent alongside the text block (see
// NewToolResultTextAndStructured), so the same payload goes out twice.
// NEO4J_CYPHER_MAX_BYTES documents the combined per-call budget; halving the
// value actually enforced during streaming keeps text+structured together
// under that budget rather than doubling it.
func (s *Neo4jMCPServer) buildToolDependencies() *tools.ToolDependencies {
	return &tools.ToolDependencies{
		DBService:              s.dbService,
		AnalyticsService:       s.anService,
		OutputFormat:           s.config.OutputFormat,
		SchemaSampleSize:       int(s.config.SchemaSampleSize),
		CypherMaxRows:          int(s.config.CypherMaxRows),
		CypherMaxBytes:         int(s.config.CypherMaxBytes) / 2,
		CypherTimeout:          time.Duration(s.config.CypherTimeoutSeconds) * time.Second,
		CypherMaxEstimatedRows: int(s.config.CypherMaxEstimatedRows),
	}
}

// getAllToolsDefs returns all available tools with their specs and handlers
func (s *Neo4jMCPServer) getAllToolsDefs(deps *tools.ToolDependencies) []ToolDefinition {

	return []ToolDefinition{
		{
			Category: tools.CategoryCypher,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.GetSchemaSpec(),
				Handler: cypher.GetSchemaHandler(deps, s.config.SchemaSampleSize),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryCypher,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.ReadCypherSpec(),
				Handler: cypher.ReadCypherHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryCypher,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.WriteCypherSpec(),
				Handler: cypher.WriteCypherHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryCypher,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.ExplainCypherSpec(),
				Handler: cypher.ExplainCypherHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryCypher,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.ProfileCypherSpec(),
				Handler: cypher.ProfileCypherHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryCypher,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.ListConstraintsAndIndexesSpec(),
				Handler: cypher.ListConstraintsAndIndexesHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryCypher,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.CreateConstraintSpec(),
				Handler: cypher.CreateConstraintHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryCypher,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.DropConstraintSpec(),
				Handler: cypher.DropConstraintHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryCypher,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.CreateIndexSpec(),
				Handler: cypher.CreateIndexHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryCypher,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.DropIndexSpec(),
				Handler: cypher.DropIndexHandler(deps),
			},
			readonly: false,
		},
		// GDS Category/Section
		{
			Category: tools.CategoryGDS,
			definition: mcpsdk.ServerTool{
				Tool:    gds.ListGDSProceduresSpec(),
				Handler: gds.ListGdsProceduresHandler(deps),
			},
			readonly: true,
		},
		// Feedback Category/Section
		{
			Category: tools.CategoryFeedback,
			definition: mcpsdk.ServerTool{
				Tool:    feedback.GiveFeedbackSpec(),
				Handler: feedback.GiveFeedbackHandler(deps),
			},
			readonly: true,
		},
		// Search Category/Section — gated behind s.searchVersionSupported via
		// filterSearchTools (see getEnabledTools below); the connected server
		// must clear the search category's own version floor (calendar
		// >= 2026.09.0 or classic-Aura >= 5.27-aura) for these tools to
		// register at all, since their SEARCH-clause syntax won't parse
		// below it.
		{
			Category: tools.CategorySearch,
			definition: mcpsdk.ServerTool{
				Tool:    search.VectorSearchSpec(),
				Handler: search.VectorSearchHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategorySearch,
			definition: mcpsdk.ServerTool{
				Tool:    search.FullTextSearchSpec(),
				Handler: search.FullTextSearchHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategorySearch,
			definition: mcpsdk.ServerTool{
				Tool:    search.CreateVectorIndexSpec(),
				Handler: search.CreateVectorIndexHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategorySearch,
			definition: mcpsdk.ServerTool{
				Tool:    search.CreateFullTextIndexSpec(),
				Handler: search.CreateFullTextIndexHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategorySearch,
			definition: mcpsdk.ServerTool{
				Tool:    search.SetVectorPropertySpec(),
				Handler: search.SetVectorPropertyHandler(deps),
			},
			readonly: false,
		},
		// Add other categories below...
	}
}
