package auth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"soulman/web-svc/auth"
)

const (
	testSupabaseURL = "https://example.supabase.co"
	testSecret      = "test-jwt-secret"
	testOwnerEmail  = "breynisson@gmail.com"
)

func hsToken(t *testing.T, secret, issuer, audience, email string, exp time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":   issuer,
		"aud":   audience,
		"email": email,
		"sub":   "test-user",
		"exp":   exp.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("signing HS256 token: %v", err)
	}
	return signed
}

func requestWithToken(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestVerify_ValidOwnerToken_ReturnsOK(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(time.Hour))

	result, email := v.Verify(requestWithToken(token))

	if result != auth.OK {
		t.Fatalf("result = %v, want OK", result)
	}
	if email != testOwnerEmail {
		t.Errorf("email = %q, want %q", email, testOwnerEmail)
	}
}

func TestVerify_ValidNonOwnerToken_ReturnsForbidden(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", "someone-else@example.com", time.Now().Add(time.Hour))

	result, email := v.Verify(requestWithToken(token))

	if result != auth.Forbidden {
		t.Fatalf("result = %v, want Forbidden", result)
	}
	if email != "someone-else@example.com" {
		t.Errorf("email = %q, want someone-else@example.com", email)
	}
}

func TestVerify_NoAuthorizationHeader_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)

	result, _ := v.Verify(httptest.NewRequest(http.MethodGet, "/api/status", nil))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized", result)
	}
}

func TestVerify_ExpiredToken_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(-time.Hour))

	result, _ := v.Verify(requestWithToken(token))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized", result)
	}
}

func TestVerify_WrongIssuer_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, "https://not-the-right-issuer.example/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(time.Hour))

	result, _ := v.Verify(requestWithToken(token))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized", result)
	}
}

func TestVerify_WrongAudience_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "anon", testOwnerEmail, time.Now().Add(time.Hour))

	result, _ := v.Verify(requestWithToken(token))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized", result)
	}
}

func TestVerify_WrongSigningSecret_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, "wrong-secret", testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(time.Hour))

	result, _ := v.Verify(requestWithToken(token))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized", result)
	}
}

func TestVerify_UnsupportedAlgorithm_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	claims := jwt.MapClaims{
		"iss":   testSupabaseURL + "/auth/v1",
		"aud":   "authenticated",
		"email": testOwnerEmail,
		"sub":   "test-user",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(rsaKey)
	if err != nil {
		t.Fatalf("signing RS256 token: %v", err)
	}

	result, _ := v.Verify(requestWithToken(signed))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized for a disallowed algorithm", result)
	}
}

func TestVerify_NoneAlgorithm_ReturnsUnauthorized(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)

	claims := jwt.MapClaims{
		"iss":   testSupabaseURL + "/auth/v1",
		"aud":   "authenticated",
		"email": testOwnerEmail,
		"sub":   "test-user",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("signing none-alg token: %v", err)
	}

	result, _ := v.Verify(requestWithToken(signed))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized for alg=none", result)
	}
}

func encodeCoord(b *big.Int) string {
	buf := make([]byte, 32)
	b.FillBytes(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func TestVerify_ValidES256TokenViaJWKS_ReturnsOK(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating EC key: %v", err)
	}

	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]string{
				{
					"kty": "EC",
					"crv": "P-256",
					"x":   encodeCoord(privateKey.X),
					"y":   encodeCoord(privateKey.Y),
				},
			},
		})
	}))
	defer jwks.Close()

	v := auth.NewVerifier(jwks.URL, testSecret, testOwnerEmail)

	claims := jwt.MapClaims{
		"iss":   jwks.URL + "/auth/v1",
		"aud":   "authenticated",
		"email": testOwnerEmail,
		"sub":   "test-user",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("signing ES256 token: %v", err)
	}

	result, email := v.Verify(requestWithToken(signed))

	if result != auth.OK {
		t.Fatalf("result = %v, want OK", result)
	}
	if email != testOwnerEmail {
		t.Errorf("email = %q, want %q", email, testOwnerEmail)
	}
}

func TestVerify_ES256WithMatchingKid_SelectsCorrectKey(t *testing.T) {
	oldKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating old EC key: %v", err)
	}
	newKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating new EC key: %v", err)
	}

	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]string{
				{"kty": "EC", "crv": "P-256", "kid": "old-key", "x": encodeCoord(oldKey.X), "y": encodeCoord(oldKey.Y)},
				{"kty": "EC", "crv": "P-256", "kid": "new-key", "x": encodeCoord(newKey.X), "y": encodeCoord(newKey.Y)},
			},
		})
	}))
	defer jwks.Close()

	v := auth.NewVerifier(jwks.URL, testSecret, testOwnerEmail)

	claims := jwt.MapClaims{
		"iss":   jwks.URL + "/auth/v1",
		"aud":   "authenticated",
		"email": testOwnerEmail,
		"sub":   "test-user",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = "new-key"
	signed, err := token.SignedString(newKey)
	if err != nil {
		t.Fatalf("signing ES256 token: %v", err)
	}

	result, email := v.Verify(requestWithToken(signed))

	if result != auth.OK {
		t.Fatalf("result = %v, want OK (correct key selected by kid)", result)
	}
	if email != testOwnerEmail {
		t.Errorf("email = %q", email)
	}
}

func TestVerify_HS256WithNoConfiguredSecret_RejectsEvenAnEmptyKeySignedToken(t *testing.T) {
	// Regression test: NewVerifier constructed with an empty secret (the
	// real deployment state when SUPABASE_JWT_SECRET is unset, since this
	// Supabase project uses ES256/JWKS) must never accept an HS256 token,
	// even one an attacker forges by signing with an empty-byte HMAC key
	// (which is a well-defined, computable operation, not an error).
	v := auth.NewVerifier(testSupabaseURL, "", testOwnerEmail)
	forgedToken := hsToken(t, "", testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(time.Hour))

	result, _ := v.Verify(requestWithToken(forgedToken))

	if result != auth.Unauthorized {
		t.Fatalf("result = %v, want Unauthorized — an HS256 token must never be accepted when no secret is configured, regardless of what key it was signed with", result)
	}
}

func TestMiddleware_OK_CallsNext(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", testOwnerEmail, time.Now().Add(time.Hour))

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec := httptest.NewRecorder()
	v.Middleware(next).ServeHTTP(rec, requestWithToken(token))

	if !called {
		t.Fatal("next handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (default recorder code before handler writes)", rec.Code)
	}
}

func TestMiddleware_Forbidden_Returns403AndDoesNotCallNext(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	token := hsToken(t, testSecret, testSupabaseURL+"/auth/v1", "authenticated", "someone-else@example.com", time.Now().Add(time.Hour))

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec := httptest.NewRecorder()
	v.Middleware(next).ServeHTTP(rec, requestWithToken(token))

	if called {
		t.Fatal("next handler should not have been called")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestMiddleware_Unauthorized_Returns401AndDoesNotCallNext(t *testing.T) {
	v := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec := httptest.NewRecorder()
	v.Middleware(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	if called {
		t.Fatal("next handler should not have been called")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
