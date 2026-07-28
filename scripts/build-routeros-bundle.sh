#!/usr/bin/env bash
set -Eeuo pipefail

# Assemble a secret-free RouterOS package from already-built image archives.
# Docker is deliberately not required here; the caller supplies the exact three
# archives produced and scanned by the container build job.

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
output_root=${1:-"$repo_root/dist"}
release_id=${2:-"dev"}
foxos_image=${FOXOS_IMAGE:-}
mihomo_image=${MIHOMO_IMAGE:-}
mosdns_image=${MOSDNS_IMAGE:-}
bundle_name="foxos-full-amd64-${release_id}"
stage_root="$output_root/$bundle_name"
upgrade_dir_name="foxos-upgrade-${release_id}"
upgrade_stage="$stage_root/$upgrade_dir_name"
archive="$output_root/${bundle_name}.tar.gz"
archive_checksum="${archive}.sha256"

die() {
  printf 'bundle error: %s\n' "$1" >&2
  exit 1
}

[[ "$release_id" =~ ^[A-Za-z0-9._-]+$ ]] || die "release id contains unsupported characters: $release_id"
(( ${#release_id} <= 40 )) || die "release id exceeds the 40-character RouterOS release limit"
[[ -n "$foxos_image" ]] || die "FOXOS_IMAGE must name the scanned FoxOS Docker archive"
[[ -n "$mihomo_image" ]] || die "MIHOMO_IMAGE must name the scanned Mihomo Docker archive"
[[ -n "$mosdns_image" ]] || die "MOSDNS_IMAGE must name the scanned MosDNS Docker archive"
command -v rg >/dev/null 2>&1 || die "ripgrep (rg) is required"

required_files=(
  "$foxos_image"
  "$mihomo_image"
  "$mosdns_image"
  "$repo_root/mihomo/config/config.yaml"
  "$repo_root/mihomo/config/base.yaml"
  "$repo_root/mosdns-config/config_custom.yaml"
  "$repo_root/mihomo/install/mihomo-container.lock.json"
  "$repo_root/mihomo/install/mosdns-container.lock.json"
)
for source in "${required_files[@]}"; do
  [[ -s "$source" ]] || die "missing or empty input: $source"
done

archive_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}
foxos_input_digest=$(archive_sha256 "$foxos_image")
mihomo_input_digest=$(archive_sha256 "$mihomo_image")
mosdns_input_digest=$(archive_sha256 "$mosdns_image")
if [[ "$foxos_input_digest" == "$mihomo_input_digest" || "$foxos_input_digest" == "$mosdns_input_digest" || "$mihomo_input_digest" == "$mosdns_input_digest" ]]; then
  die "FoxOS, Mihomo, and MosDNS input archives must have three distinct SHA-256 identities"
fi

command -v go >/dev/null 2>&1 || die "Go is required to prepare RouterOS-compatible image archives"
tool_root=$(mktemp -d "${TMPDIR:-/tmp}/foxos-routeros-bundle.XXXXXX")
trap 'rm -rf -- "$tool_root"' EXIT
(cd "$repo_root" && go build -trimpath -o "$tool_root/routeros-image" ./cmd/routeros-image)

rm -rf -- "$stage_root" "$archive" "$archive_checksum"
mkdir -p -- "$stage_root" "$upgrade_stage"

"$tool_root/routeros-image" -input "$foxos_image" -output "$upgrade_stage/foxos-amd64.tar" -architecture amd64 -component foxos
"$tool_root/routeros-image" -input "$mihomo_image" -output "$stage_root/mihomo_amd64.tar" -architecture amd64 -component mihomo
"$tool_root/routeros-image" -input "$mosdns_image" -output "$stage_root/mosdns-amd64.tar" -architecture amd64 -component mosdns
cp -a -- "$repo_root/mihomo/config" "$stage_root/mihomo-config"
cp -a -- "$repo_root/mosdns-config" "$stage_root/mosdns-config"
mkdir -p -- "$stage_root/provenance"
cp -- "$repo_root/mihomo/install/mihomo-container.lock.json" "$stage_root/provenance/mihomo-container.lock.json"
cp -- "$repo_root/mihomo/install/mosdns-container.lock.json" "$stage_root/provenance/mosdns-container.lock.json"
cp -- "$repo_root/deploy/routeros/site-config.example.rsc" "$stage_root/site-config.example.rsc"
install -m 0755 -- "$repo_root/deploy/routeros/seal-site-config.sh" "$stage_root/seal-site-config.sh"
cp -- "$repo_root/deploy/routeros/load-site-config.rsc" "$stage_root/load-site-config.rsc"
sed "s/__FOXOS_RELEASE_ID__/$release_id/g" "$repo_root/deploy/routeros/preflight.rsc" > "$stage_root/preflight.rsc"
sed "s/__FOXOS_RELEASE_ID__/$release_id/g" "$repo_root/deploy/routeros/foxos-install-inspect.rsc" > "$stage_root/foxos-install-inspect.rsc"
cp -- "$repo_root/deploy/routeros/foxos-plan.rsc" "$stage_root/foxos-plan.rsc"
sed "s/__FOXOS_RELEASE_ID__/$release_id/g" "$repo_root/deploy/routeros/foxos-full-install.rsc" > "$stage_root/foxos-full-install.rsc"
cp -- "$repo_root/deploy/routeros/foxos-start-all.rsc" "$stage_root/foxos-start-all.rsc"
cp -- "$repo_root/deploy/routeros/foxos-verify.rsc" "$stage_root/foxos-verify.rsc"
cp -- "$repo_root/deploy/routeros/foxos-dns-plan.rsc" "$stage_root/foxos-dns-plan.rsc"
cp -- "$repo_root/deploy/routeros/foxos-dns-apply.rsc" "$stage_root/foxos-dns-apply.rsc"
for lifecycle_script in \
  upgrade-inspect.rsc \
  upgrade-plan.rsc \
  upgrade.rsc \
  upgrade-promote-inspect.rsc \
  upgrade-promote-plan.rsc \
  upgrade-promote.rsc \
  rollback-inspect.rsc \
  rollback-plan.rsc \
  rollback.rsc \
  upgrade-cleanup-inspect.rsc \
  upgrade-cleanup-plan.rsc \
  upgrade-cleanup-apply.rsc; do
  sed "s/__FOXOS_RELEASE_ID__/$release_id/g" "$repo_root/deploy/routeros/$lifecycle_script" > "$upgrade_stage/$lifecycle_script"
done
cp -- "$repo_root/deploy/routeros/foxos-uninstall-inspect.rsc" "$stage_root/foxos-uninstall-inspect.rsc"
cp -- "$repo_root/deploy/routeros/uninstall-plan.rsc" "$stage_root/uninstall-plan.rsc"
cp -- "$repo_root/deploy/routeros/uninstall-apply.rsc" "$stage_root/uninstall-apply.rsc"
cp -- "$repo_root/deploy/routeros/QUICK-INSTALL.md" "$stage_root/QUICK-INSTALL.md"
printf '%s\n' \
  "FoxOS versioned upgrade payload" \
  "release-id: $release_id" \
  "upload-directory: $upgrade_dir_name" \
  "allowed-upgrade-upload: this directory only" \
  "preserve: site-config.rsc, site-config.rsc.sha512, load-site-config.rsc, mihomo-config, mosdns-config, foxos-data, foxos-backups, containers, provenance" \
  "workflow: upgrade-plan.rsc -> upgrade.rsc -> upgrade-promote-plan.rsc -> upgrade-promote.rsc" \
  "rollback: rollback-plan.rsc -> rollback.rsc" \
  "cleanup: upgrade-cleanup-plan.rsc -> upgrade-cleanup-apply.rsc" \
  "integrity: verify this directory's SHA256SUMS before upload" \
  > "$upgrade_stage/UPGRADE-MANIFEST.txt"
find "$stage_root" -name '.DS_Store' -delete

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$upgrade_stage" && find . -type f ! -path './SHA256SUMS' -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
  (cd "$upgrade_stage" && sha256sum --check --status SHA256SUMS) || die "upgrade payload SHA256 verification failed"
else
  (cd "$upgrade_stage" && find . -type f ! -path './SHA256SUMS' -print0 | sort -z | xargs -0 shasum -a 256 > SHA256SUMS)
  (cd "$upgrade_stage" && shasum -a 256 --check SHA256SUMS >/dev/null) || die "upgrade payload SHA256 verification failed"
fi

# A release bundle must never contain a populated controller secret or a common
# credential copied from a local working tree.
grep -Eq '^secret:[[:space:]]*""[[:space:]]*$' "$stage_root/mihomo-config/config.yaml" || die "Mihomo secret is not empty in bundle"
grep -Eq '^[[:space:]]*http:[[:space:]]*"127\.0\.0\.1:9099"[[:space:]]*$' "$stage_root/mosdns-config/config_custom.yaml" || die "MosDNS API must be bound to container loopback"
[[ ! -e "$stage_root/mosdns-config/ui" ]] || die "unused MosDNS management UI must not be present in bundle"
if rg -n '__FOXOS_RELEASE_ID__' "$stage_root"; then
  die "release placeholder was not resolved"
fi
if rg -n --hidden \
  -g '*.{yaml,yml,json,rsc,md,txt}' \
  -g '!foxos-env.example.rsc' \
  -g '!mihomo-config/ui/**' \
  -g '!mosdns-config/rule/**' \
  -g '!mosdns-config/gen/**' \
  '(BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|private-key:[[:space:]]*"?[^"[:space:]]+|uuid:[[:space:]]*"?[0-9a-fA-F-]{32,}|password:[[:space:]]*"[^" ]+"|secret:[[:space:]]*"[^" ]+"|vmess://|vless://|trojan://|ss://|hysteria2://)' \
  "$stage_root"; then
  die "possible credential or node link found in bundle"
fi

printf '%s\n' \
  "FoxOS full RouterOS bundle" \
  "bundle: $bundle_name" \
  "release-id: $release_id" \
  "architecture-name: x86 (x86_64 CPU / linux-amd64 image)" \
  "site-config: copy the immutable example, edit and seal it, then import only load-site-config.rsc" \
  "upload-root: configured by the sealed site-config.rsc (example default disk1/)" \
  "credentials: generated on first RouterOS install" \
  "image-format: single-layer uncompressed Docker archive for RouterOS file import" \
  "upgrade-payload: $upgrade_dir_name (upload only this directory for upgrades)" \
  "upgrade-preserves: runtime configs, loader, data, backups, root-dirs, and site manifest" \
  "image-inputs: explicit FoxOS, Mihomo, and MosDNS archives built by the caller" \
  "component-provenance: provenance/*.lock.json" \
  "release-gate: Core CI and Release workflows scan all three input images with Trivy" \
  "integrity: verify SHA256SUMS before upload" \
  "routeros-validation: static checks only; physical-device acceptance is pending" \
  > "$stage_root/RELEASE-MANIFEST.txt"

expected_roots=(
  QUICK-INSTALL.md
  RELEASE-MANIFEST.txt
  foxos-dns-apply.rsc
  foxos-dns-plan.rsc
  foxos-full-install.rsc
  foxos-install-inspect.rsc
  foxos-start-all.rsc
  foxos-uninstall-inspect.rsc
  foxos-verify.rsc
  load-site-config.rsc
  mihomo-config
  mihomo_amd64.tar
  mosdns-amd64.tar
  mosdns-config
  preflight.rsc
  provenance
  seal-site-config.sh
  site-config.example.rsc
  "$upgrade_dir_name"
  uninstall-apply.rsc
  uninstall-plan.rsc
  foxos-plan.rsc
)
actual_roots=$(find "$stage_root" -mindepth 1 -maxdepth 1 ! -name SHA256SUMS -exec basename {} \; | LC_ALL=C sort)
expected_roots_sorted=$(printf '%s\n' "${expected_roots[@]}" | LC_ALL=C sort)
if [[ "$actual_roots" != "$expected_roots_sorted" ]]; then
  printf 'expected bundle roots:\n%s\n' "$expected_roots_sorted" >&2
  printf 'actual bundle roots:\n%s\n' "$actual_roots" >&2
  die "bundle root allowlist mismatch"
fi
expected_upgrade_roots=(
  SHA256SUMS
  UPGRADE-MANIFEST.txt
  foxos-amd64.tar
  rollback-inspect.rsc
  rollback-plan.rsc
  rollback.rsc
  upgrade-cleanup-apply.rsc
  upgrade-cleanup-inspect.rsc
  upgrade-cleanup-plan.rsc
  upgrade-inspect.rsc
  upgrade-plan.rsc
  upgrade-promote-inspect.rsc
  upgrade-promote-plan.rsc
  upgrade-promote.rsc
  upgrade.rsc
)
actual_upgrade_roots=$(find "$upgrade_stage" -mindepth 1 -maxdepth 1 -exec basename {} \; | LC_ALL=C sort)
expected_upgrade_roots_sorted=$(printf '%s\n' "${expected_upgrade_roots[@]}" | LC_ALL=C sort)
if [[ "$actual_upgrade_roots" != "$expected_upgrade_roots_sorted" ]]; then
  printf 'expected upgrade payload roots:\n%s\n' "$expected_upgrade_roots_sorted" >&2
  printf 'actual upgrade payload roots:\n%s\n' "$actual_upgrade_roots" >&2
  die "upgrade payload root allowlist mismatch"
fi
[[ ! -e "$stage_root/install.rsc" && ! -e "$stage_root/foxos-env.example.rsc" && ! -e "$stage_root/site-config.rsc" ]] || die "mutable or legacy install entry leaked into bundle"
for forbidden_upgrade_root in foxos-amd64.tar upgrade-inspect.rsc upgrade-plan.rsc upgrade.rsc upgrade-promote-inspect.rsc upgrade-promote-plan.rsc upgrade-promote.rsc rollback-inspect.rsc rollback-plan.rsc rollback.rsc upgrade-cleanup-inspect.rsc upgrade-cleanup-plan.rsc upgrade-cleanup-apply.rsc; do
  [[ ! -e "$stage_root/$forbidden_upgrade_root" ]] || die "upgrade-only file leaked into mutable bundle root: $forbidden_upgrade_root"
done

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$stage_root" && find . -type f ! -path './SHA256SUMS' -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
  (cd "$stage_root" && sha256sum --check --status SHA256SUMS) || die "internal SHA256 verification failed"
else
  (cd "$stage_root" && find . -type f ! -path './SHA256SUMS' -print0 | sort -z | xargs -0 shasum -a 256 > SHA256SUMS)
  (cd "$stage_root" && shasum -a 256 --check SHA256SUMS >/dev/null) || die "internal SHA256 verification failed"
fi

tar -czf "$archive" -C "$output_root" "$bundle_name"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$output_root" && sha256sum "$(basename "$archive")" > "$(basename "$archive_checksum")")
else
  (cd "$output_root" && shasum -a 256 "$(basename "$archive")" > "$(basename "$archive_checksum")")
fi
printf '%s\n' "$archive" "$archive_checksum"
