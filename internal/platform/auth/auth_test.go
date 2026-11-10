package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/example/go-skeleton/internal/domain"
)

// fakeKeycloak serves a JWKS and signs tokens the way a Keycloak realm does.
type fakeKeycloak struct {
	*httptest.Server
	key *rsa.PrivateKey
}

func newFakeKeycloak(t *testing.T) *fakeKeycloak {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test-key",
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}}})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /realms/test/protocol/openid-connect/certs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &fakeKeycloak{Server: srv, key: key}
}

func (f *fakeKeycloak) issuer() string { return f.URL + "/realms/test" }

func (f *fakeKeycloak) sign(t *testing.T, key *rsa.PrivateKey, edit func(jwt.MapClaims)) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":                f.issuer(),
		"sub":                "user-123",
		"aud":                "account",
		"typ":                "Bearer",
		"azp":                "skeleton-cli",
		"exp":                time.Now().Add(time.Minute).Unix(),
		"iat":                time.Now().Unix(),
		"preferred_username": "demo",
		"email":              "demo@example.com",
		"scope":              "openid profile",
		"realm_access":       map[string]any{"roles": []string{"user"}},
		"resource_access":    map[string]any{"skeleton-api": map[string]any{"roles": []string{"editor"}}},
	}
	if edit != nil {
		edit(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-key"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func newAuthenticator(t *testing.T, opts Options) *Authenticator {
	t.Helper()
	a, err := New(opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(a.Close)
	return a
}

func TestAuthenticateValidToken(t *testing.T) {
	kc := newFakeKeycloak(t)
	a := newAuthenticator(t, Options{Issuer: kc.issuer(), Audience: "account"})

	p, err := a.Authenticate(t.Context(), kc.sign(t, kc.key, nil))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.Subject != "user-123" || p.Username != "demo" || p.Email != "demo@example.com" || p.ClientID != "skeleton-cli" {
		t.Errorf("principal = %+v", p)
	}
	if !p.HasRole("user") || !p.HasRole("skeleton-api:editor") {
		t.Errorf("roles = %v, want realm role user and client role skeleton-api:editor", p.Roles)
	}
	if !p.HasScope("profile") {
		t.Errorf("scopes = %v, want profile", p.Scopes)
	}
}

func TestAuthenticateRejects(t *testing.T) {
	kc := newFakeKeycloak(t)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	a := newAuthenticator(t, Options{Issuer: kc.issuer(), Audience: "account"})

	hmacToken, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": kc.issuer(), "sub": "user-123", "aud": "account", "exp": time.Now().Add(time.Minute).Unix(),
	}).SignedString([]byte("secret"))

	for name, token := range map[string]string{
		"garbage":           "not-a-token",
		"expired":           kc.sign(t, kc.key, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() }),
		"no expiry":         kc.sign(t, kc.key, func(c jwt.MapClaims) { delete(c, "exp") }),
		"wrong issuer":      kc.sign(t, kc.key, func(c jwt.MapClaims) { c["iss"] = "https://evil.example.com/realms/test" }),
		"wrong audience":    kc.sign(t, kc.key, func(c jwt.MapClaims) { c["aud"] = "another-api" }),
		"signed by another": kc.sign(t, otherKey, nil),
		"id token":          kc.sign(t, kc.key, func(c jwt.MapClaims) { c["typ"] = "ID" }),
		"refresh token":     kc.sign(t, kc.key, func(c jwt.MapClaims) { c["typ"] = "Refresh" }),
		"no subject":        kc.sign(t, kc.key, func(c jwt.MapClaims) { delete(c, "sub") }),
		"hmac signed":       hmacToken,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := a.Authenticate(t.Context(), token)
			if !domain.IsKind(err, domain.ErrKindUnauthenticated) {
				t.Fatalf("err = %v, want unauthenticated", err)
			}
			if msg := domain.PublicMessage(err); msg != "invalid or expired token" {
				t.Errorf("public message = %q, must not reveal why the token was rejected", msg)
			}
		})
	}
}

func TestAuthenticateWithoutAudienceCheck(t *testing.T) {
	kc := newFakeKeycloak(t)
	a := newAuthenticator(t, Options{Issuer: kc.issuer()})

	token := kc.sign(t, kc.key, func(c jwt.MapClaims) { c["aud"] = "anything" })
	if _, err := a.Authenticate(t.Context(), token); err != nil {
		t.Errorf("Authenticate: %v, want success when no audience is configured", err)
	}
}

func TestStartsWhileKeycloakIsDown(t *testing.T) {
	a := newAuthenticator(t, Options{Issuer: "http://127.0.0.1:1/realms/test"})
	if _, err := a.Authenticate(t.Context(), "x.y.z"); !domain.IsKind(err, domain.ErrKindUnauthenticated) {
		t.Errorf("err = %v, want unauthenticated", err)
	}
}
