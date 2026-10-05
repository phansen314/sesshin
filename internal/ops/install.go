package ops

import (
	"errors"
	"path/filepath"

	"github.com/phansen314/sesshin/internal/config"
	"github.com/phansen314/sesshin/internal/hookconf"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/settings"
)

// InstallInput is install's input (install-input).
type InstallInput struct{ DryRun bool }

// DecodeInstallInput is install's own checks: dry_run, a boolean.
func DecodeInstallInput(f *model.Fields, p *model.Problems) InstallInput {
	return InstallInput{DryRun: dryRun(f, p)}
}

// InstallOutput is install's result (install-output).
type InstallOutput struct {
	DryRun       bool         `json:"dry_run"`
	HookBinary   string       `json:"hook_binary"`
	SettingsPath string       `json:"settings_path"`
	ProposalPath *string      `json:"proposal_path"` // null with dry_run
	Apply        *[]string    `json:"apply"`         // null with dry_run
	Changes      []ChangeItem `json:"changes"`
}

// Install proposes wiring sesshin into Claude Code (operations.md, install). Its
// steps, and the order of its errors, are the operation's: check, self-test,
// then (unless dry_run) record, then propose. It never writes settings.json.
func Install(in InstallInput, s Setup) Envelope {
	l, e := s.resolve()
	if e != nil {
		return Failed(e)
	}
	// Step 1: config.toml, hooks.properties, settings.json, in that order.
	cfgPath := filepath.Join(l.ConfigDir, config.FileName)
	if _, err := config.Read(s.FS, cfgPath); err != nil {
		return Failed(configError(cfgPath, err))
	}
	hookPath := filepath.Join(l.ConfigDir, hookconf.FileName)
	if _, err := hookconf.Read(s.FS, hookPath); err != nil {
		return Failed(configError(hookPath, err))
	}
	tree, e := s.readSettings(l.ClaudeSettings)
	if e != nil {
		return Failed(e)
	}

	// Step 2: the self-test.
	hook, e := s.hookBinary()
	if e != nil {
		return Failed(e)
	}
	if e := s.SelfTest(hook, s.Build); e != nil {
		return Failed(e)
	}

	// What an earlier install recorded says which entries are sesshin's too.
	old, usable, e := s.readInstall(l.StateDir)
	if e != nil {
		return Failed(e)
	}
	recorded := ""
	if usable {
		recorded = old.HookBinary
	}
	statusLine, hadStatusLine := tree.Get("statusLine")
	p, err := settings.ProposeInstall(tree, hook, ours(hook, recorded))
	if err != nil {
		return Failed(internal("proposing: %v", err))
	}

	out := InstallOutput{
		DryRun:       in.DryRun,
		HookBinary:   hook,
		SettingsPath: l.ClaudeSettings,
		Changes:      changeItems(p.Changes),
	}
	// Steps 3 to 5.
	if !in.DryRun {
		file := model.InstallFile{
			HookBinary:  hook,
			Version:     s.Build.Version,
			InstalledAt: model.FormatTimestamp(s.Now()),
			Locations: model.InstallLocations{
				ConfigDir:      l.ConfigDir,
				StateDir:       l.StateDir,
				ClaudeSettings: l.ClaudeSettings,
			},
		}
		data, err := jsonio.MarshalFile(file)
		if err != nil {
			return Failed(internal("encoding install.json: %v", err))
		}
		if e := s.publish(l.StateDir, installName, data); e != nil {
			return Failed(e)
		}
		proposal, apply, e := s.writeProposal(l.StateDir, l.ClaudeSettings, p.Tree)
		if e != nil {
			return Failed(e)
		}
		out.ProposalPath, out.Apply = &proposal, &apply
	}

	env := Succeeded(out)
	if p.StatusLineReplaced && hadStatusLine {
		env.Warnings = append(env.Warnings, Warning{
			Kind:    "status-line-replaced",
			Message: "the proposal replaces a statusLine sesshin did not install",
			Details: map[string]any{"settings_path": l.ClaudeSettings, "status_line": statusLine},
		})
	}
	return env
}

// configError is the error of reading config.toml or hooks.properties: corrupt
// for a bad file, else the OS's (io), as every call site classifies it.
func configError(path string, err error) *Error {
	var cc *config.CorruptError
	var hc *hookconf.CorruptError
	switch {
	case errors.As(err, &cc):
		return corrupt(path, cc.Detail)
	case errors.As(err, &hc):
		return corrupt(path, hc.Detail)
	}
	return IOError(path, err)
}
