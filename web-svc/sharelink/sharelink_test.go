// web-svc/sharelink/sharelink_test.go
package sharelink_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

// forgeToken builds a token byte-for-byte the way Issue does, but without
// going through Issue — so the empty-secret case can still be constructed
// now that Issue itself refuses it. This *is* the attack being defended
// against: HMAC-SHA256 over an empty key is something anyone can compute,
// so a self-consistent empty-secret token is forgeable by any stranger.
func forgeToken(t *testing.T, secret []byte, root, path, file string, exp time.Time) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"root": root, "path": path, "file": file, "exp": exp.Unix(),
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payloadB64))
	return payloadB64 + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Guards the empty-secret test below against passing vacuously: if
// forgeToken ever stopped producing tokens this package actually accepts,
// "empty secret is rejected" would prove nothing.
func TestForgeToken_ProducesATokenVerifyAccepts(t *testing.T) {
	forged := forgeToken(t, testSecret, "Documents", "Taxes", "note.txt", time.Now().Add(time.Hour))

	root, path, file, err := sharelink.Verify(testSecret, forged)
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}
	if root != "Documents" || path != "Taxes" || file != "note.txt" {
		t.Errorf("Verify() = (%q, %q, %q), want (Documents, Taxes, note.txt)", root, path, file)
	}
}

func TestVerify_EmptySecret_RejectsSelfConsistentForgedToken(t *testing.T) {
	// Signed AND verified with an empty secret: the HMAC genuinely matches,
	// so only the fail-closed guard in Verify stands between this and a
	// total bypass of the /dl/{token} route.
	forged := forgeToken(t, []byte{}, "Documents", "", "note.txt", time.Now().Add(time.Hour))

	for name, secret := range map[string][]byte{"empty slice": {}, "nil": nil} {
		if _, _, _, err := sharelink.Verify(secret, forged); err != sharelink.ErrInvalid {
			t.Errorf("Verify(%s secret) error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestVerify_EmptySecret_RejectsTokenSignedWithARealSecret(t *testing.T) {
	token, _ := sharelink.Issue(testSecret, "Documents", "", "note.txt", time.Hour)

	if _, _, _, err := sharelink.Verify(nil, token); err != sharelink.ErrInvalid {
		t.Fatalf("Verify() error = %v, want ErrInvalid", err)
	}
}

func TestVerify_EmptySecret_RejectsAnEmptyToken(t *testing.T) {
	if _, _, _, err := sharelink.Verify(nil, ""); err != sharelink.ErrInvalid {
		t.Fatalf("Verify() error = %v, want ErrInvalid", err)
	}
}

func TestIssue_EmptySecret_IssuesNoToken(t *testing.T) {
	for name, secret := range map[string][]byte{"empty slice": {}, "nil": nil} {
		token, expiresAt := sharelink.Issue(secret, "Documents", "", "note.txt", time.Hour)
		if token != "" {
			t.Errorf("Issue(%s secret) token = %q, want empty", name, token)
		}
		if !expiresAt.IsZero() {
			t.Errorf("Issue(%s secret) expiresAt = %v, want zero time", name, expiresAt)
		}
	}
}
