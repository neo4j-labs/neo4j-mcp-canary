// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package graphutil

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/mcpsdk"
)

// checkArguments rejects arguments the tool doesn't declare. Binding into a
// struct silently drops unknown fields, so a misspelt optional argument
// (neighborProperties for nodeProperties) would otherwise be ignored and the
// caller would get a result that quietly lacks what they asked for.
func checkArguments(toolName string, request *mcpsdk.CallToolRequest, t reflect.Type) error {
	if request.Params == nil || len(request.Params.Arguments) == 0 {
		return nil
	}
	valid := jsonFieldNames(t)
	if len(valid) == 0 {
		return nil
	}
	for name := range request.Params.Arguments {
		if !slices.ContainsFunc(valid, func(v string) bool { return strings.EqualFold(v, name) }) {
			slices.Sort(valid)
			return fmt.Errorf("%s: unknown argument %q. Valid arguments: %s. Fix the argument name and retry", toolName, name, strings.Join(valid, ", "))
		}
	}
	return nil
}

// jsonFieldNames lists the JSON names a struct accepts, including those of
// embedded structs, which encoding/json flattens into the parent.
func jsonFieldNames(t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var names []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, hasTag := f.Tag.Lookup("json")
		name, _, _ := strings.Cut(tag, ",")
		switch {
		case name == "-":
		case f.Anonymous && name == "":
			names = append(names, jsonFieldNames(f.Type)...)
		case !f.IsExported():
		case name != "":
			names = append(names, name)
		case !hasTag || name == "":
			names = append(names, f.Name)
		}
	}
	return names
}

// UndeclaredInputFields returns the properties a tool's schema advertises
// that its input struct has no field for. Handlers reject unknown
// arguments, so every advertised property must bind; tool packages assert
// this is empty for each tool.
func UndeclaredInputFields(spec mcpsdk.Tool, input any) []string {
	schema, ok := spec.InputSchema.(*jsonschema.Schema)
	if !ok || schema == nil {
		return nil
	}
	valid := jsonFieldNames(reflect.TypeOf(input))
	var missing []string
	for name := range schema.Properties {
		if !slices.Contains(valid, name) {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}
