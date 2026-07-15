#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 OUTPUT-DIRECTORY" >&2
  exit 2
fi

out=$1
mkdir -p "$out"
manifest="$out/MANIFEST.txt"
: > "$manifest"
count=0

# Ask the Go tool for modules actually linked by the two shipped commands,
# rather than blindly copying every test/tool module from go.sum.
while IFS='|' read -r module version dir; do
  [ -n "$module" ] || continue
  found=0
  while IFS= read -r -d '' license; do
    relative=${license#"$dir"/}
    key=$(printf '%s@%s--%s' "$module" "$version" "$relative" | tr '/:@ ' '____')
    cp "$license" "$out/$key"
    printf '%s %s | %s | %s\n' "$module" "$version" "$relative" "$key" >> "$manifest"
    found=1
    count=$((count + 1))
  done < <(find "$dir" -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) -print0)
  if [ "$found" -eq 0 ]; then
    echo "linked module has no discoverable licence/notice: $module $version" >&2
    exit 1
  fi
done < <(go list -deps -f '{{with .Module}}{{if and .Dir (ne .Path "github.com/lajosnagyuk/ayfor")}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}{{end}}' ./cmd/ayfor ./cmd/strike | LC_ALL=C sort -u)

if [ "$count" -eq 0 ]; then
  echo "no linked-module licences collected" >&2
  exit 1
fi

LC_ALL=C sort -o "$manifest" "$manifest"
echo "collected $count linked-module licence/notice files in $out"
