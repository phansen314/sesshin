package ops

import (
	"github.com/phansen314/sesshin/internal/model"
	"github.com/phansen314/sesshin/internal/settings"
)

// UninstallInput is uninstall's input (uninstall-input).
type UninstallInput struct{ DryRun bool }

// DecodeUninstallInput is uninstall's own checks: dry_run, a boolean.
func DecodeUninstallInput(f *model.Fields, p *model.Problems) UninstallInput {
	return UninstallInput{DryRun: dryRun(f, p)}
}

// UninstallOutput is uninstall's result (uninstall-output).
type UninstallOutput struct {
	DryRun       bool         `json:"dry_run"`
	SettingsPath string       `json:"settings_path"`
	ProposalPath *string      `json:"proposal_path"` // null with dry_run
	Apply        *[]string    `json:"apply"`         // null with dry_run
	Changes      []ChangeItem `json:"changes"`
}

// Uninstall proposes removing sesshin from Claude Code (operations.md,
// uninstall): the settings.json that install.json records, else the one this
// process resolves. It reads neither config.toml nor hooks.properties, runs no
// self-test, and writes only the proposal.
func Uninstall(in UninstallInput, s Setup) Envelope {
	l, e := s.resolve()
	if e != nil {
		return Failed(e)
	}
	hook, e := s.hookBinary()
	if e != nil {
		return Failed(e)
	}
	old, usable, e := s.readInstall(l.StateDir)
	if e != nil {
		return Failed(e)
	}
	settingsPath, recorded := l.ClaudeSettings, ""
	if usable {
		settingsPath, recorded = old.Locations.ClaudeSettings, old.HookBinary
	}
	tree, e := s.readSettings(settingsPath)
	if e != nil {
		return Failed(e)
	}
	p, err := settings.ProposeUninstall(tree, ours(hook, recorded))
	if err != nil {
		return Failed(internal("proposing: %v", err))
	}

	out := UninstallOutput{DryRun: in.DryRun, SettingsPath: settingsPath, Changes: changeItems(p.Changes)}
	if !in.DryRun {
		proposal, apply, e := s.writeProposal(l.StateDir, settingsPath, p.Tree)
		if e != nil {
			return Failed(e)
		}
		out.ProposalPath, out.Apply = &proposal, &apply
	}
	return Succeeded(out)
}
