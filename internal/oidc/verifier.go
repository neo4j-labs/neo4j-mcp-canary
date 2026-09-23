// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// Package oidc verifies client-presented Bearer tokens for multi-instance
// HTTP mode's "bearer" auth type (see internal/config.InstanceAuthBearer):
// this server acts as an OAuth 2.0 resource server, checking a token's
// signature, issuer, audience, and expiry against the identity provider a
// bearer-type instance is configured with — never running the
// authorization-code/login flow itself. That flow is the MCP client's
// responsibility, per the MCP Authorization spec; this package only ever
// validates the token the client ends up presenting.
package oidc

import (
	"context"
	"fmt"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// acceptedAlgorithms restricts verification to asymmetric signing
// algorithms. JWKS-sourced keys are always asymmetric public keys, so
// accepting a symmetric (HS*) or "none" algorithm here would let a
// malicious token's alg header talk this server into either treating a
// public key as an HMAC secret or skipping signature verification
// entirely — the classic "alg confusion" / "alg: none" JWT
// vulnerabilities. jwt.WithValidMethods enforces this list regardless of
// what the token itself claims.
var acceptedAlgorithms = []string{
	"RS256", "RS384", "RS512",
	"ES256", "ES384", "ES512",
	"PS256", "PS384", "PS512",
}

// Verifier verifies Bearer tokens against one identity provider's JWKS
// endpoint. It holds a background-refreshing key cache (via
// github.com/MicahParks/keyfunc), so Verify itself never makes a network
// call — construction is the only place that can be slow or fail.
type Verifier struct {
	keyfunc jwt.Keyfunc
}

// NewVerifier builds a Verifier for one JWKS endpoint. ctx bounds the
// verifier's background key-refresh goroutine — it should be a long-lived,
// server-lifetime context (cancelled on shutdown), not a per-request one.
func NewVerifier(ctx context.Context, jwksURI string) (*Verifier, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURI})
	if err != nil {
		return nil, fmt.Errorf("oidc: failed to fetch JWKS from %q: %w", jwksURI, err)
	}
	return &Verifier{keyfunc: kf.Keyfunc}, nil
}

// Verify checks tokenString's signature (against the JWKS this Verifier
// was built from), standard time-based claims (exp/nbf/iat — exp is
// required, not merely checked if present), and that its issuer/audience
// match issuer/audience exactly. A non-nil error means the token must be
// rejected.
func (v *Verifier) Verify(tokenString, issuer, audience string) error {
	claims := jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenString, &claims, v.keyfunc,
		jwt.WithValidMethods(acceptedAlgorithms),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return fmt.Errorf("oidc: token verification failed: %w", err)
	}
	if !token.Valid {
		return fmt.Errorf("oidc: token is not valid")
	}
	return nil
}

// VerifierRegistry holds one Verifier per distinct JWKS endpoint used by
// this server's bearer-type instances. Instances that happen to share an
// IdP's jwks_uri share a Verifier — and its single background refresh
// goroutine — rather than each running its own.
type VerifierRegistry struct {
	verifiers map[string]*Verifier // keyed by jwks_uri
}

// NewVerifierRegistry builds a Verifier for every distinct jwks_uri among
// instances' bearer-type auth config, skipping non-bearer instances
// entirely. ctx bounds every verifier's background key-refresh goroutine —
// see NewVerifier.
func NewVerifierRegistry(ctx context.Context, instances []config.NeoInstance) (*VerifierRegistry, error) {
	reg := &VerifierRegistry{verifiers: make(map[string]*Verifier)}
	for _, inst := range instances {
		if inst.Auth.Type != config.InstanceAuthBearer {
			continue
		}
		if _, ok := reg.verifiers[inst.Auth.JWKSURI]; ok {
			continue
		}
		v, err := NewVerifier(ctx, inst.Auth.JWKSURI)
		if err != nil {
			return nil, fmt.Errorf("oidc: instance %q: %w", inst.Name, err)
		}
		reg.verifiers[inst.Auth.JWKSURI] = v
	}
	return reg, nil
}

// VerifyBearer has the shape of server.BearerVerifyFunc (a func type, not
// an interface this package could implement against directly without
// importing internal/server and creating a cycle): it looks up the
// Verifier for instanceAuth's jwks_uri and checks token against its
// issuer/audience. Wire it up with srv.SetBearerVerifier(registry.VerifyBearer).
func (r *VerifierRegistry) VerifyBearer(_ context.Context, instanceAuth config.InstanceAuth, token string) error {
	v, ok := r.verifiers[instanceAuth.JWKSURI]
	if !ok {
		return fmt.Errorf("oidc: no verifier configured for jwks_uri %q", instanceAuth.JWKSURI)
	}
	return v.Verify(token, instanceAuth.Issuer, instanceAuth.Audience)
}
