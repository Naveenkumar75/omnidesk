package ticket

import (
	"errors"
	"testing"
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
