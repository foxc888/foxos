#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
failed=0

flag() {
  printf 'sensitive-material check: %s\n' "$1" >&2
  failed=1
}

# Scan only release inputs and deployment scripts. Protocol names in parser tests
# and public rule lists are intentionally outside this check; the bundle builder
# performs the same check against the assembled release tree.
scan_paths=("$repo_root/mihomo/config" "$repo_root/mosdns-config" "$repo_root/deploy/routeros")
if rg -n --hidden \
  -g '!foxos-env.example.rsc' \
  -g '!*.dat' -g '!*.srs' -g '!**/ui/**' -g '!**/rule/**' -g '!**/gen/**' \
  '(wallentv|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|https?://[^[:space:]/]+:[^[:space:]/]+@|private-key:[[:space:]]*"?[^"[:space:]]+|uuid:[[:space:]]*"?[0-9a-fA-F-]{32,}|password:[[:space:]]*"[^" ]+"|secret:[[:space:]]*"[^" ]+")' \
  "${scan_paths[@]}"; then
  flag "a credential, private key, userinfo URL, or populated Mihomo secret was found"
fi

if rg -n --hidden -g '*.yaml' -g '*.yml' -g '*.json' -g '*.rsc' -g '!foxos-env.example.rsc' 'CHANGE_ME|wallentv' \
  "$repo_root/mihomo/config" "$repo_root/mosdns-config" "$repo_root/deploy/routeros"; then
  flag "placeholder or known leaked identifier found outside the explicitly excluded example files"
fi

if [[ -n "$(git -C "$repo_root" ls-files -z | tr '\0' '\n' | rg '(^|/)(\.env|.*\.pem|.*\.key|.*credentials.*|api-token|confirmation-key|routeros-password|mihomo-secret)$' || true)" ]]; then
  flag "a credential-named file is tracked"
fi

if rg -n --hidden \
  -g '*.rsc' -g '*.md' \
  '(:put[^#]*(\$apiToken|\$confirmationKey|\$routerPassword|\$mihomoSecret|\$secretValue)|/container/envs get[^#]*(FOXOS_API_TOKEN|FOXOS_CONFIRMATION_KEY|FOXOS_ROUTEROS_PASSWORD|FOXOS_MIHOMO_SECRET)[^#]*value|:put[^#]*/file get[^#]*(api-token|confirmation-key|routeros-password|mihomo-secret)[^#]*contents)' \
  "$repo_root/deploy/routeros" "$repo_root/README.md" "$repo_root/docs"; then
  flag "a deployment script or guide can print a production secret value"
fi

if ((failed != 0)); then
  exit 1
fi
printf 'Sensitive-material checks passed.\n'
