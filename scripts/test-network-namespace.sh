#!/usr/bin/env bash
set -Eeuo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  printf 'network namespace test requires Linux\n' >&2
  exit 2
fi
if ((EUID != 0)); then
  printf 'network namespace test must run as root\n' >&2
  exit 2
fi

for command in go ip curl setcap setpriv ping awk grep wc tr head unshare mount nsenter findmnt sed; do
  command -v "$command" >/dev/null 2>&1 || {
    printf 'network namespace test requires %s\n' "$command" >&2
    exit 2
  }
done

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work_root=$(mktemp -d "${TMPDIR:-/tmp}/foxos-netns.XXXXXX")
suffix=${work_root##*.}
suffix=${suffix:0:5}
router_ns="fxr-$suffix"
client_ns="fxc-$suffix"
foxos_ns="fxf-$suffix"
wan_ns="fxw-$suffix"
server_pid=""
api_token=nsApi_7vQ4mK9xP2cR8tW5yH3dF6jL1sB0uE9aC2gZ4n
confirmation_key=nsConfirm_3pT8wY1kH6rD9sF2mV5xC7qL0bN4jG8u
routeros_password=nsRouter_8rW2mJ5cT9yK4pL7sD1fH6vQ3xN0bE
mihomo_secret=nsMihomo_6qC9tR2wY5kP8dF1hJ4mV7xB3sL0nG

cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  ip netns delete "$client_ns" 2>/dev/null || true
  ip netns delete "$foxos_ns" 2>/dev/null || true
  ip netns delete "$wan_ns" 2>/dev/null || true
  ip netns delete "$router_ns" 2>/dev/null || true
  rm -rf -- "$work_root"
}
trap cleanup EXIT

fail() {
  printf 'network namespace test failed: %s\n' "$1" >&2
  if [[ -s "$work_root/foxos.log" ]]; then
    tail -80 "$work_root/foxos.log" | sed \
      -e "s/${api_token}/[REDACTED]/g" \
      -e "s/${confirmation_key}/[REDACTED]/g" \
      -e "s/${routeros_password}/[REDACTED]/g" \
      -e "s/${mihomo_secret}/[REDACTED]/g" >&2
  fi
  exit 1
}

ip netns add "$router_ns"
ip netns add "$client_ns"
ip netns add "$foxos_ns"
ip netns add "$wan_ns"
for namespace in "$router_ns" "$client_ns" "$foxos_ns" "$wan_ns"; do
  ip -n "$namespace" link set lo up
done

ip -n "$router_ns" link add br-lan type bridge
ip -n "$router_ns" address add 10.77.0.1/24 dev br-lan
ip -n "$router_ns" link set br-lan up

connect_lan() {
  local namespace=$1
  local address=$2
  local stem=$3
  local router_link="${stem}r"
  local namespace_link="${stem}n"
  ip link add "$router_link" type veth peer name "$namespace_link"
  ip link set "$router_link" netns "$router_ns"
  ip link set "$namespace_link" netns "$namespace"
  ip -n "$router_ns" link set "$router_link" master br-lan
  ip -n "$router_ns" link set "$router_link" up
  ip -n "$namespace" address add "$address" dev "$namespace_link"
  ip -n "$namespace" link set "$namespace_link" up
  ip -n "$namespace" route add default via 10.77.0.1
}

connect_lan "$client_ns" 10.77.0.10/24 "c${suffix}"
connect_lan "$foxos_ns" 10.77.0.4/24 "f${suffix}"

ip link add "wr${suffix}" type veth peer name "wn${suffix}"
ip link set "wr${suffix}" netns "$router_ns"
ip link set "wn${suffix}" netns "$wan_ns"
ip -n "$router_ns" address add 198.18.0.1/24 dev "wr${suffix}"
ip -n "$router_ns" link set "wr${suffix}" up
ip -n "$wan_ns" address add 198.18.0.2/24 dev "wn${suffix}"
ip -n "$wan_ns" link set "wn${suffix}" up
ip -n "$wan_ns" route add 10.77.0.0/24 via 198.18.0.1
ip netns exec "$router_ns" sysctl -q -w net.ipv4.ip_forward=1 >/dev/null

binary="$work_root/foxos"
static_dir="$work_root/web"
data_dir="$work_root/data"
tls_dir="$data_dir/tls"
secret_dir="$work_root/secrets"
mkdir -p "$static_dir" "$data_dir" "$work_root/backups" "$secret_dir"
printf '%s' "$api_token" > "$secret_dir/api-token"
printf '%s' "$confirmation_key" > "$secret_dir/confirmation-key"
printf '%s' "$routeros_password" > "$secret_dir/routeros-password"
printf '%s' "$mihomo_secret" > "$secret_dir/mihomo-secret"
chmod 0600 "$secret_dir"/*
[[ -s "$repo_root/web/dist/index.html" ]] || fail "web/dist is missing; build the production frontend before namespace acceptance"
cp -a "$repo_root/web/dist/." "$static_dir/"
(cd "$repo_root" && CGO_ENABLED=0 go build -trimpath -o "$binary" ./cmd/server)
chown -R 65534:65534 "$work_root"
setcap cap_net_bind_service=+ep "$binary"

ip netns exec "$foxos_ns" unshare --mount /bin/sh -c '
  set -eu
  secret_source=$1
  shift
  mount --make-rprivate /
  mount -t tmpfs -o mode=0755,nosuid,nodev tmpfs /run
  mkdir -p /run/secrets/foxos
  mount --bind "$secret_source" /run/secrets/foxos
  mount -o remount,bind,ro /run/secrets/foxos
  exec "$@"
' foxos-secret-mount "$secret_dir" \
  setpriv --reuid=65534 --regid=65534 --clear-groups env \
  FOXOS_ENV=production \
  FOXOS_BACKUP_DIR="$work_root/backups" \
  FOXOS_UPGRADE_STATE_PATH="$data_dir/upgrade-checkpoint.json" \
  FOXOS_SITE_MANAGEMENT_BRIDGE=lab-lan \
  FOXOS_SITE_STORAGE_ROOT=lab-storage \
  FOXOS_SITE_NETWORK=10.77.0.0/24 \
  FOXOS_SITE_ROUTER_ADDRESS=10.77.0.1 \
  FOXOS_SITE_MIHOMO_ADDRESS=10.77.0.2 \
  FOXOS_SITE_MOSDNS_ADDRESS=10.77.0.3 \
  FOXOS_SITE_FOXOS_ADDRESS=10.77.0.4 \
  FOXOS_SITE_PUBLIC_HOSTNAME=foxos.home.arpa \
  FOXOS_HTTPS_ENABLED=true \
  FOXOS_TLS_DIR="$tls_dir" \
  FOXOS_INTERNAL_LISTEN=127.0.0.1:8090 \
  FOXOS_HTTPS_LISTEN=0.0.0.0:443 \
  FOXOS_HTTP_REDIRECT_LISTEN=0.0.0.0:80 \
  "$binary" -static "$static_dir" -database "$data_dir/foxos.db" \
  >"$work_root/foxos.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 60); do
  [[ -s "$tls_dir/foxos-local-ca.pem" ]] && \
    ip netns exec "$client_ns" curl --noproxy '*' -fsS --connect-timeout 1 \
      --resolve foxos.home.arpa:443:10.77.0.4 \
      --cacert "$tls_dir/foxos-local-ca.pem" \
      https://foxos.home.arpa/api/v1/health/live >/dev/null 2>&1 && break
  sleep 0.25
done
kill -0 "$server_pid" 2>/dev/null || fail "FoxOS exited before acceptance"
[[ -s "$tls_dir/foxos-local-ca.pem" ]] || fail "local CA was not generated"

uid=$(awk '/^Uid:/ {print $2}' "/proc/$server_pid/status")
[[ "$uid" != "0" ]] || fail "FoxOS is running as root"
if tr '\0' '\n' < "/proc/$server_pid/environ" | grep -Eq '^FOXOS_(API_TOKEN|CONFIRMATION_KEY|ROUTEROS_PASSWORD|MIHOMO_SECRET)='; then
  fail "production process environment contains a forbidden secret key"
fi
mount_options=$(nsenter --target "$server_pid" --mount findmnt -no OPTIONS /run/secrets/foxos)
grep -Eq '(^|,)ro(,|$)' <<<"$mount_options" || fail "production secret mount is not read-only"
for secret_value in "$api_token" "$confirmation_key" "$routeros_password" "$mihomo_secret"; do
  if grep -Fq "$secret_value" "$work_root/foxos.log"; then
    fail "production log contains a secret value"
  fi
done
if grep -Eq 'FOXOS_(API_TOKEN|CONFIRMATION_KEY|ROUTEROS_PASSWORD|MIHOMO_SECRET)' "$work_root/foxos.log"; then
  fail "production log contains a secret key name"
fi

curl_from_client() {
  ip netns exec "$client_ns" curl --noproxy '*' -fsS \
    --resolve foxos.home.arpa:443:10.77.0.4 \
    --cacert "$tls_dir/foxos-local-ca.pem" \
    "$@"
}

live=$(curl_from_client https://foxos.home.arpa/api/v1/health/live)
grep -Fq '"status":"ok"' <<<"$live" || fail "live endpoint did not pass"
ready=$(curl_from_client https://foxos.home.arpa/api/v1/health/ready)
grep -Fq '"status":"ready"' <<<"$ready" || fail "ready endpoint did not pass"
page=$(curl_from_client https://foxos.home.arpa/)
grep -Fq 'id="root"' <<<"$page" || fail "Web page did not pass"
asset_path=$(grep -oE '/assets/[^" ]+\.js' <<<"$page" | head -1)
[[ -n "$asset_path" ]] || fail "production page did not reference a JavaScript asset"
asset_size=$(curl_from_client "https://foxos.home.arpa${asset_path}" | wc -c | tr -d ' ')
((asset_size > 1000)) || fail "production JavaScript asset was empty or truncated"
site=$(curl_from_client https://foxos.home.arpa/api/v1/site)
grep -Fq '"network":"10.77.0.0/24"' <<<"$site" || fail "site manifest did not match the namespace"
grep -Fq '"publicUrl":"https://foxos.home.arpa"' <<<"$site" || fail "site manifest did not advertise HTTPS"

capabilities=$(curl_from_client -H "Authorization: Bearer $api_token" https://foxos.home.arpa/api/v1/egress/capabilities)
grep -Fq '"routerosConfigured":false' <<<"$capabilities" || fail "RouterOS readiness was overstated"
grep -Fq '"mode":"mihomo-node","available":false' <<<"$capabilities" || fail "Mihomo egress was overstated"
grep -Fq '"mode":"proxy-chain","available":false' <<<"$capabilities" || fail "proxy-chain egress was overstated"

headers="$work_root/headers"
ip netns exec "$client_ns" curl --noproxy '*' -sS -D "$headers" -o /dev/null "http://10.77.0.4/path?q=1"
grep -Eq '^HTTP/[0-9.]+ 308' "$headers" || fail "HTTP did not return 308"
grep -Fiq 'location: https://foxos.home.arpa/path?q=1' "$headers" || fail "redirect target was not canonical"

ip netns exec "$client_ns" curl --noproxy '*' -fsS --cacert "$tls_dir/foxos-local-ca.pem" https://10.77.0.4/api/v1/health/live >/dev/null || fail "certificate IP SAN did not validate"
if ip netns exec "$client_ns" curl --noproxy '*' -fsS --connect-timeout 1 http://10.77.0.4:8090/api/v1/health/live >/dev/null 2>&1; then
  fail "loopback management listener was reachable from the LAN"
fi

spoof_status=$(ip netns exec "$foxos_ns" curl --noproxy '*' -sS -o "$work_root/spoof.json" -w '%{http_code}' \
  -H "Authorization: Bearer $api_token" \
  -H 'X-Forwarded-Proto: https' \
  -H 'X-Foxos-Internal-Gateway: attacker' \
  http://127.0.0.1:8090/api/v1/nodes)
[[ "$spoof_status" == "426" ]] || fail "forged proxy headers bypassed secure transport"

ip netns exec "$client_ns" ping -c 2 -W 1 198.18.0.2 >/dev/null || fail "routed return path failed"
management_route=$(ip -n "$client_ns" route get 10.77.0.4)
grep -Fq 'dev c' <<<"$management_route" || fail "management address did not stay on the LAN path"

printf 'Linux namespace acceptance passed: non-root HTTPS, redirect, CA/SAN, return path, management path, and fail-closed egress readiness.\n'
