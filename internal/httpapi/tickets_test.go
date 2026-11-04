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

	"github.com/Naveenkumar75/omnidesk/internal/store"
)

func call(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// signup registers a fresh tenant and returns its admin's JWT.
func signup(t *testing.T, h http.Handler, label string) string {
	t.Helper()
	n := time.Now().UnixNano()
	email := fmt.Sprintf("%s%d@x.test", label, n)
	reg := fmt.Sprintf(`{"tenant_name":%q,"domain":"%s%d.test","email":%q,"password":"password123"}`, label, label, n, email)
	if rec := call(h, "POST", "/api/v1/auth/register", "", reg); rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	rec := call(h, "POST", "/api/v1/auth/login", "", fmt.Sprintf(`{"email":%q,"password":"password123"}`, email))
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("login: %s", rec.Body)
	}
	return out.Token
}

func TestTicketRoutesRequireAuth(t *testing.T) {
	h := newAPI(t, nil)
	for _, path := range []string{"/api/v1/tickets", "/api/v1/tickets/1"} {
		if rec := call(h, "GET", path, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s: got %d, want 401", path, rec.Code)
		}
	}
}

func TestTicketLifecycleAndTenantIsolation(t *testing.T) {
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

	tokA := signup(t, h, "alpha")
	tokB := signup(t, h, "bravo")

	// Validation.
	if rec := call(h, "POST", "/api/v1/tickets", tokA, `{"subject":"","requester_email":"c@x.test"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty subject: got %d, want 400", rec.Code)
	}
	if rec := call(h, "POST", "/api/v1/tickets", tokA, `{"subject":"s","requester_email":"c@x.test","priority":"asap"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad priority: got %d, want 400", rec.Code)
	}

	// Create.
	rec := call(h, "POST", "/api/v1/tickets", tokA, `{"subject":"Cannot log in","requester_email":"c@x.test","priority":"high"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Status != "open" {
		t.Fatalf("new ticket status = %q, want open", created.Status)
	}
	path := fmt.Sprintf("/api/v1/tickets/%d", created.ID)

	// Tenant isolation: tenant B can neither read nor change tenant A's ticket,
	// and does not see it in its list.
	if rec := call(h, "GET", path, tokB, ""); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET: got %d, want 404", rec.Code)
	}
	if rec := call(h, "POST", path+"/transition", tokB, `{"status":"pending"}`); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant transition: got %d, want 404", rec.Code)
	}
	if rec := call(h, "GET", "/api/v1/tickets", tokB, ""); strings.Contains(rec.Body.String(), "Cannot log in") {
		t.Errorf("tenant B list leaked tenant A's ticket: %s", rec.Body)
	}

	// Valid path: open -> pending -> resolved -> closed.
	for _, to := range []string{"pending", "resolved", "closed"} {
		rec := call(h, "POST", path+"/transition", tokA, fmt.Sprintf(`{"status":%q}`, to))
		if rec.Code != http.StatusOK {
			t.Fatalf("-> %s: %d %s", to, rec.Code, rec.Body)
		}
	}

	// Closed is terminal; unknown status is a bad request.
	if rec := call(h, "POST", path+"/transition", tokA, `{"status":"open"}`); rec.Code != http.StatusConflict {
		t.Errorf("reopen closed: got %d, want 409", rec.Code)
	}
	if rec := call(h, "POST", path+"/transition", tokA, `{"status":"nonsense"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown status: got %d, want 400", rec.Code)
	}

	// Audit trail has exactly the three successful changes, in order.
	rec = call(h, "GET", path, tokA, "")
	var detail struct {
		Events []struct {
			From string `json:"from_status"`
			To   string `json:"to_status"`
		} `json:"events"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &detail)
	want := []string{"open>pending", "pending>resolved", "resolved>closed"}
	if len(detail.Events) != len(want) {
		t.Fatalf("events: got %d, want %d (%s)", len(detail.Events), len(want), rec.Body)
	}
	for i, e := range detail.Events {
		if got := e.From + ">" + e.To; got != want[i] {
			t.Errorf("event %d = %s, want %s", i, got, want[i])
		}
	}

	// Status filter.
	if rec := call(h, "GET", "/api/v1/tickets?status=closed", tokA, ""); !strings.Contains(rec.Body.String(), "Cannot log in") {
		t.Errorf("closed filter should include the ticket: %s", rec.Body)
	}
	if rec := call(h, "GET", "/api/v1/tickets?status=open", tokA, ""); strings.Contains(rec.Body.String(), "Cannot log in") {
		t.Errorf("open filter should not include a closed ticket: %s", rec.Body)
	}
}
