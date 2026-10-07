package model

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phansen314/sesshin/internal/jsonio"
)

func TestReservationFixture(t *testing.T) {
	r, res := ReadReservation([]byte(fixture(t, "reservation.json")), "api-review")
	if !res.Usable {
		t.Fatal(res.Problems)
	}
	if r.Job != "api-review" || r.Token != "3fa85f6457174562b3fc2c963f66afa6" || r.CreatedAt != "2026-10-03T18:31:51Z" || r.Placement == nil {
		t.Errorf("read %+v", r)
	}
}

func TestReservationInvalid(t *testing.T) {
	for _, tc := range []struct {
		name, field, value, job string
		want                    string
	}{
		{"job is not the file name", "job", `"other"`, "api-review", "its key must be its file's name, api-review"},
		{"all-digit job", "job", `"12"`, "12", reasonJob},
		{"job with an underscore", "job", `"api_x"`, "api_x", reasonJob},
		{"job ending in a hyphen", "job", `"api-"`, "api-", reasonJob},
		{"job too long", "job", `"` + strings.Repeat("a", 65) + `"`, strings.Repeat("a", 65), reasonJob},
		{"job null", "job", `null`, "api-review", reasonString},
		{"token too short", "token", `"3fa85f6457174562b3fc2c963f66afa"`, "api-review", reasonToken},
		{"token with a capital", "token", `"3FA85F6457174562B3FC2C963F66AFA6"`, "api-review", reasonToken},
		{"token with a dash", "token", `"3fa85f64-5717-4562-b3fc-2c963f66afa6"`, "api-review", reasonToken},
		{"unreal created_at", "created_at", `"2026-02-30T00:00:00Z"`, "api-review", "must be a real date and time"},
		{"created_at without a Z", "created_at", `"2026-10-03T18:31:51"`, "api-review", "must be a timestamp: YYYY-MM-DDTHH:MM:SSZ"},
		{"placement without a terminal", "placement", `{}`, "api-review", "required"},
	} {
		field := "/" + tc.field
		if tc.field == "placement" {
			field += "/terminal"
		}
		_, r := ReadReservation([]byte(set(t, "reservation.json", tc.value, tc.field)), tc.job)
		if r.Usable {
			t.Errorf("%s: usable", tc.name)
			continue
		}
		if !slices.ContainsFunc(r.Problems, func(p Problem) bool { return p.Field == field && p.Reason == tc.want }) {
			t.Errorf("%s: problems %v, want %s: %s", tc.name, r.Problems, field, tc.want)
		}
	}
}

func TestReservationNoPlacement(t *testing.T) {
	data := strings.Replace(fixture(t, "reservation.json"), `"placement": {
    "terminal": "kitty",
    "socket": "unix:/tmp/kitty-{kitty.pid}-4099",
    "window_id": 7
  }`, `"placement": null`, 1)
	r, res := ReadReservation([]byte(data), "api-review")
	if !res.Usable || r.Placement != nil {
		t.Errorf("%+v %v", r, res.Problems)
	}
}

// sesshin.json's job and source are required: a file without them is unusable.
func TestSesshinJobAndSource(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		want      []Problem
	}{
		{"without either", `{"schema": 2, "id": 1, "placement": null, "extra": {}}`,
			[]Problem{{"/job", "required"}, {"/source", "required"}}},
		{"without source", `{"schema": 2, "id": 1, "job": null, "placement": null, "extra": {}}`,
			[]Problem{{"/source", "required"}}},
		{"without job", `{"schema": 2, "id": 1, "source": "hook", "placement": null, "extra": {}}`,
			[]Problem{{"/job", "required"}}},
		{"unknown source", `{"schema": 2, "id": 1, "job": null, "source": "adopt", "placement": null, "extra": {}}`,
			[]Problem{{"/source", reasonSource}}},
		{"source null", `{"schema": 2, "id": 1, "job": null, "source": null, "placement": null, "extra": {}}`,
			[]Problem{{"/source", reasonString}}},
		{"all-digit job", `{"schema": 2, "id": 1, "job": "12", "source": "spawn", "placement": null, "extra": {}}`,
			[]Problem{{"/job", reasonJob}}},
		{"job not a string", `{"schema": 2, "id": 1, "job": 3, "source": "spawn", "placement": null, "extra": {}}`,
			[]Problem{{"/job", reasonString}}},
	} {
		_, r := ReadSesshin([]byte(tc.doc))
		if r.Usable || !slices.Equal(r.Problems, tc.want) {
			t.Errorf("%s: %+v, want %v", tc.name, r, tc.want)
		}
	}
	for _, doc := range []string{
		`{"schema": 2, "id": 1, "job": null, "source": "hook", "placement": null, "extra": {}}`,
		`{"schema": 2, "id": null, "job": "api", "source": "spawn", "placement": null, "extra": {}}`,
	} {
		h, r := ReadSesshin([]byte(doc))
		if !r.Usable {
			t.Errorf("%s: %v", doc, r.Problems)
		}
		if h.Source == "" {
			t.Errorf("%s: no source read", doc)
		}
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

// The job keeps its case, and its key is the file's name.
func TestReservationJobKey(t *testing.T) {
	data := strings.Replace(fixture(t, "reservation.json"), `"job": "api-review"`, `"job": "API-Review"`, 1)
	r, res := ReadReservation([]byte(data), "api-review")
	if !res.Usable || r.Job != "API-Review" {
		t.Errorf("%+v %v", r, res.Problems)
	}
	_, res = ReadReservation([]byte(data), "other")
	if res.Usable || !slices.ContainsFunc(res.Problems, func(p Problem) bool {
		return p.Field == "/job" && p.Reason == "its key must be its file's name, other"
	}) {
		t.Errorf("problems %v", res.Problems)
	}
}
