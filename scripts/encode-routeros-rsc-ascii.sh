#!/usr/bin/env bash
set -Eeuo pipefail

if (( $# != 2 )); then
  printf 'usage: %s INPUT.rsc OUTPUT.rsc\n' "${0##*/}" >&2
  exit 2
fi

input=$1
output=$2

[[ -f "$input" ]] || {
  printf 'RouterOS ASCII encoder: input is not a file: %s\n' "$input" >&2
  exit 1
}
[[ "$input" != "$output" ]] || {
  printf 'RouterOS ASCII encoder: input and output must differ\n' >&2
  exit 1
}
command -v perl >/dev/null 2>&1 || {
  printf 'RouterOS ASCII encoder: perl is required\n' >&2
  exit 1
}
command -v rg >/dev/null 2>&1 || {
  printf 'RouterOS ASCII encoder: ripgrep (rg) is required\n' >&2
  exit 1
}

umask 022
LC_ALL=C perl -pe 's/([\x80-\xFF])/sprintf("\\%02X", ord($1))/ge' "$input" > "$output"

if LC_ALL=C rg -n '[^\x00-\x7F]' "$output" >/dev/null; then
  printf 'RouterOS ASCII encoder: non-ASCII bytes remain in %s\n' "$output" >&2
  exit 1
fi
