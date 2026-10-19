// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/readiness"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/custom"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/feedback"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/gds"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/graph"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/patterns"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/search"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/traversal"
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
	if err := s.validateCustomTools(); err != nil {
		return err
	}
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

	// Graph-operation tools all emit Cypher 25; disable them on servers
	// that predate it.
	if !s.cypher25Supported {
		filters = append(filters, filterCypher25Tools)
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

func filterCypher25Tools(defs []ToolDefinition) []ToolDefinition {
	supported := make([]ToolDefinition, 0, len(defs))
	for _, t := range defs {
		switch t.Category {
		case tools.CategoryGraph, tools.CategoryTraversal, tools.CategoryPatterns:
		default:
			supported = append(supported, t)
		}
	}
	return supported
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

// validateCustomTools rejects a custom tool whose name or category collides
// with a built-in one. A shared name would silently replace the built-in
// tool, and a built-in category would subject the custom tool to that
// category's gating (GDS, search, Cypher 25).
func (s *Neo4jMCPServer) validateCustomTools() error {
	if s.config == nil || len(s.config.CustomTools) == 0 {
		return nil
	}
	builtinNames := map[string]bool{}
	for _, d := range s.builtinToolsDefs(s.buildToolDependencies()) {
		builtinNames[d.definition.Tool.Name] = true
	}
	for _, t := range s.config.CustomTools {
		if builtinNames[t.Name] {
			return fmt.Errorf("custom_tools: tool name %q is already used by a built-in tool; choose a different name", t.Name)
		}
		if slices.Contains(tools.AllCategories(), tools.Category(t.Category)) {
			return fmt.Errorf("custom_tools: tool %q uses category %q, which is a built-in category; choose a different category", t.Name, t.Category)
		}
	}
	return nil
}

// getAllToolsDefs returns the built-in tools followed by any custom tools
// from the config file.
func (s *Neo4jMCPServer) getAllToolsDefs(deps *tools.ToolDependencies) []ToolDefinition {
	defs := s.builtinToolsDefs(deps)
	if s.config == nil {
		return defs
	}
	for _, t := range s.config.CustomTools {
		defs = append(defs, ToolDefinition{
			Category: tools.Category(t.Category),
			definition: mcpsdk.ServerTool{
				Tool:    custom.Spec(t),
				Handler: custom.Handler(deps, t),
			},
			readonly: t.Access == config.CustomToolAccessRead,
		})
	}
	return defs
}

func (s *Neo4jMCPServer) builtinToolsDefs(deps *tools.ToolDependencies) []ToolDefinition {

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
		{
			Category: tools.CategorySearch,
			definition: mcpsdk.ServerTool{
				Tool:    search.CheckEmbeddingDimensionsSpec(),
				Handler: search.CheckEmbeddingDimensionsHandler(deps),
			},
			readonly: true,
		},
		// Add other categories below...
		// Graph-operation categories (graph, traversal, patterns) — parameter-only
		// tools that emit Cypher 25; gated behind s.cypher25Supported via
		// filterCypher25Tools (see getEnabledTools above).
		{
			Category: tools.CategoryGraph,
			definition: mcpsdk.ServerTool{
				Tool:    graph.CreateNodeSpec(),
				Handler: graph.CreateNodeHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryGraph,
			definition: mcpsdk.ServerTool{
				Tool:    graph.FindNodesSpec(),
				Handler: graph.FindNodesHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryGraph,
			definition: mcpsdk.ServerTool{
				Tool:    graph.GetNodeSpec(),
				Handler: graph.GetNodeHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryGraph,
			definition: mcpsdk.ServerTool{
				Tool:    graph.UpdateNodeSpec(),
				Handler: graph.UpdateNodeHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryGraph,
			definition: mcpsdk.ServerTool{
				Tool:    graph.DeleteNodeSpec(),
				Handler: graph.DeleteNodeHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryGraph,
			definition: mcpsdk.ServerTool{
				Tool:    graph.CountNodesSpec(),
				Handler: graph.CountNodesHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryGraph,
			definition: mcpsdk.ServerTool{
				Tool:    graph.CreateRelationshipSpec(),
				Handler: graph.CreateRelationshipHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryGraph,
			definition: mcpsdk.ServerTool{
				Tool:    graph.FindRelationshipsSpec(),
				Handler: graph.FindRelationshipsHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryGraph,
			definition: mcpsdk.ServerTool{
				Tool:    graph.UpdateRelationshipSpec(),
				Handler: graph.UpdateRelationshipHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryGraph,
			definition: mcpsdk.ServerTool{
				Tool:    graph.DeleteRelationshipSpec(),
				Handler: graph.DeleteRelationshipHandler(deps),
			},
			readonly: false,
		},
		{
			Category: tools.CategoryTraversal,
			definition: mcpsdk.ServerTool{
				Tool:    traversal.TraverseSpec(),
				Handler: traversal.TraverseHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryTraversal,
			definition: mcpsdk.ServerTool{
				Tool:    traversal.ExpandNeighborsSpec(),
				Handler: traversal.ExpandNeighborsHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryTraversal,
			definition: mcpsdk.ServerTool{
				Tool:    traversal.FindShortestPathSpec(),
				Handler: traversal.FindShortestPathHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryTraversal,
			definition: mcpsdk.ServerTool{
				Tool:    traversal.FindAllPathsSpec(),
				Handler: traversal.FindAllPathsHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryPatterns,
			definition: mcpsdk.ServerTool{
				Tool:    patterns.MatchPatternSpec(),
				Handler: patterns.MatchPatternHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryPatterns,
			definition: mcpsdk.ServerTool{
				Tool:    patterns.FindCommonConnectionsSpec(),
				Handler: patterns.FindCommonConnectionsHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryPatterns,
			definition: mcpsdk.ServerTool{
				Tool:    patterns.FindMotifSpec(),
				Handler: patterns.FindMotifHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryPatterns,
			definition: mcpsdk.ServerTool{
				Tool:    patterns.FindRelationshipChainSpec(),
				Handler: patterns.FindRelationshipChainHandler(deps),
			},
			readonly: true,
		},
		{
			Category: tools.CategoryPatterns,
			definition: mcpsdk.ServerTool{
				Tool:    patterns.FindSubgraphSpec(),
				Handler: patterns.FindSubgraphHandler(deps),
			},
			readonly: true,
		},
	}
}

// ToolConfigIssue is a problem with the tool-related configuration found by
// CheckToolConfig. Error issues stop the server at startup; the rest are
// warnings about configuration that is valid but will not do what it says.
type ToolConfigIssue struct {
	Error   bool
	Message string
}

// CheckToolConfig reports problems with the custom tools and the static
// tool selection (NEO4J_MCP_ENABLED_TOOLS / NEO4J_MCP_ENABLED_TOOL_CATEGORIES)
// without a database. When caps is non-nil it also reports selected tools
// the connected server can't offer.
func CheckToolConfig(cfg *config.Config, caps *readiness.Result) []ToolConfigIssue {
	s := &Neo4jMCPServer{config: cfg}
	var issues []ToolConfigIssue

	if err := s.validateCustomTools(); err != nil {
		issues = append(issues, ToolConfigIssue{Error: true, Message: err.Error()})
	}

	defs := s.getAllToolsDefs(s.buildToolDependencies())
	byName := map[string]ToolDefinition{}
	categories := map[string]bool{}
	for _, d := range defs {
		byName[d.definition.Tool.Name] = d
		categories[string(d.Category)] = true
	}
	names, selectedCategories := s.enabledToolSelection()
	for _, n := range names {
		if _, ok := byName[n]; !ok {
			issues = append(issues, ToolConfigIssue{Message: fmt.Sprintf("NEO4J_MCP_ENABLED_TOOLS names unknown tool %q, which is ignored. Check the spelling against the tool list in the README (custom tool names come from custom_tools).", n)})
		}
	}
	for _, c := range selectedCategories {
		if !categories[c] {
			issues = append(issues, ToolConfigIssue{Message: fmt.Sprintf("NEO4J_MCP_ENABLED_TOOL_CATEGORIES names unknown category %q, which is ignored. Valid categories: %v plus any custom_tools category.", c, tools.AllCategories())})
		}
	}

	for _, d := range defs {
		name := d.definition.Tool.Name
		selected := len(names) == 0 && len(selectedCategories) == 0 ||
			slices.Contains(names, name) || slices.Contains(selectedCategories, string(d.Category))
		if !selected {
			continue
		}
		if cfg.ReadOnly && !d.readonly {
			if _, custom := customToolNamed(cfg, name); custom || slices.Contains(names, name) {
				issues = append(issues, ToolConfigIssue{Message: fmt.Sprintf("tool %q modifies data and is removed because NEO4J_READ_ONLY is true. Set access: read if it only reads, or turn read-only mode off.", name)})
			}
			continue
		}
		if caps != nil {
			if reason := unavailableReason(d.Category, caps); reason != "" {
				issues = append(issues, ToolConfigIssue{Message: fmt.Sprintf("tool %q is selected but will not be registered: %s", name, reason)})
			}
		}
	}

	registered := 0
	for _, d := range defs {
		name := d.definition.Tool.Name
		selected := len(names) == 0 && len(selectedCategories) == 0 ||
			slices.Contains(names, name) || slices.Contains(selectedCategories, string(d.Category))
		if selected && !(cfg.ReadOnly && !d.readonly) {
			registered++
		}
	}
	if registered == 0 {
		issues = append(issues, ToolConfigIssue{Message: "the tool selection and read-only settings leave no tools enabled; the server would start with an empty tool list."})
	}
	return issues
}

func customToolNamed(cfg *config.Config, name string) (config.CustomTool, bool) {
	for _, t := range cfg.CustomTools {
		if t.Name == name {
			return t, true
		}
	}
	return config.CustomTool{}, false
}

func unavailableReason(c tools.Category, caps *readiness.Result) string {
	switch c {
	case tools.CategoryGDS:
		if !caps.GDSInstalled {
			return "the Graph Data Science plugin is not installed on the connected server."
		}
	case tools.CategorySearch:
		if !caps.SearchVersionSupported {
			return "the connected Neo4j version is too old for the search category (needs 2026.09 or Aura 5.27)."
		}
	case tools.CategoryGraph, tools.CategoryTraversal, tools.CategoryPatterns:
		if !caps.Cypher25Supported {
			return "the connected Neo4j version does not support Cypher 25, which this category requires."
		}
	}
	return ""
}
