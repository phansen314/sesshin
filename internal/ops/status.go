package ops

import (
	"path/filepath"
	"strconv"

	"github.com/phansen314/sesshin/internal/model"
)

// migrationStatus is the Migration status of operations.md: state.json read
// once, with no lock, into zero or one warning. A usable file is compared by
// its migration with this binary's latest; one in an older format records 0;
// one in a newer format is ahead, with nothing recorded. Missing, unreadable,
// and corrupt files give none.
func migrationStatus(env ReadEnv) []Warning {
	l, e := resolveLocations(env.GOOS, env.Getenv)
	if e != nil {
		return nil
	}
	data, err := env.FS.ReadFile(filepath.Join(l.StateDir, model.StateName))
	if err != nil {
		return nil
	}
	s, r := model.ReadState(data)
	var recorded *int64
	switch {
	case r.Usable:
		recorded = &s.Migration
	case r.OtherFormat && r.Found < model.StateSchema:
		recorded = ptrTo(int64(0))
	case r.OtherFormat:
		return []Warning{aheadWarning(nil)}
	default:
		return nil
	}
	switch {
	case *recorded < model.LatestMigration:
		return []Warning{{
			Kind:    KindMigrationPending,
			Message: "state.json records migration " + itoa(*recorded) + ", behind this binary's " + itoa(model.LatestMigration) + "; run sesshin migrate",
			Details: map[string]any{"recorded": *recorded, "latest": int64(model.LatestMigration)},
		}}
	case *recorded > model.LatestMigration:
		return []Warning{aheadWarning(recorded)}
	}
	return nil
}

func aheadWarning(recorded *int64) Warning {
	msg := "state.json is in a newer format than this binary reads; a newer binary wrote this state directory"
	var rec any // JSON null for a newer format
	if recorded != nil {
		msg = "state.json records migration " + itoa(*recorded) + ", past this binary's " + itoa(model.LatestMigration) + "; a newer binary wrote this state directory"
		rec = *recorded
	}
	return Warning{
		Kind:    KindMigrationAhead,
		Message: msg,
		Details: map[string]any{"recorded": rec, "latest": int64(model.LatestMigration)},
	}
}

// withStatus puts the migration status first among res's warnings.
func withStatus(ws []Warning, res Envelope) Envelope {
	if len(ws) == 0 {
		return res
	}
	res.Warnings = append(ws, res.Warnings...)
	return res
}

// List is list (operations.md), with the migration status.
func List(in ListInput, env ReadEnv) Envelope {
	ws := migrationStatus(env)
	return withStatus(ws, listOp(in, env))
}

// Show is show, with the migration status.
func Show(in ShowInput, env ReadEnv) Envelope {
	ws := migrationStatus(env)
	return withStatus(ws, showOp(in, env))
}

// Prune is prune, with the migration status.
func Prune(in PruneInput, env ReadEnv) Envelope {
	ws := migrationStatus(env)
	return withStatus(ws, pruneOp(in, env))
}

// Install is install, with the migration status.
func Install(in InstallInput, s Setup) Envelope {
	ws := migrationStatus(ReadEnv{FS: s.FS, Getenv: s.Getenv, GOOS: s.GOOS})
	return withStatus(ws, installOp(in, s))
}

// Spawn is spawn, with the migration status.
func Spawn(in SpawnInput, env SpawnEnv) Envelope {
	ws := migrationStatus(env.ReadEnv)
	return withStatus(ws, spawnOp(in, env))
}

// Resume is resume, with the migration status.
func Resume(in ResumeInput, env SpawnEnv) Envelope {
	ws := migrationStatus(env.ReadEnv)
	return withStatus(ws, resumeOp(in, env))
}

// Send is send, with the migration status.
func Send(in SendInput, env SendEnv) Envelope {
	ws := migrationStatus(env.ReadEnv)
	return withStatus(ws, sendOp(in, env))
}

// Focus is focus, with the migration status.
func Focus(in FocusInput, env FocusEnv) Envelope {
	ws := migrationStatus(env.ReadEnv)
	return withStatus(ws, focusOp(in, env))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
