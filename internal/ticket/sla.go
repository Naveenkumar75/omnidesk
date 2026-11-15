package ticket

import "time"

// SLA targets per priority: how long until the first public reply, and until
// the ticket is resolved. Deadlines are fixed when the ticket is created.
//
// Known simplification (documented in the README): the clock does not pause
// while a ticket is pending on the customer, and business hours are ignored.
type SLATarget struct {
	FirstResponse time.Duration
	Resolution    time.Duration
}

var slaPolicy = map[Priority]SLATarget{
	Urgent: {FirstResponse: 1 * time.Hour, Resolution: 4 * time.Hour},
	High:   {FirstResponse: 4 * time.Hour, Resolution: 24 * time.Hour},
	Normal: {FirstResponse: 8 * time.Hour, Resolution: 48 * time.Hour},
	Low:    {FirstResponse: 24 * time.Hour, Resolution: 120 * time.Hour},
}

// SLAFor returns the target for p. Unknown priorities get the Normal target.
func SLAFor(p Priority) SLATarget {
	if t, ok := slaPolicy[p]; ok {
		return t
	}
	return slaPolicy[Normal]
}

// Deadlines returns the first-response and resolution due times for a ticket
// of priority p created at createdAt.
func Deadlines(p Priority, createdAt time.Time) (firstResponse, resolution time.Time) {
	t := SLAFor(p)
	return createdAt.Add(t.FirstResponse), createdAt.Add(t.Resolution)
}
