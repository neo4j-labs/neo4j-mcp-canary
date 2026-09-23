// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package oidc_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/oidc"

	"github.com/MicahParks/jwkset"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testKID      = "test-kid"
	testIssuer   = "https://idp.example.com/"
	testAudience = "api://neo4j-mcp"
)

// newTestJWKSServer starts an httptest.Server serving a JWKS document
// containing pub's public key under testKID, mimicking a real IdP's JWKS
// endpoint closely enough for keyfunc.NewDefaultCtx to fetch and cache it.
func newTestJWKSServer(t *testing.T, pub *rsa.PublicKey) *httptest.Server {
	t.Helper()

	jwk, err := jwkset.NewJWKFromKey(pub, jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{KID: testKID, ALG: jwkset.AlgRS256, USE: jwkset.UseSig},
	})
	if err != nil {
		t.Fatalf("failed to build test JWK: %v", err)
	}
	body, err := json.Marshal(jwkset.JWKSMarshal{Keys: []jwkset.JWKMarshal{jwk.Marshal()}})
	if err != nil {
		t.Fatalf("failed to marshal test JWKS: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

// signTestToken mints an RS256 JWT signed by priv, with kid=testKID so
// keyfunc's Keyfunc resolves it against the JWKS server above.
func signTestToken(t *testing.T, priv *rsa.PrivateKey, claims jwt.RegisteredClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKID
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("failed to sign test token: %v", err)
	}
	return signed
}

func validClaims() jwt.RegisteredClaims {
	now := time.Now()
	return jwt.RegisteredClaims{
		Issuer:    testIssuer,
		Audience:  jwt.ClaimStrings{testAudience},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		IssuedAt:  jwt.NewNumericDate(now),
	}
}

func TestVerifier_Verify(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate other test key: %v", err)
	}

	server := newTestJWKSServer(t, &priv.PublicKey)
	verifier, err := oidc.NewVerifier(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("NewVerifier() unexpected error: %v", err)
	}

	t.Run("valid token is accepted", func(t *testing.T) {
		token := signTestToken(t, priv, validClaims())
		if err := verifier.Verify(token, testIssuer, testAudience); err != nil {
			t.Errorf("Verify() unexpected error: %v", err)
		}
	})

	t.Run("expired token is rejected", func(t *testing.T) {
		claims := validClaims()
		claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))
		token := signTestToken(t, priv, claims)

		if err := verifier.Verify(token, testIssuer, testAudience); err == nil {
			t.Error("Verify() expected error for expired token, got nil")
		}
	})

	t.Run("wrong audience is rejected", func(t *testing.T) {
		claims := validClaims()
		claims.Audience = jwt.ClaimStrings{"some-other-audience"}
		token := signTestToken(t, priv, claims)

		if err := verifier.Verify(token, testIssuer, testAudience); err == nil {
			t.Error("Verify() expected error for wrong audience, got nil")
		}
	})

	t.Run("wrong issuer is rejected", func(t *testing.T) {
		claims := validClaims()
		claims.Issuer = "https://not-the-configured-idp.example.com/"
		token := signTestToken(t, priv, claims)

		if err := verifier.Verify(token, testIssuer, testAudience); err == nil {
			t.Error("Verify() expected error for wrong issuer, got nil")
		}
	})

	t.Run("token signed by a different key is rejected", func(t *testing.T) {
		token := signTestToken(t, otherKey, validClaims())

		if err := verifier.Verify(token, testIssuer, testAudience); err == nil {
			t.Error("Verify() expected error for a signature from an untrusted key, got nil")
		}
	})

	t.Run("token missing expiry is rejected", func(t *testing.T) {
		claims := validClaims()
		claims.ExpiresAt = nil
		token := signTestToken(t, priv, claims)

		if err := verifier.Verify(token, testIssuer, testAudience); err == nil {
			t.Error("Verify() expected error for a token with no exp claim, got nil")
		}
	})

	t.Run("alg:none token is rejected", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims())
		token.Header["kid"] = testKID
		signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatalf("failed to sign alg:none test token: %v", err)
		}

		if err := verifier.Verify(signed, testIssuer, testAudience); err == nil {
			t.Error("Verify() expected error for an alg:none token, got nil")
		}
	})

	t.Run("malformed token is rejected", func(t *testing.T) {
		if err := verifier.Verify("not-a-jwt", testIssuer, testAudience); err == nil {
			t.Error("Verify() expected error for a malformed token, got nil")
		}
	})
}

func TestNewVerifierRegistry(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	server := newTestJWKSServer(t, &priv.PublicKey)

	bearerInstance := config.NeoInstance{
		Name: "analytics",
		Auth: config.InstanceAuth{Type: config.InstanceAuthBearer, Issuer: testIssuer, JWKSURI: server.URL, Audience: testAudience},
	}
	basicInstance := config.NeoInstance{
		Name: "prod",
		Auth: config.InstanceAuth{Type: config.InstanceAuthBasic, Username: "u", Password: "p", APIKeys: []string{"k"}},
	}

	registry, err := oidc.NewVerifierRegistry(context.Background(), []config.NeoInstance{bearerInstance, basicInstance})
	if err != nil {
		t.Fatalf("NewVerifierRegistry() unexpected error: %v", err)
	}

	t.Run("verifies a valid token for the bearer instance", func(t *testing.T) {
		token := signTestToken(t, priv, validClaims())
		if err := registry.VerifyBearer(context.Background(), bearerInstance.Auth, token); err != nil {
			t.Errorf("VerifyBearer() unexpected error: %v", err)
		}
	})

	t.Run("no verifier configured for a non-bearer instance", func(t *testing.T) {
		err := registry.VerifyBearer(context.Background(), basicInstance.Auth, "irrelevant")
		if err == nil || !strings.Contains(err.Error(), "no verifier configured") {
			t.Errorf("VerifyBearer() error = %v, want 'no verifier configured'", err)
		}
	})
}
