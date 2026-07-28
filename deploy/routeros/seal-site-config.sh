#!/usr/bin/env bash
set -Eeuo pipefail
export LC_ALL=C

config=${1:-site-config.rsc}
digest="${config}.sha512"

die() {
  printf 'site config error: %s\n' "$1" >&2
  exit 1
}

[[ "$config" == "site-config.rsc" || "$config" == */site-config.rsc ]] || die "the output must be named site-config.rsc"
[[ -f "$config" && -s "$config" ]] || die "missing or empty $config"
[[ ! -L "$config" ]] || die "$config must be a regular file, not a symlink"
manifest_version_count=0
management_bridge_count=0
storage_root_count=0
network_count=0
prefix_length_count=0
router_address_count=0
mihomo_address_count=0
mosdns_address_count=0
foxos_address_count=0
public_hostname_count=0
public_hostname=""
private_cidrs_count=0
private_cidrs=""
line_number=0

while IFS= read -r line || [[ -n "$line" ]]; do
  ((line_number += 1))
  [[ "$line" != *$'\r'* ]] || die "line $line_number contains a carriage return; use LF line endings"
  if [[ -z "$line" ]]; then
    continue
  fi
  if [[ "$line" == \#* ]]; then
    [[ "$line" != *';'* && "$line" != *'\\'* ]] || die "line $line_number contains unsafe comment syntax"
    continue
  fi
  case "$line" in
    ':global FoxOSSiteManifestVersion 2')
      ((manifest_version_count += 1))
      ;;
    *)
      if [[ "$line" =~ ^:global\ FoxOSSiteManagementBridge\ \"[A-Za-z0-9][A-Za-z0-9._-]*\"$ ]]; then
        ((management_bridge_count += 1))
      elif [[ "$line" =~ ^:global\ FoxOSSiteStorageRoot\ \"[A-Za-z0-9][A-Za-z0-9._-]*\"$ ]]; then
        ((storage_root_count += 1))
      elif [[ "$line" =~ ^:global\ FoxOSSiteNetwork\ \"[0-9.]+/[0-9]+\"$ ]]; then
        ((network_count += 1))
      elif [[ "$line" =~ ^:global\ FoxOSSitePrefixLength\ [0-9]+$ ]]; then
        ((prefix_length_count += 1))
      elif [[ "$line" =~ ^:global\ FoxOSSiteRouterAddress\ \"[0-9.]+\"$ ]]; then
        ((router_address_count += 1))
      elif [[ "$line" =~ ^:global\ FoxOSSiteMihomoAddress\ \"[0-9.]+\"$ ]]; then
        ((mihomo_address_count += 1))
      elif [[ "$line" =~ ^:global\ FoxOSSiteMosDNSAddress\ \"[0-9.]+\"$ ]]; then
        ((mosdns_address_count += 1))
      elif [[ "$line" =~ ^:global\ FoxOSSiteFoxOSAddress\ \"[0-9.]+\"$ ]]; then
        ((foxos_address_count += 1))
      elif [[ "$line" =~ ^:global\ FoxOSSitePublicHostname\ \"([a-z0-9][a-z0-9.-]*[a-z0-9])\"$ ]]; then
        ((public_hostname_count += 1))
        public_hostname=${BASH_REMATCH[1]}
      elif [[ "$line" =~ ^:global\ FoxOSSiteSubscriptionPrivateCIDRs\ \"([0-9A-Fa-f:.,/]*)\"$ ]]; then
        ((private_cidrs_count += 1))
				private_cidrs=${BASH_REMATCH[1]}
      else
        die "line $line_number is not an allowed site assignment or comment"
      fi
      ;;
  esac
done < "$config"

for count in \
  "$manifest_version_count" \
  "$management_bridge_count" \
  "$storage_root_count" \
  "$network_count" \
  "$prefix_length_count" \
  "$router_address_count" \
  "$mihomo_address_count" \
  "$mosdns_address_count" \
  "$foxos_address_count" \
  "$public_hostname_count" \
  "$private_cidrs_count"; do
  [[ "$count" == 1 ]] || die "the manifest must contain each of the 11 allowed assignments exactly once"
done

[[ ${#public_hostname} -ge 3 && ${#public_hostname} -le 253 ]] \
  || die "FOXOS public hostname must contain 3..253 ASCII bytes"
[[ "$public_hostname" =~ ^[a-z0-9][a-z0-9.-]*[a-z0-9]$ \
  && "$public_hostname" != *..* \
  && "$public_hostname" != *.-* \
  && "$public_hostname" != *-. \
  && "$public_hostname" == *.home.arpa ]] \
  || die "FOXOS public hostname must be a lowercase valid name below home.arpa"
IFS='.' read -r -a public_hostname_labels <<< "$public_hostname"
for public_hostname_label in "${public_hostname_labels[@]}"; do
  [[ -n "$public_hostname_label" ]] || die "FOXOS public hostname contains an empty label"
  public_hostname_label_bytes=$(LC_ALL=C printf '%s' "$public_hostname_label" | wc -c | tr -d '[:space:]')
  ((public_hostname_label_bytes <= 63)) || die "FOXOS public hostname label exceeds 63 ASCII bytes"
  [[ "$public_hostname_label" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] \
    || die "FOXOS public hostname contains an invalid label"
done

command -v python3 >/dev/null 2>&1 || die "python3 is required to validate private subscription CIDRs"
if ! FOXOS_SEAL_PRIVATE_CIDRS="$private_cidrs" python3 - <<'PY'
import ipaddress
import os

raw = os.environ["FOXOS_SEAL_PRIVATE_CIDRS"]
if raw:
    parts = raw.split(",")
    if len(parts) > 32 or any(not part or part.strip() != part for part in parts):
        raise SystemExit(1)
    allowed = tuple(ipaddress.ip_network(value) for value in (
        "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7",
    ))
    seen = set()
    for part in parts:
        try:
            network = ipaddress.ip_network(part, strict=True)
        except ValueError:
            raise SystemExit(1)
        canonical = str(network)
        if canonical != part or canonical in seen:
            raise SystemExit(1)
        if not any(network.version == candidate.version and network.subnet_of(candidate) for candidate in allowed):
            raise SystemExit(1)
        seen.add(canonical)
PY
then
	die "FOXOS_SUBSCRIPTION_PRIVATE_CIDRS must contain at most 32 unique canonical RFC1918/ULA prefixes"
fi

if command -v openssl >/dev/null 2>&1; then
  openssl dgst -sha512 -r < "$config" | awk '{print $1}' > "$digest"
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 512 < "$config" | awk '{print $1}' > "$digest"
else
  die "openssl or shasum is required"
fi

[[ "$(wc -c < "$digest" | tr -d ' ')" == 129 ]] || die "unexpected SHA-512 output"
printf 'sealed %s -> %s\n' "$config" "$digest"
printf 'verify before upload: openssl dgst -sha512 -r %q\n' "$config"
