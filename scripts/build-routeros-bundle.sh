#!/usr/bin/env bash
set -Eeuo pipefail

# Assemble a secret-free RouterOS package from already-built image archives.
# Docker is deliberately not required here; the caller supplies the FoxOS image
# produced by the container build job.

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
output_root=${1:-"$repo_root/dist"}
release_id=${2:-"dev"}
foxos_image=${FOXOS_IMAGE:-"$repo_root/foxos-amd64.tar"}
bundle_name="foxos-full-amd64-${release_id}"
stage_root="$output_root/$bundle_name"
archive="$output_root/${bundle_name}.tar.gz"
archive_checksum="${archive}.sha256"

die() {
  printf 'bundle error: %s\n' "$1" >&2
  exit 1
}

[[ "$release_id" =~ ^[A-Za-z0-9._-]+$ ]] || die "release id contains unsupported characters: $release_id"
command -v rg >/dev/null 2>&1 || die "ripgrep (rg) is required"
command -v go >/dev/null 2>&1 || die "Go is required to prepare RouterOS-compatible image archives"

tool_root=$(mktemp -d "${TMPDIR:-/tmp}/foxos-routeros-bundle.XXXXXX")
trap 'rm -rf -- "$tool_root"' EXIT
(cd "$repo_root" && go build -trimpath -o "$tool_root/routeros-image" ./cmd/routeros-image)

rm -rf -- "$stage_root" "$archive" "$archive_checksum"
mkdir -p -- "$stage_root"

required_files=(
  "$foxos_image"
  "$repo_root/mihomo/install/mihomo_amd64.tar"
  "$repo_root/mihomo/install/mosdns-v0.6.4-2ac30e8-amd64.tar"
  "$repo_root/mihomo/config/config.yaml"
  "$repo_root/mosdns-config/config_custom.yaml"
)
for source in "${required_files[@]}"; do
  [[ -s "$source" ]] || die "missing or empty input: $source"
done

"$tool_root/routeros-image" -input "$foxos_image" -output "$stage_root/foxos-amd64.tar" -architecture amd64
"$tool_root/routeros-image" -input "$repo_root/mihomo/install/mihomo_amd64.tar" -output "$stage_root/mihomo_amd64.tar" -architecture amd64
"$tool_root/routeros-image" -input "$repo_root/mihomo/install/mosdns-v0.6.4-2ac30e8-amd64.tar" -output "$stage_root/mosdns-amd64.tar" -architecture amd64
cp -a -- "$repo_root/mihomo/config" "$stage_root/mihomo-config"
cp -a -- "$repo_root/mosdns-config" "$stage_root/mosdns-config"
cp -- "$repo_root/deploy/routeros/foxos-env.example.rsc" "$stage_root/foxos-env.example.rsc"
cp -- "$repo_root/deploy/routeros/site-config.rsc" "$stage_root/site-config.rsc"
cp -- "$repo_root/deploy/routeros/preflight.rsc" "$stage_root/preflight.rsc"
cp -- "$repo_root/deploy/routeros/foxos-plan.rsc" "$stage_root/foxos-plan.rsc"
cp -- "$repo_root/deploy/routeros/foxos-full-install.rsc" "$stage_root/foxos-full-install.rsc"
cp -- "$repo_root/deploy/routeros/foxos-start-all.rsc" "$stage_root/foxos-start-all.rsc"
cp -- "$repo_root/deploy/routeros/foxos-verify.rsc" "$stage_root/foxos-verify.rsc"
cp -- "$repo_root/deploy/routeros/foxos-dns-plan.rsc" "$stage_root/foxos-dns-plan.rsc"
cp -- "$repo_root/deploy/routeros/foxos-dns-apply.rsc" "$stage_root/foxos-dns-apply.rsc"
cp -- "$repo_root/deploy/routeros/install.rsc" "$stage_root/install.rsc"
cp -- "$repo_root/deploy/routeros/upgrade.rsc" "$stage_root/upgrade.rsc"
cp -- "$repo_root/deploy/routeros/upgrade-promote.rsc" "$stage_root/upgrade-promote.rsc"
cp -- "$repo_root/deploy/routeros/rollback.rsc" "$stage_root/rollback.rsc"
cp -- "$repo_root/deploy/routeros/QUICK-INSTALL.md" "$stage_root/QUICK-INSTALL.md"
find "$stage_root" -name '.DS_Store' -delete

# A release bundle must never contain a populated controller secret or a common
# credential copied from a local working tree.
grep -Eq '^secret:[[:space:]]*""[[:space:]]*$' "$stage_root/mihomo-config/config.yaml" || die "Mihomo secret is not empty in bundle"
if rg -n --hidden \
  -g '*.{yaml,yml,json,rsc,md,txt}' \
  -g '!foxos-env.example.rsc' \
  -g '!mihomo-config/ui/**' \
  -g '!mosdns-config/ui/**' \
  -g '!mosdns-config/rule/**' \
  -g '!mosdns-config/gen/**' \
  '(BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|private-key:[[:space:]]*"?[^"[:space:]]+|uuid:[[:space:]]*"?[0-9a-fA-F-]{32,}|password:[[:space:]]*"[^" ]+"|secret:[[:space:]]*"[^" ]+"|vmess://|vless://|trojan://|ss://|hysteria2://)' \
  "$stage_root"; then
  die "possible credential or node link found in bundle"
fi

printf '%s\n' \
  "FoxOS full RouterOS bundle" \
  "bundle: $bundle_name" \
  "architecture-name: x86 (x86_64 CPU / linux-amd64 image)" \
  "upload-root: configured by site-config.rsc (default disk1/)" \
  "credentials: generated on first RouterOS install" \
  "image-format: single-layer uncompressed Docker archive for RouterOS file import" \
  "integrity: verify SHA256SUMS before upload" \
  "routeros-validation: static checks only; physical-device acceptance is pending" \
  > "$stage_root/RELEASE-MANIFEST.txt"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$stage_root" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
  (cd "$stage_root" && sha256sum --check --status SHA256SUMS) || die "internal SHA256 verification failed"
else
  (cd "$stage_root" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 shasum -a 256 > SHA256SUMS)
  (cd "$stage_root" && shasum -a 256 --check SHA256SUMS >/dev/null) || die "internal SHA256 verification failed"
fi

tar -czf "$archive" -C "$output_root" "$bundle_name"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$output_root" && sha256sum "$(basename "$archive")" > "$(basename "$archive_checksum")")
else
  (cd "$output_root" && shasum -a 256 "$(basename "$archive")" > "$(basename "$archive_checksum")")
fi
printf '%s\n' "$archive" "$archive_checksum"
