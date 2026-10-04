#!/usr/bin/env bash
# Keeps .gitignore current (Claude Code hook, see .claude/settings.json).
#
#   gitignore-guard.sh baseline   SessionStart: remember the untracked files that already exist
#   gitignore-guard.sh check      Stop: if new untracked files appeared, or tracked files match
#                                 .gitignore, ask Claude to classify them before finishing
#
# Each path is reported once per session: it's added to the seen list as soon as it's reported.
set -euo pipefail

cd "$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
seen="$(git rev-parse --git-dir)/claude-gitignore-seen"

untracked() { git ls-files --others --exclude-standard --directory --no-empty-directory; }
drift() { git ls-files --cached --ignored --exclude-standard | sed 's/^/tracked-but-ignored: /'; }

case "${1:-}" in
baseline)
	{ untracked; drift; } | sort -u >"$seen"
	;;
check)
	input="$(cat)"
	# A second stop in the same turn means Claude already handled the report.
	[ "$(jq -r '.stop_hook_active // false' <<<"$input")" = true ] && exit 0
	touch "$seen"
	new="$({ untracked; drift; } | sort -u | comm -23 - <(sort -u "$seen"))"
	[ -z "$new" ] && exit 0
	printf '%s\n' "$new" >>"$seen"
	reason="New files outside .gitignore since the last check:
$new

Classify each one: source to commit, or generated/local output (build artefacts, run results,
coverage, reports, caches, secrets) that needs a .gitignore rule. Add rules for the second kind,
'git rm --cached' anything tracked-but-ignored that shouldn't be in the repo, and tell the user
what you decided. Work-in-progress source can stay untracked; just say so."
	jq -n --arg r "$reason" '{decision: "block", reason: $r}'
	;;
*)
	echo "usage: $0 baseline|check" >&2
	exit 2
	;;
esac
