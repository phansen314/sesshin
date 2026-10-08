# The pickers

`sesshin restart` and `sesshin jump` are for you at a terminal: they list sessions in [fzf](https://github.com/junegunn/fzf) (0.63.0 or later) and act on the ones you pick. Agents use `resume` and `focus` instead. The rules are in [picker-spec.md](../specs/picker-spec.md).

## restart: bring sessions back after a reboot

A reboot, or a kitty closed by mistake, ends every session in it. `sesshin restart` lists the ended ones in [fzf](https://github.com/junegunn/fzf) (0.63.0 or later) and resumes the ones you pick, each in its own new tab, in its own directory, under its own tab title and job. Run it from a tab of the kitty you want them in:

```sh
sesshin restart
```

Type `killed`, press ctrl-a to mark every match (sessions that were still running when something killed them), then Enter. Tab and shift-tab mark single lines, and Esc leaves without resuming anything. `sesshin restart --query killed` starts with the query typed. Claude's own flags are not remembered by `claude --resume`; give them after `--`, and every pick gets them:

```sh
sesshin restart --query killed -- --permission-mode acceptEdits
```

It needs a terminal (it draws on `/dev/tty`), and kitty with remote control on ([how](troubleshooting.md#spawn-resume-send-or-focus-fails-terminal-with-unavailable)), as `sesshin resume` does. Its output is one JSON line of `actions`, one per pick; `jq '.result.actions[] | select(.output.ok | not)'` finds the ones that failed (two picks storing the same job: the second fails `job-taken`, and `sesshin resume <id> --job <other>` brings it back). Style fzf with `FZF_DEFAULT_OPTS` or, for both pickers, `SESSHIN_PICK_OPTS='--height 60% --layout reverse'`. Agents don't run it: they use `sesshin resume`.

## jump: go to the session that needs you

With many sessions open, `sesshin jump` lists the live ones in fzf, with the ones that want you first, next to the prompt where the cursor starts: blocked on a dialog (🔐), stalled (⛔), or finished and waiting for you (🙋), the ones whose prompt cache is about to expire first, since answering them in time saves the re-cache. Enter brings the pick's window to the front (kitty's tab and OS window too); type a job, a directory, or `working` to filter without reordering, and Esc leaves. fzf draws the list bottom up, so the first line is the lowest; `SESSHIN_PICK_OPTS='--layout reverse'` puts it at the top. `sesshin jump --query working` starts with the query typed. It needs a terminal and fzf 0.63.0 or later, as `restart` does, but not kitty remote control until you press Enter. Bind it to a key, in an overlay over whichever window you are in, in `kitty.conf` (name `sesshin` by its full path, and give kitty's `env` a `PATH` with `fzf` if kitty is started from a desktop launcher):

```
map kitty_mod+j launch --type=overlay /path/to/sesshin jump
```

Its output is one JSON line with the `focus` it ran, or `actions: []` when nothing was picked. If it, or the focus, fails, it also prints the message on the terminal and waits for a key, so an overlay doesn't close on it unseen. Agents don't run it: they use `sesshin focus`.
