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
const componentsQuery = "CALL dbms.components()"

func gdsVersionRecord(version string) []*neo4j.Record {
	return []*neo4j.Record{
		{Keys: []string{"gdsVersion"}, Values: []any{version}},
	}
}

// kernelComponentsRecord builds a single-row CALL dbms.components() result
// carrying just the "Neo4j Kernel" row readiness.kernelVersion reads —
// enough for these tests, mirroring internal/eventing's own record shape.
func kernelComponentsRecord(version string) []*neo4j.Record {
	return []*neo4j.Record{
		{
			Keys:   []string{"name", "versions", "edition"},
			Values: []any{"Neo4j Kernel", []any{version}, "enterprise"},
		},
	}
}

func TestChecker_Verify(t *testing.T) {
	t.Run("propagates a connectivity error without querying GDS or version", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1).Return(errors.New("connection error"))

		result, err := readiness.NewChecker(mockDB, "bolt://test-host:7687").Verify(context.Background())

		require.Error(t, err)
		assert.False(t, result.GDSInstalled)
		assert.False(t, result.SearchVersionSupported)
	})

	t.Run("GDS installed when gds.version returns a string", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).
			Times(1).Return(gdsVersionRecord("2.22.0"), nil)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), componentsQuery, gomock.Any()).
			Times(1).Return(kernelComponentsRecord("2026.09.0"), nil)

		result, err := readiness.NewChecker(mockDB, "bolt://test-host:7687").Verify(context.Background())

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
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), componentsQuery, gomock.Any()).
			Times(1).Return(kernelComponentsRecord("2026.09.0"), nil)

		result, err := readiness.NewChecker(mockDB, "bolt://test-host:7687").Verify(context.Background())

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
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), componentsQuery, gomock.Any()).
			Times(1).Return(kernelComponentsRecord("2026.09.0"), nil)

		result, err := readiness.NewChecker(mockDB, "bolt://test-host:7687").Verify(context.Background())

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
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), componentsQuery, gomock.Any()).
			Times(1).Return(kernelComponentsRecord("2026.09.0"), nil)

		result, err := readiness.NewChecker(mockDB, "bolt://test-host:7687").Verify(context.Background())

		require.NoError(t, err)
		assert.False(t, result.GDSInstalled)
	})

	t.Run("search version not supported when dbms.components query errors", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).
			Times(1).Return(nil, errors.New("boom"))
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), componentsQuery, gomock.Any()).
			Times(1).Return(nil, errors.New("boom"))

		result, err := readiness.NewChecker(mockDB, "bolt://test-host:7687").Verify(context.Background())

		require.NoError(t, err)
		assert.False(t, result.SearchVersionSupported)
	})

	t.Run("search version not supported below the calendar floor", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).
			Times(1).Return(nil, errors.New("boom"))
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), componentsQuery, gomock.Any()).
			Times(1).Return(kernelComponentsRecord("2026.08.0"), nil)

		result, err := readiness.NewChecker(mockDB, "bolt://test-host:7687").Verify(context.Background())

		require.NoError(t, err)
		assert.False(t, result.SearchVersionSupported)
	})

	t.Run("search version supported at or above the calendar floor", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).
			Times(1).Return(nil, errors.New("boom"))
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), componentsQuery, gomock.Any()).
			Times(1).Return(kernelComponentsRecord("2026.09.0"), nil)

		result, err := readiness.NewChecker(mockDB, "bolt://test-host:7687").Verify(context.Background())

		require.NoError(t, err)
		assert.True(t, result.SearchVersionSupported)
	})

	t.Run("search version supported for a bare classic version on an Aura host", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).
			Times(1).Return(nil, errors.New("boom"))
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), componentsQuery, gomock.Any()).
			Times(1).Return(kernelComponentsRecord("5.27.0"), nil)

		result, err := readiness.NewChecker(mockDB, "neo4j+s://my-instance.databases.neo4j.io").Verify(context.Background())

		require.NoError(t, err)
		assert.True(t, result.SearchVersionSupported)
	})

	t.Run("search version not supported for a bare classic version on a non-Aura host", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDB := db.NewMockService(ctrl)
		mockDB.EXPECT().VerifyConnectivity(gomock.Any()).Times(1)
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), gdsVersionQuery, gomock.Any()).
			Times(1).Return(nil, errors.New("boom"))
		mockDB.EXPECT().ExecuteReadQuery(gomock.Any(), componentsQuery, gomock.Any()).
			Times(1).Return(kernelComponentsRecord("5.27.0"), nil)

		result, err := readiness.NewChecker(mockDB, "bolt://self-managed.example.com:7687").Verify(context.Background())

		require.NoError(t, err)
		assert.False(t, result.SearchVersionSupported)
	})
}
