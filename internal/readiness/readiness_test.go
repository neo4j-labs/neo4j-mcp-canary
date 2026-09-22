// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package readiness_test

import (
	"context"
	"errors"
	"testing"

	db "github.com/neo4j-labs/neo4j-mcp-canary/internal/database/mocks"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/readiness"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const gdsVersionQuery = "RETURN gds.version() as gdsVersion"

func gdsVersionRecord(version string) []*neo4j.Record {
	return []*neo4j.Record{
		{Keys: []string{"gdsVersion"}, Values: []any{version}},
	}
}

func TestChecker_Verify(t *testing.T) {
	t.Run("propagates a connectivity error without querying GDS", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1).Return(errors.New("connection error"))

		result, err := readiness.NewChecker(mockDB).Verify(context.Background())

		require.Error(t, err)
		assert.False(t, result.GDSInstalled)
	})

	t.Run("GDS installed when gds.version returns a string", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).
			Times(1).Return(gdsVersionRecord("2.22.0"), nil)

		result, err := readiness.NewChecker(mockDB).Verify(context.Background())

		require.NoError(t, err)
		assert.True(t, result.GDSInstalled)
	})

	t.Run("GDS not installed when the query errors (procedure unknown)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).
			Times(1).Return(nil, errors.New("Unknown function 'gds.version'"))

		result, err := readiness.NewChecker(mockDB).Verify(context.Background())

		require.NoError(t, err)
		assert.False(t, result.GDSInstalled)
	})

	t.Run("GDS not installed when the returned value is not a string", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).
			Times(1).Return([]*neo4j.Record{{Keys: []string{"gdsVersion"}, Values: []any{42}}}, nil)

		result, err := readiness.NewChecker(mockDB).Verify(context.Background())

		require.NoError(t, err)
		assert.False(t, result.GDSInstalled)
	})

	t.Run("GDS not installed when the result shape is unexpected", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).
			Times(1).Return([]*neo4j.Record{}, nil)

		result, err := readiness.NewChecker(mockDB).Verify(context.Background())

		require.NoError(t, err)
		assert.False(t, result.GDSInstalled)
	})
}
