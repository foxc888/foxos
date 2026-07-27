#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
rsc_root="$repo_root/deploy/routeros"
failed=0

report() {
  printf 'RouterOS static check: %s\n' "$1" >&2
  failed=1
}

while IFS= read -r -d '' script; do
  if ! awk '
    BEGIN { braces = 0; brackets = 0; parens = 0; bad = 0 }
    {
      in_string = 0
      escaped = 0
      for (i = 1; i <= length($0); i++) {
        c = substr($0, i, 1)
        if (escaped) { escaped = 0; continue }
        if (in_string && c == "\\") { escaped = 1; continue }
        if (c == "\"") { in_string = !in_string; continue }
        if (!in_string && c == "#") { break }
        if (!in_string && c == "{") braces++
        if (!in_string && c == "}") braces--
        if (!in_string && c == "[") brackets++
        if (!in_string && c == "]") brackets--
        if (!in_string && c == "(") parens++
        if (!in_string && c == ")") parens--
        if (braces < 0 || brackets < 0 || parens < 0) bad = 1
      }
      if (in_string) { printf "%s:%d: unclosed string\n", FILENAME, NR > "/dev/stderr"; bad = 1 }
    }
    END {
      if (braces != 0 || brackets != 0 || parens != 0) {
        printf "%s: unbalanced delimiters braces=%d brackets=%d parens=%d\n", FILENAME, braces, brackets, parens > "/dev/stderr"
        bad = 1
      }
      exit bad
    }
  ' "$script"; then
    failed=1
  fi
done < <(find "$rsc_root" -maxdepth 1 -type f -name '*.rsc' -print0)

if rg -n -g '*.rsc' '^[[:space:]]*/ip/(dns|route|firewall|dhcp-server/network)(/|[[:space:]])[^#]*(add|set|remove|enable|disable|reset|move)' "$rsc_root"; then
  report "deployment scripts contain a forbidden DNS/DHCP route/firewall write"
fi
if rg -n -g '*.rsc' '^[[:space:]]*/system/device-mode/update' "$rsc_root"; then
  report "deployment scripts must not change device-mode"
fi
if rg -n -g '*.rsc' 'architecture[^#]*(=|!=)[^#]*"x86_64"|file=[^[:space:]]*/\$[A-Za-z_]' "$rsc_root"; then
  report "found an invalid RouterOS architecture or path expression"
fi
if rg -n -g '*.rsc' '/container/add[^#]*(foxos-mihomo|foxos-mosdns)[^#]*envlists=foxos-env' "$rsc_root"; then
  report "Mihomo or MosDNS would inherit FoxOS credentials"
fi
if ! rg -Fq 'env="MOSDNS_AUTO_INIT=0"' "$rsc_root/foxos-full-install.rsc"; then
  report "MosDNS external auto-initialization is not disabled"
fi
for endpoint in \
  'FOXOS_MIHOMO_PROXY_URL|http://10.0.0.2:7890' \
  'FOXOS_MOSDNS_URL|http://10.0.0.3:53'; do
  if ! rg -Fq "$endpoint" "$rsc_root/foxos-full-install.rsc"; then
    report "missing FoxOS runtime endpoint: $endpoint"
  fi
done
if ! rg -Fq '!= 14' "$rsc_root/foxos-full-install.rsc"; then
  report "FoxOS env allowlist does not enforce the 14-key runtime baseline"
fi
if ! rg -Fq 'address] != "10.0.0.1/24"' "$rsc_root/preflight.rsc"; then
  report "preflight does not require the exact RouterOS management prefix"
fi

for invariant in \
  'foxos-mihomo-runtime|mihomo-config|/root/.config/mihomo' \
  'foxos-mihomo-config|mihomo-config|/data/mihomo' \
  'foxos-mosdns-runtime|mosdns-config|/cus/mosdns'; do
  if ! rg -Fq "$invariant" "$rsc_root/foxos-full-install.rsc"; then
    report "missing required mount contract: $invariant"
  fi
done

if ! rg -Fq 'containers foxos-mihomo, foxos-mosdns, foxos-active' "$rsc_root/foxos-plan.rsc"; then
  report "the read-only plan does not name the installed container slots exactly"
fi
if rg -Fq '现有 foxos-env 的键数量不是安全基线要求的 12 项' "$rsc_root/foxos-full-install.rsc"; then
  report "the installer rejects recoverable partial FoxOS env creation"
fi
for invariant in \
  'if ([:len $routerPasswordID] = 0)' \
  'if ([:len $mihomoSecretID] = 0)' \
  'if ([:len $apiTokenID] = 0)' \
  'if ([:len $confirmationKeyID] = 0)' \
  'foxos-env 包含安全基线之外的键'; do
  if ! rg -Fq "$invariant" "$rsc_root/foxos-full-install.rsc"; then
    report "missing resumable credential invariant: $invariant"
  fi
done

if ((failed != 0)); then
  exit 1
fi
printf 'RouterOS static checks passed (%s scripts).\n' "$(find "$rsc_root" -maxdepth 1 -type f -name '*.rsc' | wc -l | tr -d ' ')"
