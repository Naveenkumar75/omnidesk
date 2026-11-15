package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Naveenkumar75/omnidesk/internal/auth"
	"github.com/Naveenkumar75/omnidesk/internal/ratelimit"
	"github.com/Naveenkumar75/omnidesk/internal/store"
)

// integrationAPI returns a router backed by a migrated test database, or skips.
func integrationAPI(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	st, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return newAPI(t, st), st
}

func decodeBody[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

func uniq(label string) string { return fmt.Sprintf("%s%d", label, time.Now().UnixNano()) }

// addAgent creates a user in the admin's tenant and returns (user id, token).
func addAgent(t *testing.T, h http.Handler, adminTok, role string) (int, string) {
	t.Helper()
	email := uniq("agent") + "@x.test"
	rec := call(h, "POST", "/api/v1/users", adminTok, fmt.Sprintf(`{"email":%q,"password":"password123","role":%q}`, email, role))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	u := decodeBody[struct{ ID int }](t, rec.Body.Bytes())
	login := call(h, "POST", "/api/v1/auth/login", "", fmt.Sprintf(`{"email":%q,"password":"password123"}`, email))
	tok := decodeBody[struct{ Token string }](t, login.Body.Bytes()).Token
	if tok == "" {
		t.Fatalf("agent login failed: %s", login.Body)
	}
	return u.ID, tok
}

func newTicket(t *testing.T, h http.Handler, tok, subject string) int {
	t.Helper()
	rec := call(h, "POST", "/api/v1/tickets", tok, fmt.Sprintf(`{"subject":%q,"requester_email":"c@x.test"}`, subject))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create ticket: %d %s", rec.Code, rec.Body)
	}
	return decodeBody[struct{ ID int }](t, rec.Body.Bytes()).ID
}

func TestRolesAndUserManagement(t *testing.T) {
	h, _ := integrationAPI(t)
	adminA := signup(t, h, "roles")
	adminB := signup(t, h, "other")

	agentID, agentTok := addAgent(t, h, adminA, "agent")

	// Agents cannot add users; admins can.
	if rec := call(h, "POST", "/api/v1/users", agentTok, `{"email":"x@x.test","password":"password123"}`); rec.Code != http.StatusForbidden {
		t.Errorf("agent creating user: got %d, want 403", rec.Code)
	}
	if rec := call(h, "POST", "/api/v1/users", adminA, `{"email":"y@x.test","password":"password123","role":"root"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid role: got %d, want 400", rec.Code)
	}

	// The agent's token carries the agent role and the admin's tenant.
	me := decodeBody[struct {
		Role     string `json:"role"`
		TenantID int    `json:"tenant_id"`
	}](t, call(h, "GET", "/api/v1/me", agentTok, "").Body.Bytes())
	adminMe := decodeBody[struct {
		TenantID int `json:"tenant_id"`
	}](t, call(h, "GET", "/api/v1/me", adminA, "").Body.Bytes())
	if me.Role != "agent" || me.TenantID != adminMe.TenantID {
		t.Errorf("agent identity: %+v (admin tenant %d)", me, adminMe.TenantID)
	}

	// User lists are tenant scoped.
	listB := call(h, "GET", "/api/v1/users", adminB, "").Body.String()
	if strings.Contains(listB, fmt.Sprintf(`"id":%d,`, agentID)) {
		t.Errorf("tenant B can see tenant A's agent: %s", listB)
	}
}

func TestAssignmentRespectsTenants(t *testing.T) {
	h, _ := integrationAPI(t)
	adminA := signup(t, h, "assignA")
	adminB := signup(t, h, "assignB")
	agentA, _ := addAgent(t, h, adminA, "agent")
	agentB, _ := addAgent(t, h, adminB, "agent")
	id := newTicket(t, h, adminA, "Needs an owner")
	path := fmt.Sprintf("/api/v1/tickets/%d/assignee", id)

	rec := call(h, "PUT", path, adminA, fmt.Sprintf(`{"assignee_id":%d}`, agentA))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fmt.Sprintf(`"assignee_id":%d`, agentA)) {
		t.Fatalf("assign: %d %s", rec.Code, rec.Body)
	}
	// A user id from another tenant is rejected by the composite foreign key.
	if rec := call(h, "PUT", path, adminA, fmt.Sprintf(`{"assignee_id":%d}`, agentB)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("cross-tenant assignee: got %d, want 422", rec.Code)
	}
	// Tenant B cannot touch tenant A's ticket at all.
	if rec := call(h, "PUT", path, adminB, fmt.Sprintf(`{"assignee_id":%d}`, agentB)); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant assign: got %d, want 404", rec.Code)
	}
	// assignee=me / none filters.
	mine := call(h, "GET", "/api/v1/tickets?assignee="+fmt.Sprint(agentA), adminA, "").Body.String()
	if !strings.Contains(mine, "Needs an owner") {
		t.Errorf("assignee filter missed the ticket: %s", mine)
	}
	if rec := call(h, "PUT", path, adminA, `{"assignee_id":null}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"assignee_id":null`) {
		t.Errorf("unassign: %d %s", rec.Code, rec.Body)
	}
}

func TestCommentsStopFirstResponseClock(t *testing.T) {
	h, _ := integrationAPI(t)
	admin := signup(t, h, "comments")
	other := signup(t, h, "intruder")
	id := newTicket(t, h, admin, "Where is my invoice?")
	base := fmt.Sprintf("/api/v1/tickets/%d", id)

	type detail struct {
		Ticket struct {
			FirstResponseAt *time.Time `json:"first_response_at"`
		} `json:"ticket"`
		Comments []struct {
			Internal bool `json:"internal"`
		} `json:"comments"`
	}

	// An internal note is not a response to the customer.
	if rec := call(h, "POST", base+"/comments", admin, `{"body":"checking billing","internal":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("internal note: %d %s", rec.Code, rec.Body)
	}
	d := decodeBody[detail](t, call(h, "GET", base, admin, "").Body.Bytes())
	if d.Ticket.FirstResponseAt != nil {
		t.Fatal("internal note must not set first_response_at")
	}

	if rec := call(h, "POST", base+"/comments", admin, `{"body":"It is under Billing > Invoices."}`); rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body)
	}
	d = decodeBody[detail](t, call(h, "GET", base, admin, "").Body.Bytes())
	if d.Ticket.FirstResponseAt == nil || len(d.Comments) != 2 || !d.Comments[0].Internal {
		t.Fatalf("after reply: %+v", d)
	}

	if rec := call(h, "POST", base+"/comments", other, `{"body":"hi"}`); rec.Code != http.StatusNotFound {
		t.Errorf("cross-tenant comment: got %d, want 404", rec.Code)
	}
	if rec := call(h, "POST", base+"/comments", admin, `{"body":"   "}`); rec.Code != http.StatusBadRequest {
		t.Errorf("blank comment: got %d, want 400", rec.Code)
	}

	for _, s := range []string{"resolved", "closed"} {
		call(h, "POST", base+"/transition", admin, fmt.Sprintf(`{"status":%q}`, s))
	}
	if rec := call(h, "POST", base+"/comments", admin, `{"body":"late"}`); rec.Code != http.StatusConflict {
		t.Errorf("comment on closed ticket: got %d, want 409", rec.Code)
	}
}

func TestTicketPagination(t *testing.T) {
	h, _ := integrationAPI(t)
	admin := signup(t, h, "pages")
	var want []int
	for i := range 5 {
		want = append([]int{newTicket(t, h, admin, fmt.Sprintf("ticket %d", i))}, want...) // newest first
	}

	var got []int
	cursor := ""
	for page := 0; page < 10; page++ {
		path := "/api/v1/tickets?limit=2" + cursor
		resp := decodeBody[struct {
			Tickets    []struct{ ID int } `json:"tickets"`
			NextCursor *int               `json:"next_cursor"`
		}](t, call(h, "GET", path, admin, "").Body.Bytes())
		for _, tk := range resp.Tickets {
			got = append(got, tk.ID)
		}
		if resp.NextCursor == nil {
			break
		}
		cursor = fmt.Sprintf("&cursor=%d", *resp.NextCursor)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("paged ids = %v, want %v", got, want)
	}
	for _, bad := range []string{"?limit=0", "?limit=101", "?cursor=abc", "?assignee=bob", "?breached=maybe"} {
		if rec := call(h, "GET", "/api/v1/tickets"+bad, admin, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", bad, rec.Code)
		}
	}
}

func TestLoginRateLimitedPerEmail(t *testing.T) {
	_, st := integrationAPI(t)
	h := (&API{
		Store:         st,
		Issuer:        auth.NewIssuer("0123456789abcdef", time.Hour),
		LoginPerEmail: ratelimit.New(time.Hour, 3),
	}).Router()
	body := `{"email":"nobody@ratelimit.test","password":"wrong-password"}`
	for i := range 3 {
		if rec := call(h, "POST", "/api/v1/auth/login", "", body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401", i+1, rec.Code)
		}
	}
	rec := call(h, "POST", "/api/v1/auth/login", "", body)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("4th attempt: got %d (Retry-After %q), want 429", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestStrictJSONAndLimits(t *testing.T) {
	h := newAPI(t, nil)
	tok, _ := auth.NewIssuer("0123456789abcdef", time.Hour).Issue(1, 1, "admin")
	cases := map[string]string{
		"unknown field": `{"subject":"s","requester_email":"c@x.test","prority":"high"}`,
		"display-name":  `{"subject":"s","requester_email":"Bob <c@x.test>"}`,
		"huge subject":  fmt.Sprintf(`{"subject":%q,"requester_email":"c@x.test"}`, strings.Repeat("a", 256)),
		"huge descr.":   fmt.Sprintf(`{"subject":"s","description":%q,"requester_email":"c@x.test"}`, strings.Repeat("a", 20001)),
	}
	for name, body := range cases {
		if rec := call(h, "POST", "/api/v1/tickets", tok, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, rec.Code)
		}
	}
	long := strings.Repeat("p", 73)
	if rec := call(h, "POST", "/api/v1/auth/register", "", fmt.Sprintf(`{"tenant_name":"A","domain":"a.test","email":"a@a.test","password":%q}`, long)); rec.Code != http.StatusBadRequest {
		t.Errorf("73-byte password: got %d, want 400", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	if rec := call(newAPI(t, nil), "GET", "/readyz", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no database: got %d, want 503", rec.Code)
	}
	h, _ := integrationAPI(t)
	if rec := call(h, "GET", "/readyz", "", ""); rec.Code != http.StatusOK {
		t.Errorf("with database: got %d, want 200", rec.Code)
	}
}
