#!/usr/bin/env bash
# Sets sesshin up for OpenCode: the permission rules that let the agent run sesshin
# and jq without prompting (except `sesshin install` and `sesshin uninstall`, which
# propose changes to this machine's Claude Code setup, and `sesshin prune`, which
# deletes sessions), and the skill, as a link to this clone's copy.
#
# Claude Code needs none of this: its rules come with `sesshin install`'s
# proposal, and its skill from the sesshin plugin; see the README.
#
#   scripts/opencode.sh                 # set it up
#   scripts/opencode.sh --uninstall     # take it out again
#
# Rules in ${XDG_CONFIG_HOME:-~/.config}/opencode/opencode.json; the skill as a
# link, opencode/skills/sesshin. Safe to rerun. The file is backed up (to
# .bak.<timestamp>, a new one each time) before it changes, and only sesshin's
# rules are added or removed; an opencode.jsonc, or an opencode.json jq can't
# parse, is never touched: the rules are printed to add by hand. Runs under
# macOS's /bin/bash 3.2. Needs jq.
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd -P)
skill_src=$repo/claude/skills/sesshin

oc_dir=${XDG_CONFIG_HOME:-$HOME/.config}/opencode
oc_json=$oc_dir/opencode.json
oc_jsonc=$oc_dir/opencode.jsonc
oc_link=$oc_dir/skills/sesshin
# The last matching rule wins in OpenCode, so the asks come after "sesshin *".
oc_rules='{
  "sesshin *": "allow",
  "jq *": "allow",
  "sesshin install*": "ask",
  "sesshin uninstall*": "ask",
  "sesshin prune*": "ask"
}'
# Uninstalling leaves "jq *": other tools (koan) rely on it too.
oc_sesshin_rules=$(jq -c 'del(.["jq *"])' <<<"$oc_rules")

usage() {
	echo "usage: $0 [--uninstall]" >&2
	exit 2
}

mode=install
for arg in "$@"; do
	case $arg in
	--uninstall) mode=uninstall ;;
	*) usage ;;
	esac
done

command -v jq >/dev/null || { echo "opencode.sh needs jq on PATH" >&2; exit 1; }
if [[ $mode == install ]] && ! command -v sesshin >/dev/null; then
	echo "sesshin is not on PATH. Install it from this clone with:" >&2
	echo "  GOBIN=~/.local/bin go install ./cmd/sesshin ./cmd/sesshin-hook" >&2
	echo "with GOBIN a directory on PATH, then rerun this script." >&2
	exit 1
fi

changed=
failed=

# is_our_link PATH: PATH is a symlink to this clone's skill.
is_our_link() {
	local target
	[[ -L $1 ]] || return 1
	target=$(cd "$1" 2>/dev/null && pwd -P) || return 1
	[[ $target == "$skill_src" ]]
}

# A skill in ~/.claude/skills would load a second time: Claude Code has it from
# the plugin, and OpenCode reads that directory too. An earlier version of this
# script linked it there; anything else there is the user's, so stop before
# changing anything.
stray=
for p in "$HOME/.claude/skills/sesshin" ${CLAUDE_CONFIG_DIR:+"$CLAUDE_CONFIG_DIR/skills/sesshin"}; do
	if [[ -e $p || -L $p ]] && ! is_our_link "$p"; then
		echo "skill: $p exists and isn't a link to $skill_src." >&2
		echo "  It would load as a second sesshin skill, beside the plugin's or OpenCode's." >&2
		stray=1
	fi
done
if [[ -n $stray ]]; then
	if [[ $mode == install ]]; then
		echo "Move it out of the way and rerun. Nothing was changed." >&2
		exit 1
	fi
	echo "  Left in place." >&2
fi
for p in "$HOME/.claude/skills/sesshin" ${CLAUDE_CONFIG_DIR:+"$CLAUDE_CONFIG_DIR/skills/sesshin"}; do
	if is_our_link "$p"; then
		rm "$p"
		changed=1
		echo "skill: removed the old link $p"
	fi
done

# write_json LABEL FILE JSON: backs FILE up if it exists, then replaces it.
# Each backup gets its own timestamp, so an install then an uninstall doesn't
# overwrite the original with the installed version.
write_json() {
	local bak n
	mkdir -p "$(dirname "$2")"
	if [[ -e $2 ]]; then
		bak=$2.bak.$(date +%Y%m%d-%H%M%S)
		# Two runs in the same second: don't overwrite the first's.
		if [[ -e $bak ]]; then
			n=2
			while [[ -e $bak.$n ]]; do n=$((n + 1)); done
			bak=$bak.$n
		fi
		cp -p "$2" "$bak"
		echo "$1: backed up to $bak"
	fi
	printf '%s\n' "$3" >"$2.tmp"
	mv "$2.tmp" "$2"
	changed=1
	echo "$1: updated $2"
}

# oc_by_hand: prints the rules to add to, or remove from, a config this script
# won't edit.
oc_by_hand() {
	echo "opencode: $1"
	echo "  so it was left alone."
	if [[ $mode == install ]]; then
		echo "  Add these rules by hand, in this order, at the end of"
		echo "  \"permission\": { \"bash\": { ... } } (in OpenCode the last matching rule"
		echo "  wins, so they must come after any rule matching sesshin):"
		jq -r 'to_entries[] | "    \(.key | tojson): \(.value | tojson),"' <<<"$oc_rules"
		echo "  If \"bash\" is a string, such as \"ask\", make it an object with \"*\" set to"
		echo "  that value first."
	else
		echo "  Remove these rules from \"permission\": { \"bash\": { ... } } by hand:"
		jq -r 'keys_unsorted[] | "    \(tojson)"' <<<"$oc_sesshin_rules"
	fi
}

{
	oc_cur=
	if [[ -e $oc_jsonc ]]; then
		oc_by_hand "$oc_jsonc is JSONC, which jq can't edit without losing comments,"
	elif [[ -e $oc_json ]] && ! jq empty "$oc_json" 2>/dev/null; then
		oc_by_hand "$oc_json isn't plain JSON (comments or trailing commas?),"
	elif [[ -e $oc_json ]]; then
		oc_cur=$(<"$oc_json")
	elif [[ $mode == install ]]; then
		# shellcheck disable=SC2016 # "$schema" is a JSON key
		oc_cur='{"$schema": "https://opencode.ai/config.json"}'
	else
		echo "opencode: no $oc_json, no rules to remove"
	fi

	if [[ -n $oc_cur ]]; then
		if [[ $mode == install ]]; then
			# Drop sesshin's rules wherever they are, then append them, so they
			# end up last and in order. A string default ("bash": "ask")
			# becomes the "*" rule, so it still covers everything else.
			# shellcheck disable=SC2016 # $rules is a jq variable
			filter='
				.permission = (.permission // {} | if type == "string" then {"*": .} else . end)
				| .permission.bash = (
					.permission.bash // {}
					| if type == "string" then {"*": .} else . end
					| with_entries(select(.key as $k | $rules | has($k) | not))
					| . + $rules)'
		else
			# shellcheck disable=SC2016
			filter='
				if (.permission | type) == "object" and (.permission.bash | type) == "object" then
					.permission.bash |= with_entries(select(.key as $k | $rules | has($k) | not))
					| if .permission.bash == {} then del(.permission.bash) else . end
					| if .permission == {} then del(.permission) else . end
				else . end'
		fi
		rules=$oc_rules
		[[ $mode == install ]] || rules=$oc_sesshin_rules
		new=$(jq --argjson rules "$rules" "$filter" <<<"$oc_cur")
		# Order matters here, so don't sort: a reorder is a change.
		if [[ -e $oc_json && $(jq -c . <<<"$oc_cur") == "$(jq -c . <<<"$new")" ]]; then
			echo "opencode: $oc_json already up to date"
		else
			write_json opencode "$oc_json" "$new"
		fi
		if [[ $mode == install ]]; then
			# shellcheck disable=SC2016
			others=$(jq -r --argjson rules "$oc_rules" '
				.permission.bash | keys_unsorted[]
				| select(. as $k | startswith("sesshin") and ($rules | has($k) | not))' <<<"$new")
			if [[ -n $others ]]; then
				echo "opencode: warning: these rules of yours match sesshin commands, but come"
				echo "  before sesshin's, so where both match, sesshin's win:"
				printf '    %s\n' "$others"
			fi
		fi
	fi

	if [[ $mode == install ]]; then
		if is_our_link "$oc_link"; then
			echo "opencode: skill link $oc_link already in place"
		elif [[ -e $oc_link || -L $oc_link ]]; then
			echo "opencode: $oc_link exists and isn't a link to $skill_src; left alone." >&2
			echo "  Move it out of the way and rerun to install the skill." >&2
			failed=1
		else
			mkdir -p "$(dirname "$oc_link")"
			ln -s "$skill_src" "$oc_link"
			changed=1
			echo "opencode: linked the skill, $oc_link -> $skill_src"
		fi
	else
		if is_our_link "$oc_link"; then
			rm "$oc_link"
			changed=1
			echo "opencode: removed the skill link $oc_link"
		elif [[ -e $oc_link || -L $oc_link ]]; then
			echo "opencode: $oc_link isn't a link to $skill_src; left in place"
		fi
	fi
}

[[ -n $changed ]] || echo "Nothing changed."
[[ -z $failed ]]
