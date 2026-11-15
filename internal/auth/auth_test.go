package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

func TestPasswordLengthLimits(t *testing.T) {
	if _, err := HashPassword("short"); err != ErrPasswordLength {
		t.Errorf("short password: got %v", err)
	}
	// bcrypt would silently ignore bytes past 72 (or error); we refuse them.
	long := make([]byte, MaxPasswordLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := HashPassword(string(long)); err != ErrPasswordLength {
		t.Errorf("73-byte password: got %v", err)
	}
	if _, err := HashPassword(string(long[:MaxPasswordLen])); err != nil {
		t.Errorf("72-byte password: %v", err)
	}
}

// Tokens must carry our issuer and audience; a token with the same secret but
// minted by something else is rejected.
func TestParseRequiresIssuerAndAudience(t *testing.T) {
	secret := "0123456789abcdef"
	forge := func(c jwt.Claims) string {
		s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
		return s
	}
	exp := jwt.NewNumericDate(time.Now().Add(time.Hour))
	i := NewIssuer(secret, time.Hour)
	cases := map[string]Claims{
		"no issuer":    {UserID: 1, TenantID: 1, Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Audience: jwt.ClaimStrings{TokenAudience}, ExpiresAt: exp}},
		"bad audience": {UserID: 1, TenantID: 1, Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Issuer: TokenIssuer, Audience: jwt.ClaimStrings{"other"}, ExpiresAt: exp}},
		"no expiry":    {UserID: 1, TenantID: 1, Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Issuer: TokenIssuer, Audience: jwt.ClaimStrings{TokenAudience}}},
		"unknown role": {UserID: 1, TenantID: 1, Role: "root", RegisteredClaims: jwt.RegisteredClaims{Issuer: TokenIssuer, Audience: jwt.ClaimStrings{TokenAudience}, ExpiresAt: exp}},
		"no tenant":    {UserID: 1, Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Issuer: TokenIssuer, Audience: jwt.ClaimStrings{TokenAudience}, ExpiresAt: exp}},
	}
	for name, c := range cases {
		if _, err := i.Parse(forge(c)); err == nil {
			t.Errorf("%s: token accepted", name)
		}
	}
}

func TestRequireRole(t *testing.T) {
	i := NewIssuer("0123456789abcdef", time.Hour)
	h := i.Middleware(RequireRole(RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	for role, want := range map[string]int{RoleAdmin: http.StatusNoContent, RoleAgent: http.StatusForbidden} {
		tok, _ := i.Issue(1, 1, role)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: got %d, want %d", role, rec.Code, want)
		}
	}
}
