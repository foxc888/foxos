#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
publisher="${script_dir}/publish-release-assets.sh"
fake_sha=0123456789abcdef0123456789abcdef01234567
moved_sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
test_root=$(mktemp -d "${TMPDIR:-/tmp}/foxos-release-publisher-test.XXXXXX")
trap 'rm -rf -- "$test_root"' EXIT

fail() {
  printf 'release publisher behavior test failed: %s\n' "$1" >&2
  exit 1
}

fake_assets_json() {
  local response='[]'
  local asset name size digest
  for asset in "$FAKE_PUBLISH_ASSET_DIR"/*; do
    [[ -f "$asset" ]] || continue
    name=${asset##*/}
    size=$(wc -c < "$asset" | tr -d '[:space:]')
    digest="sha256:$(sha256sum "$asset" | awk '{print $1}')"
    response=$(jq -c \
      --arg name "$name" \
      --arg size "$size" \
      --arg digest "$digest" \
      --arg url "https://downloads.github.test/${name}" \
      '. + [{name: $name, size: ($size | tonumber), digest: $digest, state: "uploaded", browser_download_url: $url}]' \
      <<< "$response")
  done
  printf '%s\n' "$response"
}

git() {
  local count_file count
  case "$*" in
    "ls-remote origin refs/tags/${GITHUB_REF_NAME}^{}")
      count_file="$FAKE_PUBLISH_ROOT/tag-read-count"
      count=0
      [[ ! -f "$count_file" ]] || count=$(<"$count_file")
      count=$((count + 1))
      printf '%s\n' "$count" > "$count_file"
      if [[ "$FAKE_SCENARIO" == publisher-cleanup-tag-moved && "$count" -ge 2 ]]; then
        printf '%s\trefs/tags/%s^{}\n' "$moved_sha" "$GITHUB_REF_NAME"
      else
        printf '%s\trefs/tags/%s^{}\n' "$fake_sha" "$GITHUB_REF_NAME"
      fi
      ;;
    "ls-remote --refs origin refs/tags/${GITHUB_REF_NAME}")
      printf '%s\trefs/tags/%s\n' "$fake_sha" "$GITHUB_REF_NAME"
      ;;
    *)
      printf 'unexpected fake publisher git invocation: %s\n' "$*" >&2
      return 64
      ;;
  esac
}

curl() {
  local data='' data_binary='' output='' request=GET url='' write_out=''
  local authorization=false
  while (($# > 0)); do
    case "$1" in
      --request)
        (($# >= 2)) || return 64
        request=$2
        shift 2
        ;;
      --data)
        (($# >= 2)) || return 64
        data=$2
        shift 2
        ;;
      --data-binary)
        (($# >= 2)) || return 64
        data_binary=$2
        shift 2
        ;;
      --header)
        (($# >= 2)) || return 64
        [[ "$2" != Authorization:* ]] || authorization=true
        shift 2
        ;;
      --output)
        (($# >= 2)) || return 64
        output=$2
        shift 2
        ;;
      --write-out)
        (($# >= 2)) || return 64
        write_out=$2
        shift 2
        ;;
      --connect-timeout|--max-time|--data-urlencode)
        (($# >= 2)) || return 64
        shift 2
        ;;
      --get)
        request=GET
        shift
        ;;
      http://*|https://*)
        url=$1
        shift
        ;;
      *)
        shift
        ;;
    esac
  done
  [[ -n "$url" ]] || return 64
  printf '%s|%s|authorization=%s\n' "$request" "$url" "$authorization" >> "$FAKE_PUBLISH_ROOT/requests.log"

  if [[ "$url" == https://downloads.github.test/* ]]; then
    [[ "$authorization" == false && -n "$output" ]] || return 64
    local download_name=${url##*/}
    cp -- "$FAKE_PUBLISH_ASSET_DIR/$download_name" "$output"
    return 0
  fi

  if [[ "$request" == GET && "$url" == *'/releases?'* ]]; then
    printf '%s\n' '[]'
    return 0
  fi
  if [[ "$request" == POST && "$url" == */releases ]]; then
    printf '%s\n' "$data" > "$FAKE_PUBLISH_ROOT/create-payload.json"
    local create_prerelease=false
    [[ "$GITHUB_REF_NAME" != *-rc.* ]] || create_prerelease=true
    jq -cn \
      --arg tag "$GITHUB_REF_NAME" \
      --argjson prerelease "$create_prerelease" \
      '{id: 42, upload_url: "https://uploads.github.test/releases/42/assets{?name,label}", draft: true, tag_name: $tag, prerelease: $prerelease}'
    return 0
  fi
  if [[ "$request" == POST && "$url" == https://uploads.github.test/releases/42/assets* ]]; then
    local asset=${data_binary#@}
    local name=${url##*name=}
    [[ -f "$asset" && "${asset##*/}" == "$name" ]] || return 64
    local size digest
    size=$(wc -c < "$asset" | tr -d '[:space:]')
    digest="sha256:$(sha256sum "$asset" | awk '{print $1}')"
    jq -cn --arg name "$name" --arg size "$size" --arg digest "$digest" \
      '{name: $name, size: ($size | tonumber), state: "uploaded", digest: $digest}'
    return 0
  fi
  if [[ "$request" == GET && "$url" == *'/releases/42/assets?'* ]]; then
    fake_assets_json
    return 0
  fi
  if [[ "$request" == PATCH && "$url" == */releases/42 ]]; then
    printf '%s\n' "$data" > "$FAKE_PUBLISH_ROOT/finalize-payload.json"
    if [[ "$FAKE_SCENARIO" != publisher-success ]]; then
      return 22
    fi
    local final_prerelease=false
    [[ "$GITHUB_REF_NAME" != *-rc.* ]] || final_prerelease=true
    jq -cn \
      --arg tag "$GITHUB_REF_NAME" \
      --argjson prerelease "$final_prerelease" \
      '{id: 42, draft: false, immutable: true, tag_name: $tag, prerelease: $prerelease, html_url: "https://github.test/foxc888/foxos/releases/42"}'
    return 0
  fi
  if [[ "$request" == GET && "$url" == */releases/latest ]]; then
    if [[ "$GITHUB_REF_NAME" == *-rc.* ]]; then
      printf '{}\n404'
    else
      printf '{"id":42}\n200'
    fi
    return 0
  fi
  if [[ "$request" == GET && "$url" == */releases/42 ]]; then
    local state_id=42 state_draft=true state_immutable=false
    if [[ "$FAKE_SCENARIO" == publisher-success || "$FAKE_SCENARIO" == publisher-cleanup-published ]]; then
      state_draft=false
      state_immutable=true
    elif [[ "$FAKE_SCENARIO" == publisher-cleanup-id-mismatch ]]; then
      state_id=99
    fi
    local state_prerelease=false
    [[ "$GITHUB_REF_NAME" != *-rc.* ]] || state_prerelease=true
    jq -cn \
      --arg id "$state_id" \
      --arg tag "$GITHUB_REF_NAME" \
      --argjson draft "$state_draft" \
      --argjson immutable "$state_immutable" \
      --argjson prerelease "$state_prerelease" \
      '{id: ($id | tonumber), draft: $draft, immutable: $immutable, tag_name: $tag, prerelease: $prerelease, html_url: "https://github.test/foxc888/foxos/releases/42"}'
    return 0
  fi
  if [[ "$request" == DELETE && "$url" == */releases/42 ]]; then
    return 0
  fi

  printf 'unexpected fake publisher curl request: %s %s write-out=%s\n' "$request" "$url" "$write_out" >&2
  return 64
}

export -f git curl fake_assets_json
export fake_sha moved_sha

prepare_case() {
  local case_root=$1
  local tag=$2
  local asset_dir="$case_root/assets"
  local bundle="foxos-full-amd64-${tag}.tar.gz"
  mkdir -p -- "$asset_dir"
  printf 'fake RouterOS bundle for %s\n' "$tag" > "$asset_dir/$bundle"
  local digest
  digest=$(sha256sum "$asset_dir/$bundle" | awk '{print $1}')
  printf '%s  %s\n' "$digest" "$bundle" > "$asset_dir/${bundle}.sha256"
}

run_publisher() {
  local scenario=$1
  local tag=$2
  local case_root=$3
  FAKE_SCENARIO=$scenario \
  FAKE_PUBLISH_ROOT=$case_root \
  FAKE_PUBLISH_ASSET_DIR="$case_root/assets" \
  GITHUB_API_URL=https://api.github.test \
  GITHUB_REF="refs/tags/${tag}" \
  GITHUB_REF_NAME="$tag" \
  GITHUB_REF_TYPE=tag \
  GITHUB_REPOSITORY=foxc888/foxos \
  GITHUB_SHA="$fake_sha" \
  GH_TOKEN=fake-token \
    bash "$publisher" "$case_root/assets"
}

assert_payloads() {
  local case_root=$1
  local tag=$2
  local prerelease=$3
  local make_latest=$4
  jq -e --arg tag "$tag" --argjson prerelease "$prerelease" '
    (has("target_commitish") | not)
    and .tag_name == $tag
    and .name == $tag
    and .body == ""
    and .draft == true
    and .prerelease == $prerelease
    and .generate_release_notes == true
    and .make_latest == "false"
    and ((keys | sort) == (["body", "draft", "generate_release_notes", "make_latest", "name", "prerelease", "tag_name"] | sort))
  ' "$case_root/create-payload.json" >/dev/null || fail "Create Release payload changed for $tag"
  jq -e --argjson prerelease "$prerelease" --arg make_latest "$make_latest" '
    .draft == false
    and .prerelease == $prerelease
    and .make_latest == $make_latest
    and ((keys | sort) == (["draft", "make_latest", "prerelease"] | sort))
  ' "$case_root/finalize-payload.json" >/dev/null || fail "Update Release payload changed for $tag"
}

for success_case in 'v1.2.3-rc.4|true|false' 'v1.2.3|false|true'; do
  IFS='|' read -r tag prerelease make_latest <<< "$success_case"
  case_root="$test_root/success-${tag}"
  mkdir -p -- "$case_root"
  prepare_case "$case_root" "$tag"
  run_publisher publisher-success "$tag" "$case_root" > "$case_root/output.log" \
    || fail "publisher rejected complete happy path for $tag"
  assert_payloads "$case_root" "$tag" "$prerelease" "$make_latest"
  rg -Fq "PATCH|https://api.github.test/repos/foxc888/foxos/releases/42|authorization=true" "$case_root/requests.log" \
    || fail "publisher did not finalize $tag with an authenticated PATCH"
  [[ "$(rg -c '^GET\|https://downloads\.github\.test/.*\|authorization=false$' "$case_root/requests.log")" == 2 ]] \
    || fail "publisher did not anonymously download both RouterOS bundle assets for $tag"
  ! rg -q '^GET\|https://downloads\.github\.test/.*\|authorization=true$' "$case_root/requests.log" \
    || fail "publisher leaked authorization to an anonymous asset download for $tag"
  rg -Fq "Published immutable release ${tag}" "$case_root/output.log" \
    || fail "publisher did not report the immutable release for $tag"
done

for cleanup_case in \
  'publisher-finalize-denied|delete' \
  'publisher-cleanup-tag-moved|retain' \
  'publisher-cleanup-published|retain' \
  'publisher-cleanup-id-mismatch|retain'; do
  IFS='|' read -r scenario cleanup_expectation <<< "$cleanup_case"
  tag=v1.2.3-rc.4
  case_root="$test_root/$scenario"
  mkdir -p -- "$case_root"
  prepare_case "$case_root" "$tag"
  if run_publisher "$scenario" "$tag" "$case_root" > "$case_root/output.log" 2>&1; then
    fail "publisher unexpectedly survived finalize failure scenario $scenario"
  fi
  assert_payloads "$case_root" "$tag" true false
  if [[ "$cleanup_expectation" == delete ]]; then
    rg -Fq 'DELETE|https://api.github.test/repos/foxc888/foxos/releases/42|authorization=true' "$case_root/requests.log" \
      || fail "publisher did not delete its exact still-draft release after $scenario"
  elif rg -Fq 'DELETE|https://api.github.test/repos/foxc888/foxos/releases/42' "$case_root/requests.log"; then
    fail "publisher deleted a release it no longer exclusively owned after $scenario"
  fi
done

printf 'Release publisher behavior checks passed.\n'
