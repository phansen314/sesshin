package model

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
)

const fixtureToken = "3fa85f6457174562b3fc2c963f66afa6"

// fixtureReservation is the file name testdata/reservation.json is read under.
const fixtureReservation = "api-review_" + fixtureToken + ".json"

func TestReservationFixture(t *testing.T) {
	r, res := ReadReservation([]byte(fixture(t, "reservation.json")), fixtureReservation)
	if !res.Usable {
		t.Fatal(res.Problems)
	}
	if r.Job == nil || *r.Job != "api-review" || r.Token != fixtureToken || r.CreatedAt != "2026-10-03T18:31:51Z" || r.Placement == nil || r.Extra == nil || len(r.Extra.Members) != 1 {
		t.Errorf("read %+v", r)
	}
}

// Names are built and parsed, with a job and without; anything else is not a
// reservation's name (design-spec.md, Reservations).
func TestReservationName(t *testing.T) {
	for _, tc := range []struct {
		job, name string
		key       string
	}{
		{"api", "api_" + fixtureToken + ".json", "api"},
		{"API-Review", "api-review_" + fixtureToken + ".json", "api-review"},
		{"", fixtureToken + ".json", ""},
	} {
		if got := ReservationName(tc.job, fixtureToken); got != tc.name {
			t.Errorf("ReservationName(%q) = %q, want %q", tc.job, got, tc.name)
		}
		key, token, ok := ParseReservationName(tc.name)
		if !ok || key != tc.key || token != fixtureToken {
			t.Errorf("ParseReservationName(%q) = %q, %q, %v", tc.name, key, token, ok)
		}
	}
	for _, name := range []string{
		"api.json", "API_" + fixtureToken + ".json", "api_" + fixtureToken, "api_" + fixtureToken[1:] + ".json",
		"api_x_" + fixtureToken + ".json", "_" + fixtureToken + ".json", "12_" + fixtureToken + ".json",
		strings.ToUpper(fixtureToken) + ".json", fixtureToken + ".json.bak", ".json", "",
	} {
		if key, token, ok := ParseReservationName(name); ok {
			t.Errorf("ParseReservationName(%q) = %q, %q: parsed", name, key, token)
		}
	}
}

func TestReservationInvalid(t *testing.T) {
	for _, tc := range []struct {
		name, field, value, file string
		want                     string
	}{
		{"job is not the file name", "job", `"other"`, fixtureReservation, "its key must be its file's name's, api-review"},
		{"all-digit job", "job", `"12"`, "12_" + fixtureToken + ".json", reasonJob},
		{"job with an underscore", "job", `"api_x"`, "api_x_" + fixtureToken + ".json", reasonJob},
		{"job ending in a hyphen", "job", `"api-"`, "api-_" + fixtureToken + ".json", reasonJob},
		{"job too long", "job", `"` + strings.Repeat("a", 65) + `"`, strings.Repeat("a", 65) + "_" + fixtureToken + ".json", reasonJob},
		{"job null with a keyed name", "job", `null`, fixtureReservation, "must have the key of its file's name, api-review"},
		{"job with a job-less name", "job", `"api-review"`, fixtureToken + ".json", "must be null: its file's name has no key"},
		{"job a number", "job", `3`, fixtureReservation, reasonString},
		{"token too short", "token", `"3fa85f6457174562b3fc2c963f66afa"`, fixtureReservation, reasonToken},
		{"token with a capital", "token", `"3FA85F6457174562B3FC2C963F66AFA6"`, fixtureReservation, reasonToken},
		{"token with a dash", "token", `"3fa85f64-5717-4562-b3fc-2c963f66afa6"`, fixtureReservation, reasonToken},
		{"token is not the file's", "token", `"00000000000000000000000000000000"`, fixtureReservation, "must be its file's name's, " + fixtureToken},
		{"unreal created_at", "created_at", `"2026-02-30T00:00:00Z"`, fixtureReservation, "must be a real date and time"},
		{"created_at without a Z", "created_at", `"2026-10-03T18:31:51"`, fixtureReservation, "must be a timestamp: YYYY-MM-DDTHH:MM:SSZ"},
		{"placement without a terminal", "placement", `{}`, fixtureReservation, "required"},
		{"extra an array", "extra", `[]`, fixtureReservation, reasonObject},
		{"extra null", "extra", `null`, fixtureReservation, reasonObject},
		{"extra too deep", "extra", strings.Repeat(`{"a":`, 32) + `{}` + strings.Repeat("}", 32), fixtureReservation, reasonExtraDepth},
	} {
		field := "/" + tc.field
		if tc.field == "placement" {
			field += "/terminal"
		}
		_, r := ReadReservation([]byte(set(t, "reservation.json", tc.value, tc.field)), tc.file)
		if r.Usable {
			t.Errorf("%s: usable", tc.name)
			continue
		}
		if !slices.ContainsFunc(r.Problems, func(p Problem) bool { return p.Field == field && p.Reason == tc.want }) {
			t.Errorf("%s: problems %v, want %s: %s", tc.name, r.Problems, field, tc.want)
		}
	}
	// A name that does not parse makes the file unusable, whatever it holds
	// (an old <key>.json among them).
	for _, name := range []string{"api-review.json", "other_" + fixtureToken + ".json", "x.txt"} {
		_, r := ReadReservation([]byte(fixture(t, "reservation.json")), name)
		if r.Usable {
			t.Errorf("name %s: usable", name)
		}
	}
}

func TestReservationNoPlacement(t *testing.T) {
	data := strings.Replace(fixture(t, "reservation.json"), `"placement": {
    "terminal": "kitty",
    "socket": "unix:/tmp/kitty-{kitty.pid}-4099",
    "window_id": 7
  }`, `"placement": null`, 1)
	r, res := ReadReservation([]byte(data), fixtureReservation)
	if !res.Usable || r.Placement != nil {
		t.Errorf("%+v %v", r, res.Problems)
	}
}

// A reservation with no job (a spawn without one) is named for its token alone.
func TestReservationNoJob(t *testing.T) {
	data := strings.Replace(fixture(t, "reservation.json"), `"job": "api-review"`, `"job": null`, 1)
	r, res := ReadReservation([]byte(data), fixtureToken+".json")
	if !res.Usable || r.Job != nil {
		t.Errorf("%+v %v", r, res.Problems)
	}
}

// An old reservation (schema 1) is in another format, which no binary reads.
func TestReservationOldSchema(t *testing.T) {
	data := strings.Replace(fixture(t, "reservation.json"), `"schema": 2`, `"schema": 1`, 1)
	if _, res := ReadReservation([]byte(data), fixtureReservation); res.Usable || !res.OtherFormat {
		t.Errorf("%+v", res)
	}
}

// Design-spec, Reservations, Fresh or stale: stale by age alone when no
// placement 120 seconds on, or a day old; the limits are inclusive of fresh.
func TestReservationStaleness(t *testing.T) {
	now := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
	placed := &jsonio.Object{}
	for _, tc := range []struct {
		name      string
		age       time.Duration
		placement *jsonio.Object
		want      string
	}{
		{"just made", 0, nil, ""},
		{"exactly at the stranded limit", ReservationStranded, nil, ""},
		{"past the stranded limit", ReservationStranded + time.Second, nil, StaleStranded},
		{"launched, past the stranded limit", time.Hour, placed, ""},
		{"exactly a day old", ReservationExpired, placed, ""},
		{"past a day", ReservationExpired + time.Second, placed, StaleExpired},
		{"unlaunched and past a day: expired first", ReservationExpired + time.Second, nil, StaleExpired},
	} {
		r := ReservationFile{CreatedAt: FormatTimestamp(now.Add(-tc.age)), Placement: tc.placement}
		if got := r.Staleness(now); got != tc.want {
			t.Errorf("%s: Staleness = %q, want %q", tc.name, got, tc.want)
		}
		if got := r.Fresh(now); got != (tc.want == "") {
			t.Errorf("%s: Fresh = %v", tc.name, got)
		}
	}
}

// The job keeps its case, and its key is the file name's.
func TestReservationJobKey(t *testing.T) {
	data := strings.Replace(fixture(t, "reservation.json"), `"job": "api-review"`, `"job": "API-Review"`, 1)
	r, res := ReadReservation([]byte(data), fixtureReservation)
	if !res.Usable || r.Job == nil || *r.Job != "API-Review" {
		t.Errorf("%+v %v", r, res.Problems)
	}
	_, res = ReadReservation([]byte(data), "other_"+fixtureToken+".json")
	if res.Usable || !slices.ContainsFunc(res.Problems, func(p Problem) bool {
		return p.Field == "/job" && p.Reason == "its key must be its file's name's, other"
	}) {
		t.Errorf("problems %v", res.Problems)
	}
}
