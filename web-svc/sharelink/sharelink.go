// Package sharelink issues and verifies stateless, time-limited tokens for
// the file-sharing feature. A token is self-contained —
// base64url(JSON payload) + "." + base64url(HMAC-SHA256 signature) — so
// verification never touches storage or the filesystem, only the secret
// it was signed with. See
// docs/superpowers/specs/2026-08-19-file-sharing-design.md.
package sharelink

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrExpired = errors.New("sharelink: token expired")
	ErrInvalid = errors.New("sharelink: invalid token")
)

// payload is the JSON structure embedded in every token. Field names are
// short since they travel in every URL.
type payload struct {
	Root string `json:"root"`
	Path string `json:"path"`
	File string `json:"file"`
	Exp  int64  `json:"exp"` // unix seconds
}

// Issue creates a token for one file, valid for ttl from now.
func Issue(secret []byte, root, path, file string, ttl time.Duration) (token string, expiresAt time.Time) {
	expiresAt = time.Now().Add(ttl)
	body, _ := json.Marshal(payload{Root: root, Path: path, File: file, Exp: expiresAt.Unix()})
	payloadB64 := base64.RawURLEncoding.EncodeToString(body)
	return payloadB64 + "." + sign(secret, payloadB64), expiresAt
}

// Verify checks a token's signature (constant-time comparison, so an
// attacker can't use timing to guess it byte-by-byte) and expiry, and
// returns the embedded root/path/file. The signature is checked before the
// payload is ever decoded — an attacker cannot get JSON-parsed until they
// hold a validly-signed token.
func Verify(secret []byte, token string) (root, path, file string, err error) {
	dot := strings.IndexByte(token, '.')
	if dot < 0 {
		return "", "", "", ErrInvalid
	}
	payloadB64, sigB64 := token[:dot], token[dot+1:]
	if !hmac.Equal([]byte(sign(secret, payloadB64)), []byte(sigB64)) {
		return "", "", "", ErrInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", "", "", ErrInvalid
	}
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return "", "", "", ErrInvalid
	}
	if time.Now().Unix() > p.Exp {
		return "", "", "", ErrExpired
	}
	return p.Root, p.Path, p.File, nil
}

func sign(secret []byte, payloadB64 string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payloadB64))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
