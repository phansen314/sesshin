package model

import (
	"strings"
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
)

// ReservationExt is the end of a reservation's file name:
// <key>_<token>.json, or <token>.json with no job.
const ReservationExt = ".json"

// ReservationName is the file name of the reservation of job and token:
// <key>_<token>.json, or <token>.json when job is "" (none). Job names allow
// no underscore, so the "_" separates the two (design-spec.md, Reservations).
func ReservationName(job, token string) string {
	if job == "" {
		return token + ReservationExt
	}
	return JobKey(job) + "_" + token + ReservationExt
}

// ParseReservationName splits a reservation's file name into its job key (""
// for none) and token. A name that is neither <key>_<token>.json nor
// <token>.json, such as one from before tokens (<key>.json), is not a
// reservation's.
func ParseReservationName(name string) (key, token string, ok bool) {
	stem, ok := strings.CutSuffix(name, ReservationExt)
	if !ok {
		return "", "", false
	}
	if IsToken(stem) {
		return "", stem, true
	}
	key, token, ok = strings.Cut(stem, "_")
	if !ok || !IsJobKey(key) || !IsToken(token) {
		return "", "", false
	}
	return key, token, true
}

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

// ReservationFile is reservations/<key>_<token>.json (design-spec.md,
// reservations/<key>_<token>.json), its fields in the schema's order.
type ReservationFile struct {
	Schema ReservationVersion `json:"schema"`
	// Job is as given; its key begins the file's name. Nil for a spawn with
	// no job.
	Job       *string   `json:"job"`
	Token     string    `json:"token"`
	CreatedAt Timestamp `json:"created_at"`
	// Placement is the launched window, kept whole as sesshin.json's is; nil
	// until the launch returns.
	Placement *jsonio.Object `json:"placement"`
	// Extra is the adopting session's extra (design-spec.md, User-owned
	// extra), kept whole in the ordered tree; {} from resume. Never nil in a
	// file that reads usable.
	Extra *jsonio.Object `json:"extra"`
}

// ReadReservation reads the reservation in the file called name. Beyond the
// schema, the name must parse (ParseReservationName), its key must be the
// job's key (none when the job is null), and its token must be the file's
// token (design-spec.md, File schemas). Its content is meaningful only when
// the result is usable.
func ReadReservation(data []byte, name string) (ReservationFile, FileResult) {
	nameKey, nameToken, nameOK := ParseReservationName(name)
	return readFile(data, ReservationSchema, func(rf *ReservationFile, f *Fields, p *Problems) {
		if !nameOK {
			p.AddAdditional("", "its name must be <key>_<token>.json or <token>.json")
		}
		if _, ok := f.Required("job"); ok {
			before := len(p.List())
			rf.Job = nullableGuarded(f, p, "job", IsJob, reasonJob)
			if len(p.List()) == before && nameOK {
				switch {
				case rf.Job == nil && nameKey != "":
					p.AddAdditional(f.Ptr("job"), "must have the key of its file's name, "+nameKey)
				case rf.Job != nil && nameKey == "":
					p.AddAdditional(f.Ptr("job"), "must be null: its file's name has no key")
				case rf.Job != nil && JobKey(*rf.Job) != nameKey:
					p.AddAdditional(f.Ptr("job"), "its key must be its file's name's, "+nameKey)
				}
			}
		}
		if v, ok := f.Required("token"); ok {
			if s, ok := p.guarded(v, f.Ptr("token"), IsToken, reasonToken); ok {
				rf.Token = s
				if nameOK && s != nameToken {
					p.AddAdditional(f.Ptr("token"), "must be its file's name's, "+nameToken)
				}
			}
		}
		rf.CreatedAt = timestamp(f, p, "created_at")
		rf.Placement = placementField(f, p)
		rf.Extra = extraField(f, p)
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
