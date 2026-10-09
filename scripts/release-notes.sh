#!/usr/bin/env bash
# Prints a release's notes: the section of CHANGELOG.md under its heading, up
# to the next "## ", with each relative link made absolute at the tag, since a
# GitHub release body resolves relative links elsewhere. Fails unless the
# heading is "## VERSION — YYYY-MM-DD": a section still marked unreleased, or
# missing, stops the release.
#
#   scripts/release-notes.sh v1.0.0 phansen314/sesshin
set -euo pipefail

tag=${1:?usage: release-notes.sh TAG OWNER/REPO}
repo=${2:?usage: release-notes.sh TAG OWNER/REPO}
version=${tag#v}

heading=$(grep -m1 -E "^## ${version//./\\.}( |$)" CHANGELOG.md || true)
if [[ -z $heading ]]; then
	echo "::error::CHANGELOG.md has no section for $version" >&2
	exit 1
fi
if [[ ! $heading =~ ^"## $version — "[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
	echo "::error::CHANGELOG.md heading \"$heading\": want \"## $version — YYYY-MM-DD\"" >&2
	exit 1
fi

notes=$(awk -v h="$heading" '/^## / { on = ($0 == h); next } on' CHANGELOG.md |
	TAG=$tag REPO=$repo perl -pe 's{\]\((?!https?://|#|mailto:)([^)]+)\)}{](https://github.com/$ENV{REPO}/blob/$ENV{TAG}/$1)}g' |
	sed '/./,$!d')
if [[ -z ${notes//[[:space:]]/} ]]; then
	echo "::error::CHANGELOG.md's section for $version is empty" >&2
	exit 1
fi
printf '%s\n' "$notes"
