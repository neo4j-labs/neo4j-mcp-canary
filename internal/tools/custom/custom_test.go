// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package custom_test

import (
	"context"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/tools/custom"
)

func tool(access config.CustomToolAccess) config.CustomTool {
	return config.CustomTool{
		Name:        "find-customers",
		Description: "Find customers by tier.",
		Category:    "crm",
		Access:      access,
		Statement:   "MATCH (c:Customer {tier: $tier}) RETURN c LIMIT $limit",
		Params: []config.CustomToolParam{
			{Name: "tier", Type: config.CustomParamString, Description: "Tier", Required: true, Enum: []any{"gold", "silver"}},
			{Name: "limit", Type: config.CustomParamInteger, Description: "Max rows", Default: 10.0},
		},
	}
}

func request(args map[string]any) *mcpsdk.CallToolRequest {
	return &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParams{Name: "find-customers", Arguments: args}}
}

func TestSpec(t *testing.T) {
	spec := custom.Spec(tool(config.CustomToolAccessRead))
	assert.Equal(t, "find-customers", spec.Name)
	assert.True(t, spec.Annotations.ReadOnlyHint)
	schema := spec.InputSchema.(*jsonschema.Schema)
	assert.Equal(t, []string{"tier"}, schema.Required)
	assert.Equal(t, "integer", schema.Properties["limit"].Type)
	assert.Equal(t, []any{"gold", "silver"}, schema.Properties["tier"].Enum)

	write := custom.Spec(tool(config.CustomToolAccessReadWrite))
	assert.False(t, write.Annotations.ReadOnlyHint)
	assert.True(t, *write.Annotations.DestructiveHint)
}

func TestBuildParams(t *testing.T) {
	def := tool(config.CustomToolAccessRead)

	params, err := custom.BuildParams(def, map[string]any{"tier": "gold"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"tier": "gold", "limit": int64(10)}, params)

	params, err = custom.BuildParams(def, map[string]any{"tier": "silver", "limit": 5.0})
	require.NoError(t, err)
	assert.Equal(t, int64(5), params["limit"])

	for name, args := range map[string]map[string]any{
		"missing required": {},
		"bad enum":         {"tier": "bronze"},
		"wrong type":       {"tier": "gold", "limit": "many"},
		"unknown argument": {"tier": "gold", "extra": 1},
	} {
		_, err := custom.BuildParams(def, args)
		assert.Error(t, err, name)
	}
}

func deps(m *db.MockService) *tools.ToolDependencies {
	return &tools.ToolDependencies{DBService: m, OutputFormat: config.OutputFormatJSON, CypherMaxRows: 100}
}

func okResult() *database.QueryResult {
	return &database.QueryResult{Records: []*neo4j.Record{}}
}

func TestHandler_ReadSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	m := db.NewMockService(ctrl)
	def := tool(config.CustomToolAccessRead)
	want := map[string]any{"tier": "gold", "limit": int64(10)}
	m.EXPECT().GetQueryType(gomock.Any(), def.Statement, want).Return(neo4j.QueryTypeReadOnly, nil)
	m.EXPECT().ExecuteReadQueryStreaming(gomock.Any(), def.Statement, want, 100, 0).Return(okResult(), nil)
	m.EXPECT().QueryResultToJSON(gomock.Any()).Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

	res, err := custom.Handler(deps(m), def)(context.Background(), request(map[string]any{"tier": "gold"}))
	require.NoError(t, err)
	assert.False(t, res.IsError)
}

func TestHandler_ReadToolThatWritesIsRefused(t *testing.T) {
	ctrl := gomock.NewController(t)
	m := db.NewMockService(ctrl)
	def := tool(config.CustomToolAccessRead)
	m.EXPECT().GetQueryType(gomock.Any(), def.Statement, gomock.Any()).Return(neo4j.QueryTypeWriteOnly, nil)

	res, err := custom.Handler(deps(m), def)(context.Background(), request(map[string]any{"tier": "gold"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestHandler_ReadWriteUsesWriteTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	m := db.NewMockService(ctrl)
	def := tool(config.CustomToolAccessReadWrite)
	m.EXPECT().ExecuteWriteQueryStreaming(gomock.Any(), def.Statement, gomock.Any(), 100, 0).Return(okResult(), nil)
	m.EXPECT().QueryResultToJSON(gomock.Any()).Return(`{"rows":[],"rowCount":0,"truncated":false}`, nil)

	res, err := custom.Handler(deps(m), def)(context.Background(), request(map[string]any{"tier": "gold"}))
	require.NoError(t, err)
	assert.False(t, res.IsError)
}

func TestHandler_InvalidArgumentsDoNotTouchDatabase(t *testing.T) {
	ctrl := gomock.NewController(t)
	m := db.NewMockService(ctrl)

	res, err := custom.Handler(deps(m), tool(config.CustomToolAccessRead))(context.Background(), request(map[string]any{"tier": "bronze"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}
