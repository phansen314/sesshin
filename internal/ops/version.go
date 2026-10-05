package ops

import (
	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/model"
)

// VersionOutput is version's result (version-output).
type VersionOutput struct {
	Version  string         `json:"version"`
	Commit   *string        `json:"commit"` // null when the build has no VCS information
	Modified bool           `json:"modified"`
	Go       string         `json:"go"`
	Formats  VersionFormats `json:"formats"`
}

// VersionFormats are the file format versions this binary reads and writes.
type VersionFormats struct {
	State       int `json:"state"`
	Lifecycle   int `json:"lifecycle"`
	Statusline  int `json:"statusline"`
	Sesshin     int `json:"sesshin"`
	Reservation int `json:"reservation"`
	Install     int `json:"install"`
}

// VersionInput is version's input (version-input): an empty object.
type VersionInput struct{}

// DecodeVersionInput is version's own checks: it asks for no member, so any
// member is unknown.
func DecodeVersionInput(*model.Fields, *model.Problems) VersionInput { return VersionInput{} }

// Version describes the binary only: no state directory, no config, no lock.
func Version(b buildinfo.Info) Envelope {
	out := VersionOutput{
		Version:  b.Version,
		Modified: b.Modified,
		Go:       b.Go,
		Formats: VersionFormats{
			State:       model.StateSchema,
			Lifecycle:   model.LifecycleSchema,
			Statusline:  model.StatuslineSchema,
			Sesshin:     model.SesshinSchema,
			Reservation: model.ReservationSchema,
			Install:     model.InstallSchema,
		},
	}
	if b.Commit != "" {
		out.Commit = &b.Commit
	}
	return Succeeded(out)
}
