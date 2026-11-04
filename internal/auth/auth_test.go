package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if h == "correct horse" {
		t.Fatal("hash must not equal plaintext")
	}
	if !CheckPassword(h, "correct horse") {
		t.Error("expected correct password to match")
	}
	if CheckPassword(h, "wrong") {
		t.Error("expected wrong password to fail")
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Error("hashes of the same password must differ (unique salt)")
	}
}

func TestIssueAndParse(t *testing.T) {
	i := NewIssuer("0123456789abcdef", time.Hour)
	tok, err := i.Issue(7, 3, "admin")
	if err != nil {
		t.Fatal(err)
	}
	c, err := i.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID != 7 || c.TenantID != 3 || c.Role != "admin" {
		t.Errorf("unexpected claims: %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	i := NewIssuer("0123456789abcdef", time.Hour)
	tok, _ := i.Issue(1, 1, "admin")

	other := NewIssuer("a-completely-different-secret", time.Hour)
	if _, err := other.Parse(tok); err == nil {
		t.Error("token signed with another secret must be rejected")
	}
	if _, err := i.Parse(tok + "x"); err == nil {
		t.Error("tampered token must be rejected")
	}
	expired := NewIssuer("0123456789abcdef", -time.Minute)
	old, _ := expired.Issue(1, 1, "admin")
	if _, err := i.Parse(old); err == nil {
		t.Error("expired token must be rejected")
	}
	if _, err := i.Parse("not-a-jwt"); err == nil {
		t.Error("garbage must be rejected")
	}
}

func TestMiddleware(t *testing.T) {
	i := NewIssuer("0123456789abcdef", time.Hour)
	h := i.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := FromContext(r.Context())
		if !ok || c.TenantID != 5 {
			t.Errorf("claims missing from context: %+v", c)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	do := func(header string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	tok, _ := i.Issue(1, 5, "agent")
	if got := do("Bearer " + tok); got != http.StatusNoContent {
		t.Errorf("valid token: got %d", got)
	}
	if got := do(""); got != http.StatusUnauthorized {
		t.Errorf("no header: got %d", got)
	}
	if got := do("Bearer nope"); got != http.StatusUnauthorized {
		t.Errorf("bad token: got %d", got)
	}
}
