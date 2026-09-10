// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package server

import (
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/cypher"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/feedback"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/gds"
)

// registerTools registers all enabled MCP tools and adds them to the provided MCP server.
// Tools are filtered according to the server configuration. For example, when the read-only
// mode is enabled (e.g. via the NEO4J_READ_ONLY environment variable or the Config.ReadOnly flag),
// any tool that performs state mutation will be excluded; only tools annotated as read-only will be registered.
// Note: this read-only filtering relies on the tool annotation "readonly" (ReadOnlyHint). If the annotation
// is not defined or is set to false, the tool will be added (i.e., only tools with readonly=true are filtered in read-only mode).
func (s *Neo4jMCPServer) registerTools() error {
	filteredTools := s.getEnabledTools()
	s.mcpServer.AddTools(filteredTools...)
	return nil
}

type toolFilter func(tools []ToolDefinition) []ToolDefinition

type toolCategory int

const (
	cypherCategory   toolCategory = 0
	gdsCategory      toolCategory = 1
	feedbackCategory toolCategory = 2
)

type ToolDefinition struct {
	category   toolCategory
	definition mcpsdk.ServerTool
	readonly   bool
}

func (s *Neo4jMCPServer) addGDSTools() {
	filteredTools := s.getEnabledTools()
	s.mcpServer.AddTools(filteredTools...)
}

func (s *Neo4jMCPServer) getEnabledTools() []mcpsdk.ServerTool {
	filters := make([]toolFilter, 0)

	// If read-only mode is enabled, expose only tools annotated as read-only.
	if s.config != nil && s.config.ReadOnly {
		filters = append(filters, filterWriteTools)
	}
	// If GDS is not installed, disable GDS tools.
	if !s.gdsInstalled {
		filters = append(filters, filterGDSTools)
	}

	deps := s.buildToolDependencies()
	toolDefs := s.getAllToolsDefs(deps)

	for _, filter := range filters {
		toolDefs = filter(toolDefs)
	}
	enabledTools := make([]mcpsdk.ServerTool, 0)
	for _, toolDef := range toolDefs {
		enabledTools = append(enabledTools, toolDef.definition)
	}
	return enabledTools
}

func filterWriteTools(tools []ToolDefinition) []ToolDefinition {
	readOnlyTools := make([]ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if t.readonly {
			readOnlyTools = append(readOnlyTools, t)
		}
	}
	return readOnlyTools
}

func filterGDSTools(tools []ToolDefinition) []ToolDefinition {
	nonGDSTools := make([]ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if t.category != gdsCategory {
			nonGDSTools = append(nonGDSTools, t)
		}
	}
	return nonGDSTools
}

// buildToolDependencies creates a ToolDependencies with all config wired through.
func (s *Neo4jMCPServer) buildToolDependencies() *tools.ToolDependencies {
	return &tools.ToolDependencies{
		DBService:              s.dbService,
		AnalyticsService:       s.anService,
		OutputFormat:           s.config.OutputFormat,
		SchemaSampleSize:       int(s.config.SchemaSampleSize),
		CypherMaxRows:          int(s.config.CypherMaxRows),
		CypherMaxBytes:         int(s.config.CypherMaxBytes),
		CypherTimeout:          time.Duration(s.config.CypherTimeoutSeconds) * time.Second,
		CypherMaxEstimatedRows: int(s.config.CypherMaxEstimatedRows),
	}
}

// getAllToolsDefs returns all available tools with their specs and handlers
func (s *Neo4jMCPServer) getAllToolsDefs(deps *tools.ToolDependencies) []ToolDefinition {

	return []ToolDefinition{
		{
			category: cypherCategory,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.GetSchemaSpec(),
				Handler: cypher.GetSchemaHandler(deps, s.config.SchemaSampleSize),
			},
			readonly: true,
		},
		{
			category: cypherCategory,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.ReadCypherSpec(),
				Handler: cypher.ReadCypherHandler(deps),
			},
			readonly: true,
		},
		{
			category: cypherCategory,
			definition: mcpsdk.ServerTool{
				Tool:    cypher.WriteCypherSpec(),
				Handler: cypher.WriteCypherHandler(deps),
			},
			readonly: false,
		},
		// GDS Category/Section
		{
			category: gdsCategory,
			definition: mcpsdk.ServerTool{
				Tool:    gds.ListGDSProceduresSpec(),
				Handler: gds.ListGdsProceduresHandler(deps),
			},
			readonly: true,
		},
		// Feedback Category/Section
		{
			category: feedbackCategory,
			definition: mcpsdk.ServerTool{
				Tool:    feedback.GiveFeedbackSpec(),
				Handler: feedback.GiveFeedbackHandler(deps),
			},
			readonly: true,
		},
		// Add other categories below...
	}
}
