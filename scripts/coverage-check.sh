#!/bin/sh

set -eu

COVERAGE_FILE=${1:-coverage.out}
OVERALL_MIN=${OVERALL_MIN:-80}
PACKAGE_THRESHOLDS=${PACKAGE_THRESHOLDS:-"./cmd/collection-sync:80 ./internal/config:90 ./internal/plex:85 ./internal/sonarr:85 ./internal/radarr:85"}

if [ ! -f "$COVERAGE_FILE" ]; then
	echo "coverage file not found: $COVERAGE_FILE" >&2
	exit 1
fi

extract_total() {
	if ! cover_output=$(go tool cover -func="$1"); then
		echo "failed to read coverage profile: $1" >&2
		exit 1
	fi
	total=$(printf '%s\n' "$cover_output" | awk '/^total:/ {gsub("%", "", $3); print $3}')
	if [ -z "$total" ]; then
		echo "failed to extract total coverage from: $1" >&2
		exit 1
	fi
	printf '%s\n' "$total"
}

check_threshold() {
	label=$1
	actual=$2
	minimum=$3
	if ! awk -v actual="$actual" -v minimum="$minimum" 'BEGIN { exit !(actual + 0 >= minimum + 0) }'; then
		echo "coverage gate failed for $label: got ${actual}% need ${minimum}%" >&2
		exit 1
	fi
	echo "coverage gate passed for $label: ${actual}% >= ${minimum}%"
}

overall=$(extract_total "$COVERAGE_FILE")
check_threshold "overall" "$overall" "$OVERALL_MIN"

tmp=
cleanup() {
	if [ -n "$tmp" ]; then
		rm -f "$tmp"
	fi
}

trap cleanup EXIT HUP INT TERM

for spec in $PACKAGE_THRESHOLDS; do
	package=${spec%:*}
	minimum=${spec#*:}
	tmp=$(mktemp "${TMPDIR:-/tmp}/coverage-check.XXXXXX")
	go test -covermode=atomic -coverprofile="$tmp" "$package" >/dev/null
	actual=$(extract_total "$tmp")
	rm -f "$tmp"
	tmp=
	check_threshold "$package" "$actual" "$minimum"
done

trap - EXIT HUP INT TERM