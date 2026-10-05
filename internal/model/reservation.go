package model

import (
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// ReservationExt is the end of a reservation's file name: <job>.json.
const ReservationExt = ".json"

// A reservation's staleness limits (design-spec.md, Reservations): fixed, not
// configured, since the hooks judge freshness too and read no config.toml.
const (
	// ReservationStranded is how long after created_at a reservation may
	// still have no placement.
	ReservationStranded = 120 * time.Second
	// ReservationExpired is the age past which a reservation is stale
	// whatever its window.
	ReservationExpired = 86400 * time.Second
)

// The reasons Staleness gives, in precedence order.
const (
	StaleExpired  = "expired"
	StaleStranded = "stranded"
)

// ReservationFile is reservations/<job>.json (design-spec.md,
// reservations/<job>.json), its fields in the schema's order.
type ReservationFile struct {
	Schema ReservationVersion `json:"schema"`
	// Job is also the file's name.
	Job       string    `json:"job"`
	Token     string    `json:"token"`
	CreatedAt Timestamp `json:"created_at"`
	// Placement is the launched window, kept whole as sesshin.json's is; nil
	// until the launch returns.
	Placement *jsonio.Object `json:"placement"`
}

// ReadReservation reads the reservation in the file named job + ".json".
// Beyond the schema, its job must be the file's name (design-spec.md, File
// schemas). Its content is meaningful only when the result is usable.
func ReadReservation(data []byte, job string) (ReservationFile, FileResult) {
	return readFile(data, ReservationSchema, func(rf *ReservationFile, f *Fields, p *Problems) {
		if v, ok := f.Required("job"); ok {
			if s, ok := p.guarded(v, f.Ptr("job"), IsJob, reasonJob); ok {
				rf.Job = s
				if s != job {
					p.AddAdditional(f.Ptr("job"), "must be its file's name, "+job)
				}
			}
		}
		if v, ok := f.Required("token"); ok {
			rf.Token, _ = p.guarded(v, f.Ptr("token"), IsToken, reasonToken)
		}
		rf.CreatedAt = timestamp(f, p, "created_at")
		rf.Placement = placementField(f, p)
	})
}

// Staleness is why a usable reservation is stale by what the file says alone,
// StaleExpired or StaleStranded, and "" when it is fresh at now. A reservation
// whose launched window no longer exists is stale too, but only an operation
// that asks the terminal can tell: the hooks and every other check judge by
// age alone. The one freshness rule, for prune and the hooks both.
func (r ReservationFile) Staleness(now time.Time) string {
	switch age := now.Sub(r.CreatedAt.Time()); {
	case age > ReservationExpired:
		return StaleExpired
	case r.Placement == nil && age > ReservationStranded:
		return StaleStranded
	}
	return ""
}

// Fresh reports whether the reservation is fresh at now, by age alone.
func (r ReservationFile) Fresh(now time.Time) bool { return r.Staleness(now) == "" }
