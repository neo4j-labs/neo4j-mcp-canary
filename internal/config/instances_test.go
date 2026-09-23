// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package config

import (
	"strings"
	"testing"
)

func validBasicInstance(name string) NeoInstance {
	return NeoInstance{
		Name:     name,
		URI:      "neo4j+s://" + name + ".databases.neo4j.io",
		Database: "neo4j",
		Auth: InstanceAuth{
			Type:     InstanceAuthBasic,
			Username: "mcp_service",
			Password: "s3cret",
			APIKeys:  []string{"key-1"},
		},
	}
}

func TestValidateInstances(t *testing.T) {
	tests := []struct {
		name      string
		instances []NeoInstance
		wantErr   string // substring; empty means no error expected
	}{
		{
			name:      "empty list is an error",
			instances: nil,
			wantErr:   "must contain at least one instance",
		},
		{
			name:      "valid basic instance",
			instances: []NeoInstance{validBasicInstance("prod")},
		},
		{
			name: "valid basic_passthrough instance",
			instances: []NeoInstance{{
				Name: "staging",
				URI:  "neo4j://staging.internal:7687",
				Auth: InstanceAuth{Type: InstanceAuthBasicPassthrough},
			}},
		},
		{
			name: "valid bearer instance",
			instances: []NeoInstance{{
				Name: "analytics",
				URI:  "neo4j+s://analytics.databases.neo4j.io",
				Auth: InstanceAuth{
					Type:     InstanceAuthBearer,
					Issuer:   "https://idp.example.com/",
					JWKSURI:  "https://idp.example.com/.well-known/jwks.json",
					Audience: "api://neo4j-mcp",
				},
			}},
		},
		{
			name: "missing name",
			instances: []NeoInstance{func() NeoInstance {
				i := validBasicInstance("prod")
				i.Name = ""
				return i
			}()},
			wantErr: "name is required",
		},
		{
			name: "name with path separator rejected",
			instances: []NeoInstance{func() NeoInstance {
				i := validBasicInstance("prod")
				i.Name = "prod/evil"
				return i
			}()},
			wantErr: "must match",
		},
		{
			name: "reserved name rejected",
			instances: []NeoInstance{func() NeoInstance {
				i := validBasicInstance("prod")
				i.Name = "mcp"
				return i
			}()},
			wantErr: "reserved",
		},
		{
			name:      "duplicate names rejected",
			instances: []NeoInstance{validBasicInstance("prod"), validBasicInstance("prod")},
			wantErr:   "duplicate instance name",
		},
		{
			name: "missing uri",
			instances: []NeoInstance{func() NeoInstance {
				i := validBasicInstance("prod")
				i.URI = ""
				return i
			}()},
			wantErr: "uri is required",
		},
		{
			name: "unknown auth type",
			instances: []NeoInstance{{
				Name: "prod", URI: "neo4j://x:7687",
				Auth: InstanceAuth{Type: "oauth2"},
			}},
			wantErr: "must be one of",
		},
		{
			name: "basic without password",
			instances: []NeoInstance{{
				Name: "prod", URI: "neo4j://x:7687",
				Auth: InstanceAuth{Type: InstanceAuthBasic, Username: "u", APIKeys: []string{"k"}},
			}},
			wantErr: "requires auth.username and auth.password",
		},
		{
			name: "basic without api keys",
			instances: []NeoInstance{{
				Name: "prod", URI: "neo4j://x:7687",
				Auth: InstanceAuth{Type: InstanceAuthBasic, Username: "u", Password: "p"},
			}},
			wantErr: "requires at least one auth.api_keys entry",
		},
		{
			name: "basic with blank api key",
			instances: []NeoInstance{{
				Name: "prod", URI: "neo4j://x:7687",
				Auth: InstanceAuth{Type: InstanceAuthBasic, Username: "u", Password: "p", APIKeys: []string{"  "}},
			}},
			wantErr: "must not be empty",
		},
		{
			name: "basic with leftover bearer fields",
			instances: []NeoInstance{{
				Name: "prod", URI: "neo4j://x:7687",
				Auth: InstanceAuth{Type: InstanceAuthBasic, Username: "u", Password: "p", APIKeys: []string{"k"}, Issuer: "https://idp"},
			}},
			wantErr: "does not use auth.issuer",
		},
		{
			name: "basic_passthrough with leftover fields",
			instances: []NeoInstance{{
				Name: "prod", URI: "neo4j://x:7687",
				Auth: InstanceAuth{Type: InstanceAuthBasicPassthrough, Username: "u"},
			}},
			wantErr: "does not use username/password",
		},
		{
			name: "bearer missing issuer",
			instances: []NeoInstance{{
				Name: "prod", URI: "neo4j://x:7687",
				Auth: InstanceAuth{Type: InstanceAuthBearer, JWKSURI: "https://x", Audience: "aud"},
			}},
			wantErr: "requires auth.issuer",
		},
		{
			name: "bearer with leftover basic fields",
			instances: []NeoInstance{{
				Name: "prod", URI: "neo4j://x:7687",
				Auth: InstanceAuth{Type: InstanceAuthBearer, Issuer: "https://idp", JWKSURI: "https://x", Audience: "aud", Username: "u"},
			}},
			wantErr: "does not use username/password/api_keys",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInstances(tt.instances)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateInstances() unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateInstances() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
