package cli

import (
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/pick"
)

// commands are sesshin's commands, in help order (cli-spec.md, Commands).
var commands = []Command{
	{
		Name:    "list",
		Summary: "List the sessions sesshin has recorded, live by default, with their liveness, metrics, and prompt cache",
		Options: []Option{
			{Name: "liveness", Field: "/liveness", Type: String, Help: "`live` (the default, which includes unknown), ended, or all"},
			{Name: "include-headless", Field: "/include_headless", Type: Bool, Help: "include headless sessions (claude -p, and sessions started by other sessions)"},
			{Name: "fields", Field: "/fields", Type: List, Help: "only these session fields, a comma list of `names`; id and session_id are always included"},
			{Name: "limit", Field: "/limit", Type: Int, Help: "at most `n` sessions; 0 for the count alone"},
		},
		Example: `  sesshin list --fields name,status,cwd
  sesshin list --liveness ended --limit 10 --fields name,ended_at,end_reason
  sesshin list --liveness all --include-headless --limit 0 | jq .result.total   # how many sessions sesshin has`,
		Run: operation(ops.DecodeListInput, func(in ops.ListInput, env Env) ops.Envelope {
			return ops.List(in, env.read())
		}),
	},
	{
		Name:      "show",
		Summary:   "Show one session in full, live or ended, and optionally its raw statusline payload",
		Arguments: []Argument{{Name: "session", Field: "/session"}},
		Options:   []Option{{Name: "include-payload", Field: "/include_payload", Type: Bool, Help: "also return the stored statusline payload, verbatim"}},
		Example: `  sesshin show 12 | jq .result.session.prompt_cache
  sesshin show 0b6c5a3e --include-payload | jq .result.statusline_payload.rate_limits`,
		Run: operation(ops.DecodeShowInput, func(in ops.ShowInput, env Env) ops.Envelope {
			return ops.Show(in, env.read())
		}),
	},
	{
		Name:    "install",
		Summary: "Propose wiring sesshin into Claude Code's settings.json, with its hooks and statusline, for you to review and apply",
		Options: []Option{{Name: "dry-run", Field: "/dry_run", Type: Bool, Help: "check and report the changes, writing nothing"}},
		Example: `  sesshin install --dry-run | jq '.result.changes'
  sesshin install | jq -r '.result.apply[]'   # print the review and apply commands
  diff -uN ~/.claude/settings.json ~/.local/state/sesshin/settings.proposed.json
  cat ~/.local/state/sesshin/settings.proposed.json > ~/.claude/settings.json
  sesshin install --dry-run | jq -e 'all(.result.changes[]; .action == "unchanged")'   # wired?`,
		Run: operation(ops.DecodeInstallInput, func(in ops.InstallInput, env Env) ops.Envelope {
			return ops.Install(in, env.setup())
		}),
	},
	{
		Name:    "uninstall",
		Summary: "Propose removing sesshin's hooks and statusline from Claude Code's settings.json, for you to review and apply as install's proposal is",
		Options: []Option{{Name: "dry-run", Field: "/dry_run", Type: Bool, Help: "report the changes, writing nothing"}},
		Example: "  sesshin uninstall | jq -r '.result.apply[]'   # print the review and apply commands",
		Run: operation(ops.DecodeUninstallInput, func(in ops.UninstallInput, env Env) ops.Envelope {
			return ops.Uninstall(in, env.setup())
		}),
	},
	{
		Name:    "version",
		Summary: "Report this binary's version and the file formats it supports",
		Example: "  sesshin version | jq -r .result.version",
		Run: operation(ops.DecodeVersionInput, func(_ ops.VersionInput, env Env) ops.Envelope {
			return ops.Version(env.BuildInfo())
		}),
	}, {
		Name:    "prune",
		Summary: "Remove ended sessions last seen before the retention window",
		Options: []Option{
			{Name: "dry-run", Field: "/dry_run", Type: Bool, Help: "report what would be removed, and remove nothing"},
			{Name: "retain-days", Field: "/retain_days", Type: Int, Help: "remove ended sessions last seen over `n` days ago, for this run only"},
		},
		Example: "  sesshin prune --dry-run | jq '.result.pruned | length'\n  sesshin prune --retain-days 7",
		Run: operation(ops.DecodePruneInput, func(in ops.PruneInput, env Env) ops.Envelope {
			return ops.Prune(in, env.read())
		}),
	},
	{
		Name:    "migrate",
		Summary: "Convert the state directory's files to this binary's formats, by running every pending migration step",
		Options: []Option{{Name: "dry-run", Field: "/dry_run", Type: Bool, Help: "report what would be converted, and convert nothing"}},
		Example: "  sesshin migrate --dry-run | jq '.result | {from, to, sessions: (.changed | length), unconverted}'\n  sesshin migrate",
		Run: operation(ops.DecodeMigrateInput, func(in ops.MigrateInput, env Env) ops.Envelope {
			return ops.Migrate(in, env.read())
		}),
	},
	{
		Name:     "spawn",
		Summary:  "Launch claude in a new tab, split, or OS window of this terminal, optionally under a job name, and wait for it to start",
		Rest:     "/args",
		RestName: "claude args",
		Options: []Option{
			{Name: "job", Field: "/job", Type: String, Help: "reserve this job `name` for the session"},
			{Name: "cwd", Field: "/cwd", Type: Dir, Help: "start in this `dir` (default: the working directory)"},
			{Name: "type", Field: "/type", Type: String, Help: "`tab` (the default), split, or os-window"},
			{Name: "name", Field: "/name", Type: String, Help: "the session `name`, for claude --name and the tab title (default: the job)"},
			{Name: "prompt", Field: "/prompt", Type: String, Help: "the first `prompt`; not with --prompt-file"},
			{Name: "prompt-file", Field: "/prompt", Type: File, Help: "read the first prompt from `file` (- for stdin); not with --prompt"},
			{Name: "var", Field: "/vars", Type: Map, Help: "a user variable of the new window, as `KEY=VALUE`; repeatable"},
			{Name: "extra", Field: "/extra", Type: JSON, Help: "the session's user-owned extra, a JSON `object`, handed to it in its reservation (default {})"},
			{Name: "start-timeout-secs", Field: "/start_timeout_secs", Type: Int, Help: "wait up to `n` seconds for the session to start (default 15); 0 returns once the window is open"},
		},
		Example: `  sesshin spawn --job api --cwd ~/code/api --prompt 'run the test suite and fix failures'
  sesshin spawn --job review-142 -- --model opus                 # a claude flag
  sesshin spawn --job docs --var PROJECT=docs --start-timeout-secs 0
  sesshin spawn --job api | jq .result.session.id                # the new sesshin ID
  gh issue view 42 --json body -q .body | sesshin spawn --job issue-42 --prompt-file -
  sesshin spawn --job auth-3 --extra '{"ticket":"auth-3"}'     # link it to its work`,
		Run: operation(ops.DecodeSpawnInput, func(in ops.SpawnInput, env Env) ops.Envelope {
			return ops.Spawn(in, env.spawn())
		}),
	},
	{
		Name:      "resume",
		Summary:   "Reopen an ended session with claude --resume, in a new tab of this terminal, under its title and job, and wait for it to be live again",
		Arguments: []Argument{{Name: "session", Field: "/session"}},
		Rest:      "/args",
		RestName:  "claude args",
		Options: []Option{
			{Name: "job", Field: "/job", Type: String, Help: "resume under this job `name` instead of the session's own"},
			{Name: "start-timeout-secs", Field: "/start_timeout_secs", Type: Int, Help: "wait up to `n` seconds for the session to be live again (default 15); 0 returns once the tab is open"},
		},
		Example: `  sesshin resume api                                     # the job's last session
  sesshin resume 12 --job api-old                        # its job api is held by another session
  sesshin resume 12 -- --permission-mode acceptEdits     # a claude flag
  sesshin list --liveness ended --limit 5 --fields name,job,end_reason   # find it first`,
		Run: operation(ops.DecodeResumeInput, func(in ops.ResumeInput, env Env) ops.Envelope {
			return ops.Resume(in, env.spawn())
		}),
	},
	{
		Name:      "send",
		Summary:   "Type text into a live session's window as one paste and press Enter; refuses a session mid-turn unless forced",
		Arguments: []Argument{{Name: "session", Field: "/session"}},
		Options: []Option{
			{Name: "text", Field: "/text", Type: String, Help: "the `text` to type; not with --text-file"},
			{Name: "text-file", Field: "/text", Type: File, Help: "read the text from `file` (- for stdin); not with --text"},
			{Name: "submit", Field: "/submit", Type: Bool, Help: "press Enter after the text (the default); --submit=false leaves it in the input box"},
			{Name: "force", Field: "/force", Type: Bool, Help: "send even when the session's turn hasn't ended, e.g. to answer a prompt"},
		},
		OneOf: []string{"text", "text-file"},
		Example: `  sesshin send api --text 'run the tests again'
  sesshin send 12 --text-file review-notes.md
  git diff | sesshin send api --text-file - --submit=false   # paste it, and leave it for you to send
  sesshin send api --text 'yes' --force                       # answer a prompt deliberately
  sesshin show api | jq -r .result.session.status             # waiting or idle: a turn has ended`,
		Run: operation(ops.DecodeSendInput, func(in ops.SendInput, env Env) ops.Envelope {
			return ops.Send(in, env.send())
		}),
	},
	{
		Name:      "focus",
		Summary:   "Bring a live session's window to the front, with its tab and OS window",
		Arguments: []Argument{{Name: "session", Field: "/session"}},
		Example: `  sesshin focus api
  sesshin focus 12
  sesshin list --fields attention | jq -r '[.result.sessions[] | select(.attention == "blocked")][0].id // empty' | xargs -r sesshin focus`,
		Run: operation(ops.DecodeFocusInput, func(in ops.FocusInput, env Env) ops.Envelope {
			return ops.Focus(in, env.focus())
		}),
	},
	{
		Name:      "update",
		Summary:   "Change a session's user-owned extra, live or ended: replace it, or set and remove keys; self names the session this runs in",
		Arguments: []Argument{{Name: "session", Field: "/session"}},
		Objects:   []string{"/extra"},
		Options: []Option{
			{Name: "extra-merge", Field: "/extra/merge", Type: JSON, Help: "set each key of this JSON `object`, replacing its whole value"},
			{Name: "extra-remove", Field: "/extra/remove", Type: Repeat, Help: "delete this `key`; repeatable"},
			{Name: "extra-replace-all", Field: "/extra/replace_all", Type: JSON, Help: "make extra exactly this JSON `object`; {} clears it; not with the others"},
		},
		Example: `  sesshin update self --extra-merge '{"ticket":"auth-3"}'     # from inside a session, after /new
  sesshin update 12 --extra-merge '{"ticket":"auth-3"}'
  sesshin update api --extra-remove ticket --extra-remove note
  sesshin update 0b6c5a3e --extra-replace-all '{}'
  sesshin update 12 --extra-merge '{"status":"review"}' | jq .result.session.extra`,
		Run: operation(ops.DecodeUpdateInput, func(in ops.UpdateInput, env Env) ops.Envelope {
			return ops.Update(in, env.read())
		}),
	},
	{
		Name:     "restart",
		Summary:  "Pick ended sessions in fzf and resume each in a new tab of this terminal; for a person at a terminal, e.g. after a reboot",
		Rest:     "/args",
		RestName: "claude args",
		Options: []Option{
			{Name: "query", Field: "/query", Type: String, Help: "the initial search `text`, e.g. killed"},
		},
		Example: `  sesshin restart --query killed                         # type killed, ctrl-a, Enter: every session a reboot took
  sesshin restart -- --permission-mode acceptEdits       # a claude flag, for every pick
  sesshin restart | jq '.result.actions[] | select(.output.ok | not)'   # what failed`,
		Run: operation(pick.DecodeInput, func(in pick.Input, env Env) ops.Envelope {
			return pick.Restart(in, env.pick())
		}),
	},
	{
		Name:    "jump",
		Summary: "Pick a live session in fzf, the ones that want you first, and bring its window to the front; for a person at a terminal",
		Options: []Option{
			{Name: "query", Field: "/query", Type: String, Help: "the initial search `text`, in fzf's syntax, e.g. working"},
		},
		Example: `  sesshin jump                                           # the session that wants you is on top: Enter
  sesshin jump --query working                           # start with a filter
  # kitty.conf: map kitty_mod+j launch --type=overlay /path/to/sesshin jump`,
		Run: operation(pick.DecodeJumpInput, func(in pick.JumpInput, env Env) ops.Envelope {
			je := env.jump()
			res := pick.Jump(in, je)
			// The envelope is written first; then a failure is shown on the
			// terminal, and waits for a key (picker-spec.md, jump step 6).
			// A failure it will show is left out of stderr's note.
			if pick.WillShowFailure(res, je.Sys) {
				env.showsFailure()
			}
			env.after(func() { pick.ShowFailure(res, je.Sys) })
			return res
		}),
	},
}
