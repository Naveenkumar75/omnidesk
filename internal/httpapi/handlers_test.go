package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Naveenkumar75/omnidesk/internal/auth"
	"github.com/Naveenkumar75/omnidesk/internal/store"
)

func newAPI(t *testing.T, st *store.Store) http.Handler {
	t.Helper()
	return (&API{Store: st, Issuer: auth.NewIssuer("0123456789abcdef", time.Hour)}).Router()
}

func post(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Validation fails before the database is touched, so no Store is needed.
func TestRegisterValidation(t *testing.T) {
	h := newAPI(t, nil)
	cases := map[string]string{
		"bad json":       `{`,
		"missing tenant": `{"domain":"a.test","email":"a@a.test","password":"password123"}`,
		"bad email":      `{"tenant_name":"A","domain":"a.test","email":"nope","password":"password123"}`,
		"short password": `{"tenant_name":"A","domain":"a.test","email":"a@a.test","password":"short"}`,
	}
	for name, body := range cases {
		if rec := post(h, "/api/v1/auth/register", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, rec.Code)
		}
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	newAPI(t, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestMeRequiresAuth(t *testing.T) {
	rec := httptest.NewRecorder()
	newAPI(t, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

// Integration test against a real PostgreSQL. Skipped unless TEST_DATABASE_URL is set.
func TestRegisterLoginFlow(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	h := newAPI(t, st)

	n := time.Now().UnixNano()
	domain := fmt.Sprintf("t%d.test", n)
	email := fmt.Sprintf("admin@t%d.test", n)
	reg := fmt.Sprintf(`{"tenant_name":"T","domain":%q,"email":%q,"password":"password123"}`, domain, email)

	if rec := post(h, "/api/v1/auth/register", reg); rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}

	// Duplicate domain must conflict and must not leave a second tenant behind
	// (the tenant insert is rolled back with the failed user insert).
	dup := fmt.Sprintf(`{"tenant_name":"T2","domain":%q,"email":"other%d@x.test","password":"password123"}`, domain, n)
	if rec := post(h, "/api/v1/auth/register", dup); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: got %d, want 409", rec.Code)
	}

	if rec := post(h, "/api/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":"wrong"}`, email)); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: got %d, want 401", rec.Code)
	}

	rec := post(h, "/api/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":"password123"}`, email))
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("no token in %s", rec.Body)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	me := httptest.NewRecorder()
	h.ServeHTTP(me, req)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"role":"admin"`) {
		t.Errorf("me: %d %s", me.Code, me.Body)
	}
}
