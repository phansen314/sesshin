package model

import "strconv"

// The one format version of each file this binary reads and writes
// (design-spec.md, Format versions): every file's schema const.
const (
	StateSchema       = 2
	LifecycleSchema   = 1
	StatuslineSchema  = 1
	SesshinSchema     = 2
	ReservationSchema = 1
	InstallSchema     = 1
)

// LatestMigration is the last migration step this binary knows: the value a
// fresh state.json records as `migration` (design-spec.md, Migrations).
const LatestMigration = 1

// Each file struct's Schema field has one of these types, which encode as the
// file's version whatever the field holds, so no writer can forget to set it.
type (
	StateVersion       struct{}
	LifecycleVersion   struct{}
	StatuslineVersion  struct{}
	SesshinVersion     struct{}
	ReservationVersion struct{}
	InstallVersion     struct{}
)

func (StateVersion) MarshalJSON() ([]byte, error)       { return version(StateSchema) }
func (LifecycleVersion) MarshalJSON() ([]byte, error)   { return version(LifecycleSchema) }
func (StatuslineVersion) MarshalJSON() ([]byte, error)  { return version(StatuslineSchema) }
func (SesshinVersion) MarshalJSON() ([]byte, error)     { return version(SesshinSchema) }
func (ReservationVersion) MarshalJSON() ([]byte, error) { return version(ReservationSchema) }
func (InstallVersion) MarshalJSON() ([]byte, error)     { return version(InstallSchema) }

func version(n int64) ([]byte, error) { return strconv.AppendInt(nil, n, 10), nil }
