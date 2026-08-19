# File Sharing (Time-Limited Links) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Share" action next to each file's "Download" button in the dashboard's file browser that generates a time-limited, unauthenticated link to that one file, copies it to the clipboard, and — when opened, even with no dashboard login — triggers the browser's save/download UI directly. Also: Download and Share become icon buttons instead of text.

**Architecture:** A new stateless-token package (`web-svc/sharelink`) signs `(root, path, file, expiry)` tuples with an in-memory HMAC secret generated at process start. A new authenticated endpoint (`POST /api/files/share`) issues a token; a new unauthenticated endpoint (`GET /dl/{token}`) verifies it and streams the file through the same download logic `/api/files/download` already uses. The frontend gets two new SVG icon components and a Share button wired to the new endpoint plus a clipboard write.

**Tech Stack:** Go 1.25 (`web-svc`, `crypto/hmac`, `crypto/rand`, chi router), React 19 + TypeScript + Vite + Vitest (`web`).

**Spec:** `docs/superpowers/specs/2026-08-19-file-sharing-design.md`

## Global Constraints

- Token format: `base64url(JSON payload)` + `"."` + `base64url(HMAC-SHA256 signature over the payload's base64url text)`. Payload fields: `root`, `path`, `file` (strings), `exp` (int64 unix seconds).
- Default TTL is 60 minutes, configurable via `web.share_link_ttl_minutes` in shared config; zero/absent defaults to 60 (not a fatal validation error).
- Signing secret is 32 random bytes from `crypto/rand`, generated once at `web-svc` process startup, held only in memory — never written to `.env` or disk. A restart invalidates all outstanding links (accepted tradeoff, documented in the spec's Non-Goals).
- `POST /api/files/share` is inside the existing owner-JWT route group. `GET /dl/{token}` is unauthenticated — registered directly on the router, outside that group.
- `GET /dl/{token}` responses on failure are `text/html` (not JSON) — a human may land there directly. Expired → `410 Gone`. Anything else invalid (bad signature, unknown root, missing file) → `404 Not Found`, with the same generic message in both cases (no distinction between "root gone" vs "file gone" vs "tampered token").
- Successful downloads (both routes) get identical treatment: `Content-Disposition: attachment; filename="..."`, `Cache-Control: no-store`, and the existing UTF-8-BOM-for-non-ASCII-text logic — via one shared `serveFileDownload` helper, not duplicated logic.
- No revocation before expiry, no single-use enforcement, no rate limiting on `/dl/{token}`, one file per link (all explicit spec Non-Goals — do not add them).
- Frontend: no new npm dependency for icons — small inline SVG components. Existing Download button becomes icon-only with `aria-label="Download"`; new Share button is icon-only with `aria-label="Share"`.
- `web/vite.config.ts` needs a `/dl` proxy entry (pointing at the same backend port as the existing `/api` entry) added to both `server.proxy` (dev, port 9015) and `preview.proxy` (prod, port 9005) — without it, a `/dl/<token>` link opened through the Vite dev server or the `cloudflared`-tunneled preview server never reaches `web-svc`.

---

### Task 1: `web-svc/sharelink` package

**Files:**
- Create: `web-svc/sharelink/sharelink.go`
- Test: `web-svc/sharelink/sharelink_test.go`

**Interfaces:**
- Produces: `sharelink.Issue(secret []byte, root, path, file string, ttl time.Duration) (token string, expiresAt time.Time)`, `sharelink.Verify(secret []byte, token string) (root, path, file string, err error)`, `sharelink.ErrExpired`, `sharelink.ErrInvalid` — consumed by Tasks 4 and 5.

- [ ] **Step 1: Write the failing tests**

Create `web-svc/sharelink/sharelink_test.go`:

```go
// web-svc/sharelink/sharelink_test.go
package sharelink_test

import (
	"testing"
	"time"

	"soulman/web-svc/sharelink"
)

var testSecret = []byte("test-secret-32-bytes-long-abcdef")

func TestIssueVerify_RoundTripsRootPathFile(t *testing.T) {
	token, expiresAt := sharelink.Issue(testSecret, "Documents", "Taxes/2025", "2025-return.pdf", time.Hour)

	root, path, file, err := sharelink.Verify(testSecret, token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if root != "Documents" || path != "Taxes/2025" || file != "2025-return.pdf" {
		t.Errorf("Verify() = (%q, %q, %q), want (Documents, Taxes/2025, 2025-return.pdf)", root, path, file)
	}
	if expiresAt.Before(time.Now()) {
		t.Errorf("expiresAt = %v, want a future time", expiresAt)
	}
}

func TestIssueVerify_RoundTripsNonASCIIFilename(t *testing.T) {
	token, _ := sharelink.Issue(testSecret, "Documents", "", "Alexander-tékklisti.txt", time.Hour)

	_, _, file, err := sharelink.Verify(testSecret, token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if file != "Alexander-tékklisti.txt" {
		t.Errorf("file = %q, want Alexander-tékklisti.txt", file)
	}
}

func TestVerify_ExpiredToken_ReturnsErrExpired(t *testing.T) {
	token, _ := sharelink.Issue(testSecret, "Documents", "", "note.txt", -time.Minute)

	_, _, _, err := sharelink.Verify(testSecret, token)
	if err != sharelink.ErrExpired {
		t.Fatalf("Verify() error = %v, want ErrExpired", err)
	}
}

func TestVerify_TamperedSignature_ReturnsErrInvalid(t *testing.T) {
	token, _ := sharelink.Issue(testSecret, "Documents", "", "note.txt", time.Hour)
	tampered := token[:len(token)-1] + "x"

	_, _, _, err := sharelink.Verify(testSecret, tampered)
	if err != sharelink.ErrInvalid {
		t.Fatalf("Verify() error = %v, want ErrInvalid", err)
	}
}

func TestVerify_TamperedPayload_ReturnsErrInvalid(t *testing.T) {
	token, _ := sharelink.Issue(testSecret, "Documents", "", "note.txt", time.Hour)
	// Flip a character inside the payload segment (before the "."), leaving
	// the original signature untouched — the signature no longer matches.
	dot := 0
	for i, c := range token {
		if c == '.' {
			dot = i
			break
		}
	}
	tampered := "x" + token[1:dot] + token[dot:]

	_, _, _, err := sharelink.Verify(testSecret, tampered)
	if err != sharelink.ErrInvalid {
		t.Fatalf("Verify() error = %v, want ErrInvalid", err)
	}
}

func TestVerify_WrongSecret_ReturnsErrInvalid(t *testing.T) {
	token, _ := sharelink.Issue(testSecret, "Documents", "", "note.txt", time.Hour)
	otherSecret := []byte("a-completely-different-secret!!")

	_, _, _, err := sharelink.Verify(otherSecret, token)
	if err != sharelink.ErrInvalid {
		t.Fatalf("Verify() error = %v, want ErrInvalid", err)
	}
}

func TestVerify_MalformedToken_ReturnsErrInvalid(t *testing.T) {
	_, _, _, err := sharelink.Verify(testSecret, "not-a-valid-token")
	if err != sharelink.ErrInvalid {
		t.Fatalf("Verify() error = %v, want ErrInvalid", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./web-svc/sharelink/... -v`
Expected: FAIL — `package sharelink: no such file or directory` (or "undefined: sharelink.Issue" once the package exists but is empty).

- [ ] **Step 3: Write the implementation**

Create `web-svc/sharelink/sharelink.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./web-svc/sharelink/... -v`
Expected: PASS (all 7 tests).

- [ ] **Step 5: Commit**

```bash
git add web-svc/sharelink/sharelink.go web-svc/sharelink/sharelink_test.go
git commit -m "feat(web-svc): add sharelink package for stateless share-link tokens"
```

---

### Task 2: Share-link TTL config

**Files:**
- Modify: `common/sharedconfig/config.go`
- Modify: `web-svc/config/config.go`
- Modify: `web-svc/config/config_test.go`
- Modify: `config/dev.json`
- Modify: `config/prod.json`

**Interfaces:**
- Consumes: nothing new.
- Produces: `sharedconfig.WebConfig.ShareLinkTTLMinutes int`, `config.Config.ShareLinkTTL time.Duration` (already defaulted to 60 minutes when unset) — consumed by Task 3 (`web-svc/main.go`).

- [ ] **Step 1: Write the failing tests**

In `web-svc/config/config_test.go`, add `"time"` to the import block (it currently imports `"os"`, `"path/filepath"`, `"testing"`, `"soulman/web-svc/config"`), then append:

```go
func TestLoad_ShareLinkTTLDefaultsTo60Minutes(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, validConfigJSON)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ShareLinkTTL != 60*time.Minute {
		t.Errorf("ShareLinkTTL = %v, want 60m", cfg.ShareLinkTTL)
	}
}

func TestLoad_ShareLinkTTLMinutesOverride(t *testing.T) {
	dir := t.TempDir()
	withTTL := `{"web": {"owner_email": "breynisson@gmail.com", "cors_allowed_origin": "http://localhost:5178", "perception_svc_url": "http://localhost:9011", "memory_svc_url": "http://localhost:9012", "thinking_svc_url": "http://localhost:9013", "action_svc_url": "http://localhost:9014", "obsidian_root": "C:\\Users\\Lenovo\\Documents\\obsidian", "claude_project_roots": [{"label": "Obsidian", "path": "C:\\Users\\Lenovo\\Documents\\obsidian"}], "file_browser_roots": [{"label": "Documents", "path": "C:\\Users\\Lenovo\\Documents"}], "share_link_ttl_minutes": 15}}`
	path := writeConfigFile(t, dir, withTTL)
	os.Setenv("CONFIG_PATH", path)
	os.Setenv("SUPABASE_URL", "https://example.supabase.co")
	os.Setenv("SUPABASE_JWT_SECRET", "shh")
	defer os.Unsetenv("CONFIG_PATH")
	defer os.Unsetenv("SUPABASE_URL")
	defer os.Unsetenv("SUPABASE_JWT_SECRET")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ShareLinkTTL != 15*time.Minute {
		t.Errorf("ShareLinkTTL = %v, want 15m", cfg.ShareLinkTTL)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./web-svc/config/... -v -run TestLoad_ShareLinkTTL`
Expected: FAIL — `cfg.ShareLinkTTL undefined (type *config.Config has no field or method ShareLinkTTL)`.

- [ ] **Step 3: Write the implementation**

In `common/sharedconfig/config.go`, add a field to `WebConfig` (after `FileBrowserRoots`):

```go
	// ShareLinkTTLMinutes is how long a generated share link
	// (web-svc/sharelink) stays valid. Optional — zero or absent defaults
	// to 60 in web-svc/config.Load, the same loose-default posture as
	// DoNotDisturb's Start/End (not a fatal validation error). See
	// docs/superpowers/specs/2026-08-19-file-sharing-design.md.
	ShareLinkTTLMinutes int `json:"share_link_ttl_minutes"`
```

In `web-svc/config/config.go`, add `"time"` to the import block, add `ShareLinkTTL time.Duration` to the `Config` struct (after `FileBrowserRoots`), and in `Load()`, before the `return &Config{...}` statement:

```go
	shareLinkTTL := time.Duration(shared.Web.ShareLinkTTLMinutes) * time.Minute
	if shareLinkTTL <= 0 {
		shareLinkTTL = 60 * time.Minute
	}
```

then add `ShareLinkTTL: shareLinkTTL,` to the returned `&Config{...}` literal (after `FileBrowserRoots: shared.Web.FileBrowserRoots,`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./web-svc/config/... -v`
Expected: PASS (all tests in the package, including the two new ones).

- [ ] **Step 5: Update `config/dev.json` and `config/prod.json`**

In both files, add `"share_link_ttl_minutes": 60` to the `"web"` object, after the `"file_browser_roots"` array closes:

```json
    "file_browser_roots": [
      { "label": "Documents", "path": "C:\\Users\\Lenovo\\Documents" },
      { "label": "Downloads", "path": "C:\\Users\\Lenovo\\Downloads" }
    ],
    "share_link_ttl_minutes": 60
```

- [ ] **Step 6: Commit**

```bash
git add common/sharedconfig/config.go web-svc/config/config.go web-svc/config/config_test.go config/dev.json config/prod.json
git commit -m "feat(web-svc): add configurable share-link TTL (default 60 minutes)"
```

---

### Task 3: `httpserver.Config` fields, secret generation, and download-serving refactor

**Files:**
- Modify: `web-svc/httpserver/server.go`
- Modify: `web-svc/httpserver/files_handler.go`
- Modify: `web-svc/main.go`

**Interfaces:**
- Consumes: `config.Config.ShareLinkTTL` (Task 2).
- Produces: `httpserver.Config.ShareLinkSecret []byte`, `httpserver.Config.ShareLinkTTL time.Duration` — consumed by Tasks 4 and 5. `serveFileDownload(w http.ResponseWriter, r *http.Request, absPath, filename string)` (unexported, package `httpserver`) — consumed by Task 5.

This task is a refactor plus plumbing — it changes no externally observable behavior of the existing `/api/files/download` endpoint. There is no new test to write; instead, the existing `TestAPIFilesDownload_*` suite in `files_handler_test.go` is the correctness check, run both before and after.

- [ ] **Step 1: Confirm the baseline is green**

Run: `go test ./web-svc/httpserver/... -run TestAPIFilesDownload -v`
Expected: PASS (all `TestAPIFilesDownload_*` tests, unchanged from before this task).

- [ ] **Step 2: Extract `serveFileDownload` in `files_handler.go`**

Replace the current `filesDownload` function body with a thin wrapper, and move its download-serving logic into a new shared function. Before:

```go
func (s *Server) filesDownload(w http.ResponseWriter, r *http.Request) {
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, r.URL.Query().Get("root"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	filename := r.URL.Query().Get("file")
	absPath, err := filebrowser.ResolveFile(root, r.URL.Query().Get("path"), filename)
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// This must never be served from a browser or edge (Cloudflare tunnel)
	// cache: a file's content can change between browses, and — the
	// concrete incident that motivated this — http.ServeFile's
	// Last-Modified header alone was enough for a browser to silently
	// replay a stale cached response after this handler's encoding
	// behavior changed server-side, making the fix look like it hadn't
	// deployed at all even though the origin was already correct.
	w.Header().Set("Cache-Control", "no-store")

	needsBOM, err := isBOMlessUTF8Text(absPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if needsBOM {
		serveWithUTF8BOM(w, absPath)
		return
	}
	http.ServeFile(w, r, absPath)
}
```

After:

```go
func (s *Server) filesDownload(w http.ResponseWriter, r *http.Request) {
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, r.URL.Query().Get("root"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	filename := r.URL.Query().Get("file")
	absPath, err := filebrowser.ResolveFile(root, r.URL.Query().Get("path"), filename)
	if err != nil {
		writeFileBrowserError(w, err)
		return
	}
	serveFileDownload(w, r, absPath, filename)
}

// serveFileDownload streams absPath as an attachment named filename,
// applying the same Content-Disposition/Cache-Control/UTF-8-BOM treatment
// regardless of which route reached it — the owner-JWT-gated
// /api/files/download, or the token-gated /dl/{token} share link.
func serveFileDownload(w http.ResponseWriter, r *http.Request, absPath, filename string) {
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// This must never be served from a browser or edge (Cloudflare tunnel)
	// cache: a file's content can change between browses, and — the
	// concrete incident that motivated this — http.ServeFile's
	// Last-Modified header alone was enough for a browser to silently
	// replay a stale cached response after this handler's encoding
	// behavior changed server-side, making the fix look like it hadn't
	// deployed at all even though the origin was already correct.
	w.Header().Set("Cache-Control", "no-store")

	needsBOM, err := isBOMlessUTF8Text(absPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if needsBOM {
		serveWithUTF8BOM(w, absPath)
		return
	}
	http.ServeFile(w, r, absPath)
}
```

- [ ] **Step 3: Run tests to confirm the refactor didn't change behavior**

Run: `go test ./web-svc/httpserver/... -run TestAPIFilesDownload -v`
Expected: PASS (identical to Step 1 — same tests, same results).

- [ ] **Step 4: Add the new `Config` fields**

In `web-svc/httpserver/server.go`, add two fields to `Config` (after `FileBrowserRoots`):

```go
	ShareLinkSecret    []byte
	ShareLinkTTL       time.Duration
```

(`"time"` is already imported in this file.)

- [ ] **Step 5: Generate the secret and wire it through in `main.go`**

Add `"crypto/rand"` to `web-svc/main.go`'s import block. Before the `srv := httpserver.New(...)` call, add:

```go
	shareLinkSecret := make([]byte, 32)
	if _, err := rand.Read(shareLinkSecret); err != nil {
		slog.Error("generating share link secret failed", "error", err)
		os.Exit(1)
	}
```

Add two fields to the `httpserver.Config{...}` literal passed to `httpserver.New` (after `FileBrowserRoots: fileBrowserRoots,`):

```go
		ShareLinkSecret:    shareLinkSecret,
		ShareLinkTTL:       cfg.ShareLinkTTL,
```

- [ ] **Step 6: Build to confirm everything compiles**

Run: `go build ./...` (from the `web-svc` module root, or `go build ./web-svc/...` from the repo root)
Expected: builds with no errors.

- [ ] **Step 7: Commit**

```bash
git add web-svc/httpserver/server.go web-svc/httpserver/files_handler.go web-svc/main.go
git commit -m "refactor(web-svc): extract serveFileDownload and wire in a share-link secret"
```

---

### Task 4: `POST /api/files/share` handler

**Files:**
- Modify: `web-svc/httpserver/files_handler.go`
- Modify: `web-svc/httpserver/server.go`
- Modify: `web-svc/httpserver/files_handler_test.go`

**Interfaces:**
- Consumes: `sharelink.Issue` (Task 1), `s.cfg.ShareLinkSecret`/`s.cfg.ShareLinkTTL` (Task 3), `findFileBrowserRoot`/`writeFileBrowserError`/`writeJSON` (existing).
- Produces: `Server.filesShare` handler, response shape `{"url": "/dl/<token>", "expiresAt": "<RFC3339>"}` — consumed by Task 7 (frontend `shareFile`).

- [ ] **Step 1: Write the failing tests**

In `web-svc/httpserver/files_handler_test.go`, add `"strings"` and `"time"` to the import block, add `"soulman/web-svc/sharelink"`, then append:

```go
func TestAPIFilesShare_ReturnsURLAndExpiry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
		ShareLinkSecret:   []byte("test-secret-32-bytes-long-abcdef"),
		ShareLinkTTL:      time.Hour,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodPost, "/api/files/share?root=Documents&path=&file=note.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(body.URL, "/dl/") {
		t.Errorf("url = %q, want a /dl/ prefix", body.URL)
	}
	if body.ExpiresAt == "" {
		t.Errorf("expiresAt is empty")
	}
}

func TestAPIFilesShare_MissingFile_Returns404(t *testing.T) {
	dir := t.TempDir()
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
		ShareLinkSecret:   []byte("test-secret-32-bytes-long-abcdef"),
		ShareLinkTTL:      time.Hour,
	}
	verifier := auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail)
	srv := httpserver.New("9005", cfg, verifier)

	req := httptest.NewRequest(http.MethodPost, "/api/files/share?root=Documents&path=&file=missing.txt", nil)
	req.Header.Set("Authorization", "Bearer "+ownerToken(t))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIFilesShare_NoToken_Returns401(t *testing.T) {
	cfg := httpserver.Config{CORSAllowedOrigin: "http://localhost:5178"}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	req := httptest.NewRequest(http.MethodPost, "/api/files/share?root=Documents&path=&file=note.txt", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./web-svc/httpserver/... -run TestAPIFilesShare -v`
Expected: FAIL — `404 page not found` (route doesn't exist yet).

- [ ] **Step 3: Write the implementation**

In `web-svc/httpserver/files_handler.go`, add `"soulman/web-svc/sharelink"` to the import block, then add:

```go
func (s *Server) filesShare(w http.ResponseWriter, r *http.Request) {
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, r.URL.Query().Get("root"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown root")
		return
	}
	relPath := r.URL.Query().Get("path")
	filename := r.URL.Query().Get("file")
	if _, err := filebrowser.ResolveFile(root, relPath, filename); err != nil {
		writeFileBrowserError(w, err)
		return
	}
	token, expiresAt := sharelink.Issue(s.cfg.ShareLinkSecret, root.Label, relPath, filename, s.cfg.ShareLinkTTL)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":       "/dl/" + token,
		"expiresAt": expiresAt,
	})
}
```

In `web-svc/httpserver/server.go`, add a route inside the existing owner-JWT `r.Group`, after `r.Post("/api/files/upload", s.filesUpload)`:

```go
		r.Post("/api/files/share", s.filesShare)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./web-svc/httpserver/... -v`
Expected: PASS (all tests in the package, including the three new ones).

- [ ] **Step 5: Commit**

```bash
git add web-svc/httpserver/files_handler.go web-svc/httpserver/server.go web-svc/httpserver/files_handler_test.go
git commit -m "feat(web-svc): add POST /api/files/share to issue share-link tokens"
```

---

### Task 5: `GET /dl/{token}` unauthenticated download handler

**Files:**
- Modify: `web-svc/httpserver/files_handler.go`
- Modify: `web-svc/httpserver/server.go`
- Modify: `web-svc/httpserver/files_handler_test.go`

**Interfaces:**
- Consumes: `sharelink.Verify` (Task 1), `serveFileDownload` (Task 3), `findFileBrowserRoot`/`filebrowser.ResolveFile` (existing).
- Produces: `Server.shareDownload` handler on route `GET /dl/{token}`, registered outside the JWT group.

- [ ] **Step 1: Write the failing tests**

Append to `web-svc/httpserver/files_handler_test.go`:

```go
func TestShareDownload_ValidToken_ServesFileBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	secret := []byte("test-secret-32-bytes-long-abcdef")
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
		ShareLinkSecret:   secret,
		ShareLinkTTL:      time.Hour,
	}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	token, _ := sharelink.Issue(secret, "Documents", "", "note.txt", time.Hour)

	// Deliberately no Authorization header — reaching this handler with no
	// auth at all is the point of a share link.
	req := httptest.NewRequest(http.MethodGet, "/dl/"+token, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello world" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hello world")
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="note.txt"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestShareDownload_ExpiredToken_Returns410(t *testing.T) {
	dir := t.TempDir()
	secret := []byte("test-secret-32-bytes-long-abcdef")
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
		ShareLinkSecret:   secret,
	}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	token, _ := sharelink.Issue(secret, "Documents", "", "note.txt", -time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/dl/"+token, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410, body=%s", rec.Code, rec.Body.String())
	}
}

func TestShareDownload_TamperedToken_Returns404(t *testing.T) {
	dir := t.TempDir()
	secret := []byte("test-secret-32-bytes-long-abcdef")
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
		ShareLinkSecret:   secret,
	}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	token, _ := sharelink.Issue(secret, "Documents", "", "note.txt", time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/dl/"+token+"x", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

func TestShareDownload_FileDeletedSinceIssue_Returns404(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	secret := []byte("test-secret-32-bytes-long-abcdef")
	cfg := httpserver.Config{
		CORSAllowedOrigin: "http://localhost:5178",
		FileBrowserRoots:  []filebrowser.Root{{Label: "Documents", Path: dir}},
		ShareLinkSecret:   secret,
	}
	srv := httpserver.New("9005", cfg, auth.NewVerifier(testSupabaseURL, testSecret, testOwnerEmail))
	token, _ := sharelink.Issue(secret, "Documents", "", "note.txt", time.Hour)
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/dl/"+token, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./web-svc/httpserver/... -run TestShareDownload -v`
Expected: FAIL — `404 page not found` for all four (route doesn't exist yet).

- [ ] **Step 3: Write the implementation**

In `web-svc/httpserver/files_handler.go`, add `"errors"` (if not already imported — it is), `"fmt"`, `"html"`, and `"github.com/go-chi/chi/v5"` to the import block, then add:

```go
func (s *Server) shareDownload(w http.ResponseWriter, r *http.Request) {
	rootLabel, relPath, filename, err := sharelink.Verify(s.cfg.ShareLinkSecret, chi.URLParam(r, "token"))
	if errors.Is(err, sharelink.ErrExpired) {
		writeShareLinkError(w, http.StatusGone, "This link has expired.")
		return
	}
	if err != nil {
		writeShareLinkError(w, http.StatusNotFound, "This link is invalid or the file is no longer available.")
		return
	}
	root, ok := findFileBrowserRoot(s.cfg.FileBrowserRoots, rootLabel)
	if !ok {
		writeShareLinkError(w, http.StatusNotFound, "This link is invalid or the file is no longer available.")
		return
	}
	absPath, err := filebrowser.ResolveFile(root, relPath, filename)
	if err != nil {
		writeShareLinkError(w, http.StatusNotFound, "This link is invalid or the file is no longer available.")
		return
	}
	serveFileDownload(w, r, absPath, filename)
}

// writeShareLinkError writes a minimal HTML page rather than JSON — unlike
// every other error path in this file, a human may land here directly by
// opening a stale or tampered share link in a browser.
func writeShareLinkError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html><meta charset=\"utf-8\"><p>%s</p>", html.EscapeString(message))
}
```

`files_handler.go` already imports `"errors"` (used by `writeFileBrowserError`) — confirm it's present rather than adding a duplicate.

In `web-svc/httpserver/server.go`, add the route directly on the router (not inside `r.Group`), after the `r.Group(...)` block and before `return r`:

```go
	r.Get("/dl/{token}", s.shareDownload)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./web-svc/httpserver/... -v`
Expected: PASS (all tests in the package).

- [ ] **Step 5: Commit**

```bash
git add web-svc/httpserver/files_handler.go web-svc/httpserver/server.go web-svc/httpserver/files_handler_test.go
git commit -m "feat(web-svc): add GET /dl/{token} unauthenticated share-link download"
```

---

### Task 6: Vite proxy for `/dl`

**Files:**
- Modify: `web/vite.config.ts`

**Interfaces:**
- Consumes: nothing new.
- Produces: `/dl/*` requests reach `web-svc` in both `npm run dev` (port 9015) and `npm run preview` (port 9005) — required for Task 9's manual verification and for the feature to work at all through the tunnel.

There is no test harness for `vite.config.ts` in this repo; `npm run build` (which runs `tsc -b` first) is the correctness check — it fails fast on a syntax error.

- [ ] **Step 1: Add the proxy entries**

In `web/vite.config.ts`, add a `/dl` entry to both `server.proxy` and `preview.proxy`:

```ts
  server: {
    port: 5190,
    host: '0.0.0.0',
    proxy: {
      '/api': { target: 'http://localhost:9015', changeOrigin: true },
      '/dl': { target: 'http://localhost:9015', changeOrigin: true },
    },
  },
  preview: {
    port: 5191,
    host: '0.0.0.0',
    allowedHosts: ['soulman.breynisson.org'],
    proxy: {
      '/api': { target: 'http://localhost:9005', changeOrigin: true },
      '/dl': { target: 'http://localhost:9005', changeOrigin: true },
    },
  },
```

- [ ] **Step 2: Verify the config still builds**

Run (from `web/`): `npm run build`
Expected: builds successfully with no errors.

- [ ] **Step 3: Commit**

```bash
git add web/vite.config.ts
git commit -m "feat(web): proxy /dl to web-svc for share-link downloads"
```

---

### Task 7: Frontend `shareFile` API function

**Files:**
- Modify: `web/src/api.ts`
- Modify: `web/src/api.test.ts`

**Interfaces:**
- Consumes: nothing new.
- Produces: `api.ShareLinkResponse { url: string; expiresAt: string }`, `api.shareFile(token: string | null, root: string, path: string, file: string): Promise<ShareLinkResponse>` — consumed by Task 9 (`FileBrowser.tsx`).

- [ ] **Step 1: Write the failing tests**

In `web/src/api.test.ts`, add `shareFile` to the import list from `./api` (alongside `downloadFile`, `uploadFile`), then append:

```ts
describe('shareFile', () => {
  it('POSTs to /api/files/share with encoded query params and returns the parsed body', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ url: '/dl/abc123', expiresAt: '2026-08-19T16:00:00Z' }),
    });

    const result = await shareFile('tok-abc', 'Documents', 'Taxes', '2025-return.pdf');

    expect(result).toEqual({ url: '/dl/abc123', expiresAt: '2026-08-19T16:00:00Z' });
    const [url, options] = mockFetch.mock.calls[0];
    expect(url).toBe('/api/files/share?root=Documents&path=Taxes&file=2025-return.pdf');
    expect(options.method).toBe('POST');
    expect(options.headers).toEqual({ Authorization: 'Bearer tok-abc' });
  });

  it('throws ApiError with the response status on failure', async () => {
    const mockFetch = fetch as unknown as ReturnType<typeof vi.fn>;
    mockFetch.mockResolvedValue({ ok: false, status: 404 });

    await expect(shareFile('tok-abc', 'Documents', '', 'missing.pdf')).rejects.toMatchObject({ status: 404 });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `web/`): `npx vitest run src/api.test.ts`
Expected: FAIL — `shareFile is not a function` (or a TypeScript error that the import doesn't exist).

- [ ] **Step 3: Write the implementation**

In `web/src/api.ts`, after `uploadFile`, add:

```ts
export interface ShareLinkResponse {
  url: string;
  expiresAt: string;
}

export async function shareFile(
  token: string | null,
  root: string,
  path: string,
  file: string,
): Promise<ShareLinkResponse> {
  const response = await fetch(
    `/api/files/share?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}&file=${encodeURIComponent(file)}`,
    { method: 'POST', headers: token ? { Authorization: `Bearer ${token}` } : {} },
  );
  if (!response.ok) {
    throw new ApiError(response.status, `share failed (${response.status})`);
  }
  return response.json();
}
```

(Not `mutateJSON`: that helper always sends a JSON body and always returns `Promise<void>`, discarding the response — `filesShare` takes query params like the rest of the file-browser API and needs the parsed `{url, expiresAt}` body back, same reasoning that already put `downloadFile`/`uploadFile` outside `getJSON`/`mutateJSON`.)

- [ ] **Step 4: Run tests to verify they pass**

Run (from `web/`): `npx vitest run src/api.test.ts`
Expected: PASS (all tests in the file).

- [ ] **Step 5: Commit**

```bash
git add web/src/api.ts web/src/api.test.ts
git commit -m "feat(web): add shareFile API function"
```

---

### Task 8: Icon components

**Files:**
- Create: `web/src/components/icons.tsx`
- Create: `web/src/components/icons.test.tsx`

**Interfaces:**
- Produces: `DownloadIcon(props: React.SVGProps<SVGSVGElement>)`, `ShareIcon(props: React.SVGProps<SVGSVGElement>)` — consumed by Task 9.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/icons.test.tsx`:

```tsx
// web/src/components/icons.test.tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { DownloadIcon, ShareIcon } from './icons';

describe('icons', () => {
  it('DownloadIcon renders an svg element', () => {
    const { container } = render(<DownloadIcon />);
    expect(container.querySelector('svg')).toBeInTheDocument();
  });

  it('ShareIcon renders an svg element', () => {
    const { container } = render(<ShareIcon />);
    expect(container.querySelector('svg')).toBeInTheDocument();
  });

  it('forwards extra props (e.g. a test id) onto the svg element', () => {
    const { container } = render(<DownloadIcon data-testid="dl-icon" />);
    expect(container.querySelector('[data-testid="dl-icon"]')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run (from `web/`): `npx vitest run src/components/icons.test.tsx`
Expected: FAIL — `Failed to resolve import "./icons"`.

- [ ] **Step 3: Write the implementation**

Create `web/src/components/icons.tsx`:

```tsx
// web/src/components/icons.tsx
export function DownloadIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.5" {...props}>
      <path d="M10 3v9m0 0-3.5-3.5M10 12l3.5-3.5M4 15.5h12" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function ShareIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 20 20" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.5" {...props}>
      <circle cx="15" cy="4.5" r="2" />
      <circle cx="5" cy="10" r="2" />
      <circle cx="15" cy="15.5" r="2" />
      <path d="M6.7 9 13.3 5.5M6.7 11 13.3 14.5" strokeLinecap="round" />
    </svg>
  );
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run (from `web/`): `npx vitest run src/components/icons.test.tsx`
Expected: PASS (all 3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/components/icons.tsx web/src/components/icons.test.tsx
git commit -m "feat(web): add DownloadIcon and ShareIcon components"
```

---

### Task 9: Wire Share into `FileBrowser.tsx`, icon-ify Download

**Files:**
- Modify: `web/src/components/FileBrowser.tsx`
- Modify: `web/src/components/FileBrowser.test.tsx`

**Interfaces:**
- Consumes: `shareFile` (Task 7), `DownloadIcon`/`ShareIcon` (Task 8).

- [ ] **Step 1: Update the failing/changed tests**

`web/src/components/FileBrowser.test.tsx` currently selects the Download button by its visible text (`screen.findByText('Download')`), which breaks once it becomes icon-only. Replace the whole file with:

```tsx
// web/src/components/FileBrowser.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ApiError } from '../api';
import { setParams } from '../urlState';

vi.mock('../auth', () => ({ getAccessToken: vi.fn().mockResolvedValue('tok-abc') }));

const mockListFiles = vi.fn();
const mockDownloadFile = vi.fn();
const mockUploadFile = vi.fn();
const mockShareFile = vi.fn();
vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    listFiles: (...args: unknown[]) => mockListFiles(...args),
    downloadFile: (...args: unknown[]) => mockDownloadFile(...args),
    uploadFile: (...args: unknown[]) => mockUploadFile(...args),
    shareFile: (...args: unknown[]) => mockShareFile(...args),
  };
});

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/');
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
    configurable: true,
  });
});

describe('FileBrowser', () => {
  it('lists folders and files, and drills into a subfolder via breadcrumb', async () => {
    mockListFiles
      .mockResolvedValueOnce({ folders: ['Taxes'], files: [{ name: 'note.txt', size: 42 }] })
      .mockResolvedValueOnce({ folders: [], files: [{ name: '2025-return.pdf', size: 1024 }] });
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByText('Taxes'));

    expect(await screen.findByText('2025-return.pdf')).toBeInTheDocument();
    expect(mockListFiles).toHaveBeenLastCalledWith('tok-abc', 'Documents', 'Taxes');
    expect(screen.getByText('Documents')).toBeInTheDocument();
  });

  it('downloads a file when its Download button is clicked', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [{ name: 'note.txt', size: 42 }] });
    mockDownloadFile.mockResolvedValue(undefined);
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByRole('button', { name: 'Download' }));

    expect(mockDownloadFile).toHaveBeenCalledWith('tok-abc', 'Documents', '', 'note.txt');
  });

  it('creates a share link, copies it to the clipboard, and shows a success message', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [{ name: 'note.txt', size: 42 }] });
    mockShareFile.mockResolvedValue({ url: '/dl/abc123', expiresAt: '2026-08-19T16:00:00Z' });
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByRole('button', { name: 'Share' }));

    expect(mockShareFile).toHaveBeenCalledWith('tok-abc', 'Documents', '', 'note.txt');
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(`${window.location.origin}/dl/abc123`);
    expect(await screen.findByText('Link copied')).toBeInTheDocument();
  });

  it('shows an error and no success message when creating a share link fails', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [{ name: 'note.txt', size: 42 }] });
    mockShareFile.mockRejectedValue(new ApiError(500, 'share failed (500)'));
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByRole('button', { name: 'Share' }));

    expect(await screen.findByText('Failed to create share link')).toBeInTheDocument();
    expect(screen.queryByText('Link copied')).not.toBeInTheDocument();
  });

  it('shows a replace confirmation on a 409 and retries with overwrite=true', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [] });
    mockUploadFile.mockRejectedValueOnce(new ApiError(409, 'upload failed (409)'));
    mockUploadFile.mockResolvedValueOnce(undefined);
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    const file = new File(['hello'], 'note.txt', { type: 'text/plain' });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, file);
    await userEvent.click(await screen.findByText('Upload'));

    expect(await screen.findByText(/already exists/i)).toBeInTheDocument();

    await userEvent.click(screen.getByText('replace?'));

    expect(mockUploadFile).toHaveBeenLastCalledWith('tok-abc', 'Documents', '', file, true);
  });

  it('clicking a breadcrumb segment truncates the path to that depth', async () => {
    mockListFiles
      .mockResolvedValueOnce({ folders: ['Taxes'], files: [] })
      .mockResolvedValueOnce({ folders: ['2025'], files: [] })
      .mockResolvedValueOnce({ folders: [], files: [{ name: 'deep.txt', size: 1 }] })
      .mockResolvedValueOnce({ folders: ['2025'], files: [] });
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    await userEvent.click(await screen.findByText('Taxes'));
    await userEvent.click(await screen.findByText('2025'));
    await screen.findByText('deep.txt');
    expect(mockListFiles).toHaveBeenLastCalledWith('tok-abc', 'Documents', 'Taxes/2025');

    await userEvent.click(screen.getByText('Taxes'));

    expect(await screen.findByText('2025')).toBeInTheDocument();
    expect(mockListFiles).toHaveBeenLastCalledWith('tok-abc', 'Documents', 'Taxes');
  });

  it('shows a success message and resets the file input after a successful upload', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [] });
    mockUploadFile.mockResolvedValueOnce(undefined);
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    const file = new File(['hello'], 'note.txt', { type: 'text/plain' });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, file);
    await userEvent.click(await screen.findByText('Upload'));

    expect(await screen.findByText(/"note.txt" uploaded successfully/i)).toBeInTheDocument();
    expect(input.value).toBe('');
  });

  it('clears a stale success message once a new file is chosen', async () => {
    mockListFiles.mockResolvedValue({ folders: [], files: [] });
    mockUploadFile.mockResolvedValueOnce(undefined);
    const { FileBrowser } = await import('./FileBrowser');
    render(<FileBrowser root="Documents" />);

    const file = new File(['hello'], 'note.txt', { type: 'text/plain' });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, file);
    await userEvent.click(await screen.findByText('Upload'));
    await screen.findByText(/"note.txt" uploaded successfully/i);

    await userEvent.upload(input, new File(['x'], 'other.txt', { type: 'text/plain' }));

    expect(screen.queryByText(/uploaded successfully/i)).not.toBeInTheDocument();
  });

  it('resets currentPath when remounted with a different root (simulates a root switch via key change)', async () => {
    mockListFiles
      .mockResolvedValueOnce({ folders: ['Taxes'], files: [] })
      .mockResolvedValueOnce({ folders: [], files: [{ name: 'x.txt', size: 1 }] })
      .mockResolvedValueOnce({ folders: [], files: [{ name: 'y.txt', size: 2 }] });
    const { FileBrowser } = await import('./FileBrowser');
    const { rerender } = render(<FileBrowser key="Documents" root="Documents" />);
    await userEvent.click(await screen.findByText('Taxes'));
    await screen.findByText('x.txt');

    // Mirrors what FileRootList's root-switch handler actually does before
    // the remount: clear filePath in the URL so the fresh instance's lazy
    // currentPath initializer doesn't pick up the previous root's drilled-down
    // path. Without this, the URL (a jsdom global that outlives `rerender`)
    // would still carry the old filePath, defeating the point of the test.
    setParams({ fileRoot: 'Downloads', filePath: null });
    rerender(<FileBrowser key="Downloads" root="Downloads" />);

    expect(await screen.findByText('y.txt')).toBeInTheDocument();
    expect(mockListFiles).toHaveBeenLastCalledWith('tok-abc', 'Downloads', '');
  });
});
```

- [ ] **Step 2: Run tests to verify the new/changed ones fail**

Run (from `web/`): `npx vitest run src/components/FileBrowser.test.tsx --pool=forks --no-file-parallelism`
Expected: FAIL — the Download-button test fails (`getByRole` finds nothing with `name: 'Download'`, since it's still a text button without an `aria-label`), and the two new Share tests fail (`getByRole` finds nothing with `name: 'Share'`, since the button doesn't exist yet).

- [ ] **Step 3: Write the implementation**

Replace `web/src/components/FileBrowser.tsx` in full with:

```tsx
// web/src/components/FileBrowser.tsx
import { useEffect, useRef, useState } from 'react';
import { getAccessToken } from '../auth';
import { listFiles, downloadFile, uploadFile, shareFile, ApiError, type FileListing } from '../api';
import { getParam, setParams } from '../urlState';
import { DownloadIcon, ShareIcon } from './icons';

export function FileBrowser({ root }: { root: string }) {
  const [currentPath, setCurrentPath] = useState<string>(() => getParam('filePath') ?? '');
  const [listing, setListing] = useState<FileListing | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pendingFile, setPendingFile] = useState<File | null>(null);
  const [conflictFile, setConflictFile] = useState<string | null>(null);
  const [uploadSuccess, setUploadSuccess] = useState<string | null>(null);
  const [shareSuccessFile, setShareSuccessFile] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      const token = await getAccessToken();
      try {
        const data = await listFiles(token, root, currentPath);
        if (active) {
          setListing(data);
          setError(null);
        }
      } catch {
        if (active) setError('Unable to load folder');
      }
    })();
    return () => {
      active = false;
    };
  }, [root, currentPath, refreshKey]);

  function navigateTo(path: string) {
    setCurrentPath(path);
    setShareSuccessFile(null);
    setParams({ fileRoot: root, filePath: path || null });
  }

  const crumbs = currentPath === '' ? [] : currentPath.split('/');

  async function handleDownload(name: string) {
    setShareSuccessFile(null);
    const token = await getAccessToken();
    try {
      await downloadFile(token, root, currentPath, name);
    } catch {
      setError('Download failed — the file may have moved');
      setRefreshKey((k) => k + 1);
    }
  }

  async function handleShare(name: string) {
    setShareSuccessFile(null);
    const token = await getAccessToken();
    try {
      const { url } = await shareFile(token, root, currentPath, name);
      await navigator.clipboard.writeText(window.location.origin + url);
      setError(null);
      setShareSuccessFile(name);
    } catch {
      setError('Failed to create share link');
    }
  }

  async function handleUpload(file: File, overwrite: boolean) {
    const token = await getAccessToken();
    setUploadSuccess(null);
    try {
      await uploadFile(token, root, currentPath, file, overwrite);
      setConflictFile(null);
      setPendingFile(null);
      setError(null);
      setUploadSuccess(file.name);
      if (fileInputRef.current) fileInputRef.current.value = '';
      setRefreshKey((k) => k + 1);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setConflictFile(file.name);
        setError(null);
      } else if (err instanceof ApiError && err.status === 413) {
        setError('Upload exceeds the 200MB limit');
        setConflictFile(null);
      } else {
        setError('Upload failed');
        setConflictFile(null);
      }
    }
  }

  return (
    <div>
      <nav className="mb-2 text-sm text-gray-500">
        <button onClick={() => navigateTo('')} className="underline">
          {root}
        </button>
        {crumbs.map((seg, i) => (
          <span key={i}>
            {' / '}
            <button onClick={() => navigateTo(crumbs.slice(0, i + 1).join('/'))} className="underline">
              {seg}
            </button>
          </span>
        ))}
      </nav>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!listing && !error && <p className="text-sm text-gray-500">Loading...</p>}
      {listing && (
        <ul className="space-y-1">
          {listing.folders.map((folder) => (
            <li key={folder}>
              <button
                onClick={() => navigateTo(currentPath ? `${currentPath}/${folder}` : folder)}
                className="text-sm underline"
              >
                {folder}
              </button>
            </li>
          ))}
          {listing.files.map((file) => (
            <li key={file.name} className="flex items-center gap-2">
              <span className="text-sm">{file.name}</span>
              <span className="text-xs text-gray-400">{formatSize(file.size)}</span>
              <button
                onClick={() => handleDownload(file.name)}
                aria-label="Download"
                title="Download"
                className="text-gray-600 hover:text-gray-900"
              >
                <DownloadIcon />
              </button>
              <button
                onClick={() => handleShare(file.name)}
                aria-label="Share"
                title="Share"
                className="text-gray-600 hover:text-gray-900"
              >
                <ShareIcon />
              </button>
              {shareSuccessFile === file.name && <span className="text-xs text-green-600">Link copied</span>}
            </li>
          ))}
        </ul>
      )}
      <div className="mt-4">
        <input
          ref={fileInputRef}
          type="file"
          onChange={(e) => {
            setPendingFile(e.target.files?.[0] ?? null);
            setConflictFile(null);
            setUploadSuccess(null);
          }}
        />
        <button
          disabled={!pendingFile}
          onClick={() => pendingFile && handleUpload(pendingFile, false)}
          className="ml-2 text-sm underline"
        >
          Upload
        </button>
        {uploadSuccess && (
          <p className="mt-2 text-sm text-green-600">&quot;{uploadSuccess}&quot; uploaded successfully.</p>
        )}
        {conflictFile && (
          <div className="mt-2 text-sm text-red-600">
            &quot;{conflictFile}&quot; already exists —{' '}
            <button onClick={() => pendingFile && handleUpload(pendingFile, true)} className="underline">
              replace?
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run (from `web/`): `npx vitest run src/components/FileBrowser.test.tsx --pool=forks --no-file-parallelism`
Expected: PASS (all 10 tests).

- [ ] **Step 5: Run the full frontend suite**

Run (from `web/`): `npx vitest run --pool=forks --no-file-parallelism`
Expected: PASS (every test file in the project — `--pool=forks --no-file-parallelism` avoids the resource-contention flakiness seen with the default parallel pool on this machine).

- [ ] **Step 6: Commit**

```bash
git add web/src/components/FileBrowser.tsx web/src/components/FileBrowser.test.tsx
git commit -m "feat(web): add Share button and icon-only Download/Share to FileBrowser"
```
