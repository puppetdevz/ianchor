#!/bin/bash
set -euo pipefail

# Usage: ./scripts/coverage-check.sh <coverprofile>
#
# `go tool cover -func` prints each percentage rounded to 0.1, so its "100.0%"
# total still passes with a handful of uncovered statements. This gate is exact:
# every function must report 100.0%, and the profile itself must have zero
# statements with a zero execution count.

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <coverprofile>" >&2
  exit 2
fi

profile="$1"
if [ ! -s "$profile" ]; then
  echo "coverage profile $profile is missing or empty" >&2
  exit 1
fi

functions="$(mktemp "${TMPDIR:-/tmp}/ianchor-cover-func.XXXXXX")"
trap 'rm -f "$functions"' EXIT
go tool cover -func="$profile" >"$functions"

uncovered_functions="$(awk 'NR > 1 && $NF != "100.0%"' "$functions")"
if [ -n "$uncovered_functions" ]; then
  echo "functions below 100.0% statement coverage:" >&2
  echo "$uncovered_functions" >&2
  exit 1
fi

read -r statements covered < <(awk '
  NR > 1 {
    statements += $2
    if ($3 > 0) covered += $2
  }
  END { printf "%d %d\n", statements, covered }
' "$profile")

if [ "$statements" -eq 0 ]; then
  echo "coverage profile $profile records no statements" >&2
  exit 1
fi

uncovered=$((statements - covered))
if [ "$uncovered" -ne 0 ]; then
  echo "statement coverage is $covered/$statements ($uncovered uncovered), want all statements covered" >&2
  exit 1
fi

echo "statement coverage: $covered/$statements statements, every function at 100.0%"
