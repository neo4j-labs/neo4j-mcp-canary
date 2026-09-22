// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package eventing_test

import (
	"context"
	"testing"

	analyticsReal "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics"
	analytics "github.com/neo4j-labs/neo4j-mcp-canary/internal/analytics/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/eventing"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func toolRequest(name, query string) *mcpsdk.CallToolRequest {
	args := map[string]any{}
	if query != "" {
		args["query"] = query
	}
	return &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParams{Name: name, Arguments: args}}
}

func TestEmitter_OnToolCallComplete(t *testing.T) {
	cfg := &config.Config{OutputFormat: config.OutputFormatJSON}

	t.Run("does nothing when analytics is disabled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(false)
		// No EmitEvent/NewToolEvent expectations: a call would fail the mock.

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("read-cypher", "MATCH (n) RETURN n"), mcpsdk.NewToolResultText("ok"))
	})

	t.Run("reports success from a non-error result", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewToolEvent("give-feedback", true, (*analyticsReal.ToolVectorInfo)(nil), config.OutputFormatJSON).Times(1)
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(1)

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("give-feedback", ""), mcpsdk.NewToolResultText("thanks"))
	})

	t.Run("reports failure from an error result", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewToolEvent("give-feedback", false, gomock.Any(), config.OutputFormatJSON).Times(1)
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(1)

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("give-feedback", ""), mcpsdk.NewToolResultError("nope"))
	})

	t.Run("attaches vector info for a vector-search tool call", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		var gotVectorSearch *bool
		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewToolEvent("vector-search", true, gomock.Any(), config.OutputFormatJSON).
			Times(1).
			Do(func(_ string, _ bool, vectorInfo *analyticsReal.ToolVectorInfo, _ config.OutputFormat) {
				if vectorInfo != nil {
					gotVectorSearch = vectorInfo.VectorSearch
				}
			})
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(1)

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("vector-search", ""), mcpsdk.NewToolResultText("ok"))

		if assert.NotNil(t, gotVectorSearch) {
			assert.True(t, *gotVectorSearch)
		}
	})

	t.Run("attaches vector info for a fulltext-search tool call", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		var gotFullTextSearch *bool
		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewToolEvent("fulltext-search", true, gomock.Any(), config.OutputFormatJSON).
			Times(1).
			Do(func(_ string, _ bool, vectorInfo *analyticsReal.ToolVectorInfo, _ config.OutputFormat) {
				if vectorInfo != nil {
					gotFullTextSearch = vectorInfo.FullTextSearch
				}
			})
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(1)

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("fulltext-search", ""), mcpsdk.NewToolResultText("ok"))

		if assert.NotNil(t, gotFullTextSearch) {
			assert.True(t, *gotFullTextSearch)
		}
	})

	t.Run("attaches vector info for a set-vector-property tool call", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		var gotVectorPropertySet *bool
		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewToolEvent("set-vector-property", true, gomock.Any(), config.OutputFormatJSON).
			Times(1).
			Do(func(_ string, _ bool, vectorInfo *analyticsReal.ToolVectorInfo, _ config.OutputFormat) {
				if vectorInfo != nil {
					gotVectorPropertySet = vectorInfo.VectorPropertySet
				}
			})
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(1)

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("set-vector-property", ""), mcpsdk.NewToolResultText("ok"))

		if assert.NotNil(t, gotVectorPropertySet) {
			assert.True(t, *gotVectorPropertySet)
		}
	})

	t.Run("attaches vector info for explain-cypher and profile-cypher, matching read/write-cypher", func(t *testing.T) {
		for _, tool := range []string{"explain-cypher", "profile-cypher"} {
			t.Run(tool, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				defer ctrl.Finish()

				var gotVectorSearch *bool
				analyticsService := analytics.NewMockService(ctrl)
				analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
				analyticsService.EXPECT().NewToolEvent(tool, true, gomock.Any(), config.OutputFormatJSON).
					Times(1).
					Do(func(_ string, _ bool, vectorInfo *analyticsReal.ToolVectorInfo, _ config.OutputFormat) {
						if vectorInfo != nil {
							gotVectorSearch = vectorInfo.VectorSearch
						}
					})
				analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(1)

				e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
				e.OnToolCallComplete(context.Background(), toolRequest(tool, "MATCH (n) RETURN db.index.vector.queryNodes('idx', 5, n.embedding)"), mcpsdk.NewToolResultText("ok"))

				if assert.NotNil(t, gotVectorSearch) {
					assert.True(t, *gotVectorSearch)
				}
			})
		}
	})

	t.Run("emits a GDS project-created event when profile-cypher runs gds.graph.project", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewToolEvent("profile-cypher", true, gomock.Any(), config.OutputFormatJSON).Times(1)
		analyticsService.EXPECT().NewGDSProjCreatedEvent().Times(1)
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(2) // tool event + GDS project-created event

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("profile-cypher", "CALL gds.graph.project('g', '*', '*')"), mcpsdk.NewToolResultText("ok"))
	})

	t.Run("does not emit a GDS event for explain-cypher, since EXPLAIN never executes", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewToolEvent("explain-cypher", true, gomock.Any(), config.OutputFormatJSON).Times(1)
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(1)
		// No NewGDSProjCreatedEvent expectation: explain-cypher never executes
		// the query, so no projection was actually created.

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("explain-cypher", "CALL gds.graph.project('g', '*', '*')"), mcpsdk.NewToolResultText("ok"))
	})

	t.Run("emits a GDS project-created event when read-cypher runs gds.graph.project", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewToolEvent("read-cypher", true, gomock.Any(), config.OutputFormatJSON).Times(1)
		analyticsService.EXPECT().NewGDSProjCreatedEvent().Times(1)
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(2) // tool event + GDS project-created event

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("read-cypher", "CALL gds.graph.project('g', '*', '*')"), mcpsdk.NewToolResultText("ok"))
	})

	t.Run("emits a GDS project-dropped event when write-cypher runs gds.graph.drop", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewToolEvent("write-cypher", true, gomock.Any(), config.OutputFormatJSON).Times(1)
		analyticsService.EXPECT().NewGDSProjDropEvent().Times(1)
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(2) // tool event + GDS project-dropped event

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("write-cypher", "CALL gds.graph.drop('g')"), mcpsdk.NewToolResultText("ok"))
	})

	t.Run("does not emit a GDS event for a non-cypher tool even with a matching query arg", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		analyticsService := analytics.NewMockService(ctrl)
		analyticsService.EXPECT().IsEnabled().Times(1).Return(true)
		analyticsService.EXPECT().NewToolEvent("get-schema", true, gomock.Any(), config.OutputFormatJSON).Times(1)
		analyticsService.EXPECT().EmitEvent(gomock.Any()).Times(1)
		// No NewGDSProjCreatedEvent/NewGDSProjDropEvent expectations: get-schema
		// isn't a cypher tool, so emitGDSEventsIfNeeded must not run.

		e := eventing.NewEmitter(analyticsService, db.NewMockService(ctrl), cfg, "test-version")
		e.OnToolCallComplete(context.Background(), toolRequest("get-schema", "CALL gds.graph.project('g', '*', '*')"), mcpsdk.NewToolResultText(`{"indexes":[]}`))
	})
}
