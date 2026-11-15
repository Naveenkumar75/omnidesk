package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Naveenkumar75/omnidesk/internal/ticket"
)

func seedTenant(t *testing.T, st *Store) (tenantID, userID int) {
	t.Helper()
	n := time.Now().UnixNano()
	u, err := st.CreateTenantWithAdmin(context.Background(), "SLA", fmt.Sprintf("sla%d.test", n), fmt.Sprintf("a%d@sla.test", n), "x")
	if err != nil {
		t.Fatal(err)
	}
	return u.TenantID, u.ID
}

// makeOverdue moves a ticket's deadlines into the past.
func makeOverdue(t *testing.T, st *Store, id int, firstResponse, resolution bool) {
	t.Helper()
	q := `UPDATE tickets SET `
	if firstResponse {
		q += `first_response_due_at = now() - interval '1 minute', `
	}
	if resolution {
		q += `resolution_due_at = now() - interval '1 minute', `
	}
	q += `updated_at = now() WHERE id = $1`
	if _, err := st.db.Exec(q, id); err != nil {
		t.Fatal(err)
	}
}

func breachState(t *testing.T, st *Store, tenantID, id int) (fr, res bool, events []string) {
	t.Helper()
	tk, err := st.GetTicket(context.Background(), tenantID, id)
	if err != nil {
		t.Fatal(err)
	}
	evs, err := st.TicketEvents(context.Background(), tenantID, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		events = append(events, e.Kind)
	}
	return tk.FirstResponseBreachedAt != nil, tk.ResolutionBreachedAt != nil, events
}

func TestSweepMarksOverdueTicketsOnce(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	tenant, user := seedTenant(t, st)
	mk := func(subject string) int {
		tk, err := st.CreateTicket(ctx, tenant, user, NewTicket{Subject: subject, RequesterEmail: "c@x.test", Priority: ticket.High})
		if err != nil {
			t.Fatal(err)
		}
		return tk.ID
	}

	overdue := mk("overdue on both")
	makeOverdue(t, st, overdue, true, true)

	answered := mk("answered in time, resolution overdue")
	if _, err := st.AddComment(ctx, tenant, answered, user, "on it", false); err != nil {
		t.Fatal(err)
	}
	makeOverdue(t, st, answered, true, true)

	resolved := mk("resolved before the deadline passed")
	if _, err := st.TransitionTicket(ctx, tenant, resolved, user, ticket.Resolved); err != nil {
		t.Fatal(err)
	}
	makeOverdue(t, st, resolved, true, true)

	notDue := mk("still within SLA")

	for range 3 { // repeated sweeps must not double-mark
		if _, err := st.SweepSLABreaches(ctx, 500); err != nil {
			t.Fatal(err)
		}
	}

	check := func(id int, wantFR, wantRes bool, wantEvents int) {
		t.Helper()
		fr, res, evs := breachState(t, st, tenant, id)
		breaches := 0
		for _, e := range evs {
			if e != "status_change" {
				breaches++
			}
		}
		if fr != wantFR || res != wantRes || breaches != wantEvents {
			t.Errorf("ticket %d: first_response=%v resolution=%v breach events=%d (%v), want %v %v %d",
				id, fr, res, breaches, evs, wantFR, wantRes, wantEvents)
		}
	}
	check(overdue, true, true, 2)
	check(answered, false, true, 1)
	check(resolved, false, false, 0)
	check(notDue, false, false, 0)
}

// Concurrent sweepers (several API replicas) must claim disjoint rows: every
// overdue ticket ends up with exactly one breach event.
func TestConcurrentSweepsDoNotDoubleCount(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	tenant, user := seedTenant(t, st)
	const n = 60
	ids := make([]int, 0, n)
	for i := range n {
		tk, err := st.CreateTicket(ctx, tenant, user, NewTicket{Subject: fmt.Sprint(i), RequesterEmail: "c@x.test", Priority: ticket.Low})
		if err != nil {
			t.Fatal(err)
		}
		makeOverdue(t, st, tk.ID, true, false)
		ids = append(ids, tk.ID)
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				r, err := st.SweepSLABreaches(ctx, 7) // small batches to force interleaving
				if err != nil {
					t.Error(err)
					return
				}
				if r.FirstResponse == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()

	var events int
	if err := st.db.QueryRow(
		`SELECT count(*) FROM ticket_events WHERE tenant_id = $1 AND kind = 'first_response_breach'`, tenant,
	).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != n {
		t.Fatalf("got %d breach events for %d overdue tickets", events, n)
	}
}

func TestCreateTicketSetsDeadlinesFromPriority(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	tenant, user := seedTenant(t, st)
	tk, err := st.CreateTicket(ctx, tenant, user, NewTicket{Subject: "s", RequesterEmail: "c@x.test", Priority: ticket.Urgent})
	if err != nil {
		t.Fatal(err)
	}
	if got := tk.FirstResponseDueAt.Sub(tk.CreatedAt); got != time.Hour {
		t.Errorf("urgent first-response window = %v, want 1h", got)
	}
	if got := tk.ResolutionDueAt.Sub(tk.CreatedAt); got != 4*time.Hour {
		t.Errorf("urgent resolution window = %v, want 4h", got)
	}
}
