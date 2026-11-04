// Package ticket holds the ticket domain rules. It has no database or HTTP
// dependencies so the state machine can be tested in isolation.
package ticket

import "fmt"

type Status string

const (
	Open     Status = "open"
	Pending  Status = "pending"
	Resolved Status = "resolved"
	Closed   Status = "closed"
)

type Priority string

const (
	Low    Priority = "low"
	Normal Priority = "normal"
	High   Priority = "high"
	Urgent Priority = "urgent"
)

// transitions is the complete set of allowed status changes.
// Closed is terminal; a resolved ticket can be reopened before it is closed.
var transitions = map[Status][]Status{
	Open:     {Pending, Resolved},
	Pending:  {Open, Resolved},
	Resolved: {Open, Closed},
	Closed:   {},
}

func (s Status) Valid() bool {
	_, ok := transitions[s]
	return ok
}

func (p Priority) Valid() bool {
	switch p {
	case Low, Normal, High, Urgent:
		return true
	}
	return false
}

func CanTransition(from, to Status) bool {
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

type TransitionError struct{ From, To Status }

func (e *TransitionError) Error() string {
	return fmt.Sprintf("cannot move ticket from %q to %q", e.From, e.To)
}

// Check returns a *TransitionError when the change is not allowed.
func Check(from, to Status) error {
	if !CanTransition(from, to) {
		return &TransitionError{From: from, To: to}
	}
	return nil
}
