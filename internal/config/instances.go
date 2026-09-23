// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// InstanceAuthType selects how the server connects to a single configured
// Neo4j instance in multi-instance HTTP mode (see NeoInstance).
type InstanceAuthType string

const (
	// InstanceAuthBasic uses a static service-account username/password
	// stored in the config file to connect to Neo4j, regardless of what the
	// calling MCP client presents. Because this bypasses any per-client
	// Neo4j credential, a client must still present one of Auth.APIKeys in
	// the configured API-key header to use the instance at all — otherwise
	// any caller reaching this instance's route would ride on the shared
	// service account with no authentication whatsoever.
	InstanceAuthBasic InstanceAuthType = "basic"
	// InstanceAuthBasicPassthrough forwards the calling client's own HTTP
	// Basic Auth credentials to Neo4j as-is (the same behavior single-
	// instance HTTP mode has always had), scoped to this one instance.
	InstanceAuthBasicPassthrough InstanceAuthType = "basic_passthrough"
	// InstanceAuthBearer verifies the calling client's Bearer token (issuer,
	// audience, signature, expiry) against Auth.Issuer/JWKSURI/Audience,
	// then forwards the same token to Neo4j.
	InstanceAuthBearer InstanceAuthType = "bearer"
)

// ValidInstanceAuthTypes defines the allowed InstanceAuthType values.
var ValidInstanceAuthTypes = []InstanceAuthType{InstanceAuthBasic, InstanceAuthBasicPassthrough, InstanceAuthBearer}

// instanceNamePattern constrains NeoInstance.Name to a safe, single URL path
// segment, since the name is used directly to route requests to
// "/<name>/mcp" (see internal/server).
var instanceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

// reservedInstanceNames are path segments this server already uses (or is
// expected to use) for other purposes, so they can't double as instance
// names.
var reservedInstanceNames = map[string]bool{
	"mcp":         true,
	".well-known": true,
}

// InstanceAuth describes one instance's connection auth, discriminated by
// Type. Only the fields relevant to that Type may be set; ValidateInstances
// rejects fields left over from another type so a config entry can't carry
// dead, confusing settings.
type InstanceAuth struct {
	Type InstanceAuthType `json:"type"`

	// Username/Password are required for InstanceAuthBasic: the static
	// service-account credentials this server connects to Neo4j with.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// APIKeys are the client-facing shared secrets a caller must present
	// (in the configured API-key header) to use an InstanceAuthBasic
	// instance. Required — at least one non-empty entry.
	APIKeys []string `json:"api_keys,omitempty"`

	// Issuer/JWKSURI/Audience are required for InstanceAuthBearer: the
	// identity provider this server verifies incoming bearer tokens
	// against before forwarding them to Neo4j.
	Issuer   string `json:"issuer,omitempty"`
	JWKSURI  string `json:"jwks_uri,omitempty"`
	Audience string `json:"audience,omitempty"`
}

// NeoInstance is one entry in the config file's top-level neo4j_instances
// list (see instancesKey in configfile.go). Unlike every other Config field,
// this list is intentionally unreachable from CLI flags or environment
// variables — see the comment on Config.Instances.
type NeoInstance struct {
	Name     string       `json:"name"`
	URI      string       `json:"uri"`
	Database string       `json:"database"`
	Auth     InstanceAuth `json:"auth"`
}

// ValidateInstances validates a decoded neo4j_instances list: instance names
// are non-empty, unique, and safe single URL path segments; every entry has
// a URI and a recognized, fully-populated auth type.
func ValidateInstances(instances []NeoInstance) error {
	if len(instances) == 0 {
		return fmt.Errorf("neo4j_instances must contain at least one instance if present")
	}

	seen := make(map[string]bool, len(instances))
	for i, inst := range instances {
		if inst.Name == "" {
			return fmt.Errorf("neo4j_instances[%d]: name is required", i)
		}
		if !instanceNamePattern.MatchString(inst.Name) {
			return fmt.Errorf("neo4j_instances[%d]: name %q must match %s (it is used directly as a URL path segment, e.g. /%s/mcp)", i, inst.Name, instanceNamePattern.String(), inst.Name)
		}
		if reservedInstanceNames[inst.Name] {
			return fmt.Errorf("neo4j_instances[%d]: name %q is reserved", i, inst.Name)
		}
		if seen[inst.Name] {
			return fmt.Errorf("neo4j_instances[%d]: duplicate instance name %q", i, inst.Name)
		}
		seen[inst.Name] = true

		if inst.URI == "" {
			return fmt.Errorf("neo4j_instances[%d] (%s): uri is required", i, inst.Name)
		}

		if err := validateInstanceAuth(inst.Auth); err != nil {
			return fmt.Errorf("neo4j_instances[%d] (%s): %w", i, inst.Name, err)
		}
	}
	return nil
}

func validateInstanceAuth(a InstanceAuth) error {
	if !slices.Contains(ValidInstanceAuthTypes, a.Type) {
		return fmt.Errorf("auth.type %q must be one of %v", a.Type, ValidInstanceAuthTypes)
	}

	switch a.Type {
	case InstanceAuthBasic:
		if a.Username == "" || a.Password == "" {
			return fmt.Errorf("auth.type %q requires auth.username and auth.password", a.Type)
		}
		if len(a.APIKeys) == 0 {
			return fmt.Errorf("auth.type %q requires at least one auth.api_keys entry, so a client must authenticate to use the shared service account", a.Type)
		}
		for _, k := range a.APIKeys {
			if strings.TrimSpace(k) == "" {
				return fmt.Errorf("auth.api_keys entries must not be empty")
			}
		}
		if a.Issuer != "" || a.JWKSURI != "" || a.Audience != "" {
			return fmt.Errorf("auth.type %q does not use auth.issuer/auth.jwks_uri/auth.audience", a.Type)
		}
	case InstanceAuthBasicPassthrough:
		if a.Username != "" || a.Password != "" || len(a.APIKeys) != 0 || a.Issuer != "" || a.JWKSURI != "" || a.Audience != "" {
			return fmt.Errorf("auth.type %q does not use username/password/api_keys/issuer/jwks_uri/audience", a.Type)
		}
	case InstanceAuthBearer:
		if a.Issuer == "" || a.JWKSURI == "" || a.Audience == "" {
			return fmt.Errorf("auth.type %q requires auth.issuer, auth.jwks_uri, and auth.audience", a.Type)
		}
		if a.Username != "" || a.Password != "" || len(a.APIKeys) != 0 {
			return fmt.Errorf("auth.type %q does not use username/password/api_keys", a.Type)
		}
	}
	return nil
}
