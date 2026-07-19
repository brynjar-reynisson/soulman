// Package auth verifies Supabase-issued JWTs the same way agent-suite's
// UserResolverFilter does (pinned to HS256 shared-secret or ES256
// JWKS-fetched-and-cached, requiring iss=<supabaseURL>/auth/v1 and
// aud="authenticated"), but skips agent-suite's DB-backed user resolution
// entirely: Soulman has exactly one real user, so the email claim itself
// *is* the authorization decision.
package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Result int

const (
	Unauthorized Result = iota
	Forbidden
	OK
)

// kidFromToken extracts the "kid" header claim if present, "" otherwise.
func kidFromToken(t *jwt.Token) string {
	kid, _ := t.Header["kid"].(string)
	return kid
}

type Verifier struct {
	supabaseURL string
	jwtSecret   []byte
	ownerEmail  string
	httpClient  *http.Client

	mu         sync.Mutex
	cachedKeys *jwksCache
}

func NewVerifier(supabaseURL, jwtSecret, ownerEmail string) *Verifier {
	return &Verifier{
		supabaseURL: strings.TrimRight(supabaseURL, "/"),
		jwtSecret:   []byte(jwtSecret),
		ownerEmail:  ownerEmail,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
	}
}

type supabaseClaims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
}

// Verify checks the request's Bearer token and returns the authorization
// outcome plus the token's email claim (empty if the token itself couldn't
// be verified). Result is Unauthorized for any missing/invalid/expired/
// wrong-issuer/wrong-audience token, Forbidden for a valid token whose
// email doesn't match ownerEmail, OK otherwise.
func (v *Verifier) Verify(r *http.Request) (Result, string) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return Unauthorized, ""
	}
	tokenString := strings.TrimPrefix(header, "Bearer ")

	expectedIssuer := v.supabaseURL + "/auth/v1"
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256", "ES256"}),
		jwt.WithIssuer(expectedIssuer),
		jwt.WithAudience("authenticated"),
	)

	token, err := parser.ParseWithClaims(tokenString, &supabaseClaims{}, func(t *jwt.Token) (interface{}, error) {
		switch t.Method.Alg() {
		case "HS256":
			if len(v.jwtSecret) == 0 {
				return nil, fmt.Errorf("HS256 rejected: no shared secret configured")
			}
			return v.jwtSecret, nil
		case "ES256":
			return v.getOrFetchPublicKey(kidFromToken(t))
		default:
			return nil, fmt.Errorf("unsupported algorithm: %s", t.Method.Alg())
		}
	})
	if err != nil || !token.Valid {
		return Unauthorized, ""
	}

	claims, ok := token.Claims.(*supabaseClaims)
	if !ok {
		return Unauthorized, ""
	}

	if claims.Email != v.ownerEmail {
		return Forbidden, claims.Email
	}
	return OK, claims.Email
}

// Middleware gates next behind Verify: 401 on Unauthorized, 403 on
// Forbidden, otherwise calls next.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := v.Verify(r)
		switch result {
		case OK:
			next.ServeHTTP(w, r)
		case Forbidden:
			writeJSONError(w, http.StatusForbidden, "forbidden")
		default:
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		}
	})
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

type jwksCache struct {
	byKid map[string]*ecdsa.PublicKey
	first *ecdsa.PublicKey // deterministic fallback for a token with no/unmatched kid
}

func (v *Verifier) getOrFetchPublicKey(kid string) (*ecdsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cachedKeys == nil {
		cache, err := v.fetchPublicKeysFromJWKS()
		if err != nil {
			return nil, err
		}
		v.cachedKeys = cache
	}
	if kid != "" {
		if key, ok := v.cachedKeys.byKid[kid]; ok {
			return key, nil
		}
	}
	if v.cachedKeys.first != nil {
		return v.cachedKeys.first, nil
	}
	return nil, fmt.Errorf("no EC key found in JWKS")
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (v *Verifier) fetchPublicKeysFromJWKS() (*jwksCache, error) {
	url := v.supabaseURL + "/auth/v1/.well-known/jwks.json"
	resp, err := v.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching JWKS from %s: %w", url, err)
	}
	defer resp.Body.Close()

	var set jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("parsing JWKS from %s: %w", url, err)
	}

	cache := &jwksCache{byKid: make(map[string]*ecdsa.PublicKey)}
	for _, k := range set.Keys {
		if k.Kty != "EC" {
			continue
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, fmt.Errorf("decoding JWKS x coordinate: %w", err)
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, fmt.Errorf("decoding JWKS y coordinate: %w", err)
		}
		key := &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(xBytes),
			Y:     new(big.Int).SetBytes(yBytes),
		}
		if cache.first == nil {
			cache.first = key
		}
		if k.Kid != "" {
			cache.byKid[k.Kid] = key
		}
	}
	if cache.first == nil {
		return nil, fmt.Errorf("no EC key found in JWKS at %s", url)
	}
	return cache, nil
}
