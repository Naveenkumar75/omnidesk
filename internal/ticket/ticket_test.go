package ticket

import (
	"errors"
	"testing"
	"time"
)

func TestCanTransition(t *testing.T) {
	allowed := [][2]Status{
		{Open, Pending}, {Open, Resolved},
		{Pending, Open}, {Pending, Resolved},
		{Resolved, Open}, {Resolved, Closed},
	}
	for _, c := range allowed {
		if !CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be allowed", c[0], c[1])
		}
	}

	denied := [][2]Status{
		{Open, Closed}, {Open, Open},
		{Pending, Closed}, {Pending, Pending},
		{Resolved, Pending}, {Resolved, Resolved},
		{Closed, Open}, {Closed, Pending}, {Closed, Resolved}, {Closed, Closed},
		{Status("bogus"), Open}, {Open, Status("bogus")},
	}
	for _, c := range denied {
		if CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be denied", c[0], c[1])
		}
	}
}

func TestCheckReturnsTypedError(t *testing.T) {
	err := Check(Closed, Open)
	var te *TransitionError
	if !errors.As(err, &te) || te.From != Closed || te.To != Open {
		t.Fatalf("expected TransitionError, got %v", err)
	}
	if Check(Open, Pending) != nil {
		t.Error("open -> pending should pass")
	}
}

func TestValid(t *testing.T) {
	if !Open.Valid() || Status("x").Valid() {
		t.Error("status validation wrong")
	}
	if !Urgent.Valid() || Priority("x").Valid() {
		t.Error("priority validation wrong")
	}
}

func TestDeadlines(t *testing.T) {
	created := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	fr, res := Deadlines(Urgent, created)
	if !fr.Equal(created.Add(time.Hour)) || !res.Equal(created.Add(4*time.Hour)) {
		t.Errorf("urgent: got %v / %v", fr, res)
	}
	for _, p := range []Priority{Low, Normal, High, Urgent} {
		s := SLAFor(p)
		if s.FirstResponse <= 0 || s.Resolution <= s.FirstResponse {
			t.Errorf("%s: resolution target must exceed first-response target: %+v", p, s)
		}
	}
	if SLAFor("bogus") != SLAFor(Normal) {
		t.Error("unknown priority should fall back to normal")
	}
	// Higher priority must never get a looser target.
	order := []Priority{Low, Normal, High, Urgent}
	for i := 1; i < len(order); i++ {
		if SLAFor(order[i]).FirstResponse > SLAFor(order[i-1]).FirstResponse {
			t.Errorf("%s has a looser first-response target than %s", order[i], order[i-1])
		}
	}
}
