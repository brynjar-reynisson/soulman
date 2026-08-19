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
