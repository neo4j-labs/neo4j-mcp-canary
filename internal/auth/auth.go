// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package auth

import "context"

type contextKey string

const (
	basicAuthUserKey           contextKey = "basicAuthUser"
	basicAuthPassKey           contextKey = "basicAuthPass"
	bearerTokenKey             contextKey = "bearerToken"
	toolSelectionNamesKey      contextKey = "toolSelectionNames"
	toolSelectionCategoriesKey contextKey = "toolSelectionCategories"
	instanceSelectionKey       contextKey = "instanceSelection"
)

// WithBasicAuth adds basic auth credentials to the context
func WithBasicAuth(ctx context.Context, user, pass string) context.Context {
	ctx = context.WithValue(ctx, basicAuthUserKey, user)
	ctx = context.WithValue(ctx, basicAuthPassKey, pass)
	return ctx
}

// GetBasicAuthCredentials retrieves basic auth credentials from the context
func GetBasicAuthCredentials(ctx context.Context) (string, string, bool) {
	user, okUser := ctx.Value(basicAuthUserKey).(string)
	pass, okPass := ctx.Value(basicAuthPassKey).(string)
	return user, pass, okUser && okPass
}

// WithBearerToken adds bearer token to the context
func WithBearerToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, bearerTokenKey, token)
}

// GetBearerToken retrieves bearer token from the context
func GetBearerToken(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(bearerTokenKey).(string)
	return token, ok
}

// HasAuth checks if either basic auth or bearer token is present in the context
func HasAuth(ctx context.Context) bool {
	_, _, okBasic := GetBasicAuthCredentials(ctx)
	_, okBearer := GetBearerToken(ctx)
	return okBasic || okBearer
}

// WithToolSelection adds a per-request tool selection (by name and/or
// category, as read from HTTP headers) to the context. Either slice may be
// empty; GetToolSelection reports ok=false only when this was never called
// for the request at all.
func WithToolSelection(ctx context.Context, names, categories []string) context.Context {
	ctx = context.WithValue(ctx, toolSelectionNamesKey, names)
	ctx = context.WithValue(ctx, toolSelectionCategoriesKey, categories)
	return ctx
}

// GetToolSelection retrieves the per-request tool selection from the
// context. ok is false if WithToolSelection was never called for this
// request (e.g. stdio mode, or neither selection header was present).
func GetToolSelection(ctx context.Context) (names, categories []string, ok bool) {
	names, okNames := ctx.Value(toolSelectionNamesKey).([]string)
	categories, okCategories := ctx.Value(toolSelectionCategoriesKey).([]string)
	return names, categories, okNames && okCategories
}

// WithInstanceSelection records which configured Neo4j instance (see
// config.NeoInstance) a request targets. Unlike the other per-request
// values in this file, the instance is resolved once per HTTP route at
// server startup — from the "/<name>/mcp" path the route is registered
// under — rather than parsed from each incoming request, so this is set
// unconditionally by that route's middleware chain, never left unset in
// multi-instance mode.
func WithInstanceSelection(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, instanceSelectionKey, name)
}

// GetInstanceSelection retrieves the selected instance name from the
// context. ok is false outside multi-instance mode (single-instance HTTP
// mode and STDIO mode never call WithInstanceSelection).
func GetInstanceSelection(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(instanceSelectionKey).(string)
	return name, ok
}
