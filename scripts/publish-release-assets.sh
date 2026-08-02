#!/usr/bin/env bash
set -Eeuo pipefail

semver_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$'

fail() {
  printf 'release publish failed: %s\n' "$1" >&2
  exit 1
}

classify_tag() {
  local tag=$1
  [[ "$tag" =~ $semver_pattern ]] || return 1
  if [[ "$tag" == *-rc.* ]]; then
    printf '%s %s\n' true false
  else
    printf '%s %s\n' false true
  fi
}

if [[ "${1:-}" == --classify-tag ]]; then
  [[ $# == 2 ]] || fail 'usage: publish-release-assets.sh --classify-tag <tag>'
  read -r prerelease make_latest < <(classify_tag "$2") || fail 'invalid release tag'
  printf 'prerelease=%s\nmake_latest=%s\n' "$prerelease" "$make_latest"
  exit 0
fi

asset_dir=${1:-release-assets}
for variable in GITHUB_API_URL GITHUB_REF GITHUB_REF_NAME GITHUB_REF_TYPE GITHUB_REPOSITORY GITHUB_SHA GH_TOKEN; do
  [[ -n "${!variable:-}" ]] || fail "required environment is empty: $variable"
done
for command in awk curl find git jq mktemp rm sha256sum sleep sort tr wc; do
  command -v "$command" >/dev/null 2>&1 || fail "required command is unavailable: $command"
done
[[ "$GITHUB_REF" == "refs/tags/${GITHUB_REF_NAME}" && "$GITHUB_REF_TYPE" == tag ]] || fail 'publisher requires a tag push context'
read -r prerelease make_latest < <(classify_tag "$GITHUB_REF_NAME") || fail 'invalid release tag'
[[ -d "$asset_dir" ]] || fail "asset directory does not exist: $asset_dir"

api_get() {
  curl --fail --silent --show-error \
    --header "Authorization: Bearer ${GH_TOKEN}" \
    --header 'Accept: application/vnd.github+json' \
    --header 'X-GitHub-Api-Version: 2022-11-28' \
    "$1"
}

remote_tag_sha() {
  local resolved
  resolved=$(git ls-remote origin "refs/tags/${GITHUB_REF_NAME}^{}" | awk 'NR == 1 { print $1 }')
  if [[ -z "$resolved" ]]; then
    resolved=$(git ls-remote --refs origin "refs/tags/${GITHUB_REF_NAME}" | awk 'NR == 1 { print $1 }')
  fi
  printf '%s\n' "$resolved"
}

require_release_absent() {
  local page=1
  local response length matches
  while ((page <= 100)); do
    response=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/releases?per_page=100&page=${page}")
    length=$(jq -er 'if type == "array" then length else error("unexpected response") end' <<< "$response")
    matches=$(jq -er --arg tag "$GITHUB_REF_NAME" '[.[] | select(.tag_name == $tag)] | length' <<< "$response")
    [[ "$matches" == 0 ]] || fail "a draft or published release already uses tag ${GITHUB_REF_NAME}"
    ((length < 100)) && return 0
    ((page += 1))
  done
  fail 'release absence could not be proven within 100 API pages'
}

release_id=""
draft_owned=false
verification_dir=""
cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n "$verification_dir" && -d "$verification_dir" ]]; then
    rm -rf -- "$verification_dir"
  fi
  if ((status != 0)) && [[ "$draft_owned" == true && "$release_id" =~ ^[1-9][0-9]*$ ]]; then
    local current
    current=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/releases/${release_id}" 2>/dev/null || true)
    if jq -e --arg id "$release_id" --arg tag "$GITHUB_REF_NAME" \
      '.id == ($id | tonumber) and .draft == true and .tag_name == $tag' \
      <<< "$current" >/dev/null 2>&1 && [[ "$(remote_tag_sha)" == "$GITHUB_SHA" ]]; then
      curl --fail --silent --show-error --request DELETE \
        --header "Authorization: Bearer ${GH_TOKEN}" \
        --header 'Accept: application/vnd.github+json' \
        --header 'X-GitHub-Api-Version: 2022-11-28' \
        "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/releases/${release_id}" >/dev/null || true
    fi
  fi
  exit "$status"
}
trap cleanup EXIT

[[ "$(remote_tag_sha)" == "$GITHUB_SHA" ]] || fail 'remote release tag does not resolve to GITHUB_SHA'
require_release_absent

create_payload=$(jq -cn \
  --arg tag "$GITHUB_REF_NAME" \
  --argjson prerelease "$prerelease" \
  '{tag_name: $tag, name: $tag, body: "", draft: true, prerelease: $prerelease, generate_release_notes: true, make_latest: "false"}')
create_response=$(curl --fail --silent --show-error --request POST \
  --header "Authorization: Bearer ${GH_TOKEN}" \
  --header 'Accept: application/vnd.github+json' \
  --header 'Content-Type: application/json' \
  --header 'X-GitHub-Api-Version: 2022-11-28' \
  --data "$create_payload" \
  "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/releases")
release_id=$(jq -er '.id | select(type == "number" and . > 0)' <<< "$create_response")
draft_owned=true
upload_url=$(jq -er '.upload_url | select(type == "string" and length > 0) | sub("\\{.*$"; "")' <<< "$create_response")
jq -e \
  --arg tag "$GITHUB_REF_NAME" \
  --argjson prerelease "$prerelease" \
  '.draft == true and .tag_name == $tag and .prerelease == $prerelease' \
  <<< "$create_response" >/dev/null || fail 'created draft identity does not match the release context'

assets=()
while IFS= read -r -d '' asset; do
  assets[${#assets[@]}]=$asset
done < <(find "$asset_dir" -mindepth 1 -maxdepth 1 -type f -print0 | sort -z)
(( ${#assets[@]} > 0 && ${#assets[@]} <= 100 )) || fail 'release must contain between 1 and 100 regular-file assets'
expected_assets='[]'
for asset in "${assets[@]}"; do
  name=${asset##*/}
  [[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || fail "unsafe release asset name: $name"
  size=$(wc -c < "$asset" | tr -d '[:space:]')
  sha256=$(sha256sum "$asset" | awk '{print $1}')
  encoded_name=$(jq -nr --arg value "$name" '$value | @uri')
  upload_response=$(curl --fail --silent --show-error --request POST \
    --header "Authorization: Bearer ${GH_TOKEN}" \
    --header 'Accept: application/vnd.github+json' \
    --header 'Content-Type: application/octet-stream' \
    --header 'X-GitHub-Api-Version: 2022-11-28' \
    --data-binary "@${asset}" \
    "${upload_url}?name=${encoded_name}")
  jq -e \
    --arg name "$name" \
    --arg size "$size" \
    --arg digest "sha256:${sha256}" \
    '.name == $name and .size == ($size | tonumber) and .state == "uploaded" and .digest == $digest' \
    <<< "$upload_response" >/dev/null || fail "uploaded asset identity mismatch: $name"
  expected_assets=$(jq -c \
    --arg name "$name" \
    --arg size "$size" \
    --arg digest "sha256:${sha256}" \
    '. + [{name: $name, size: ($size | tonumber), digest: $digest}]' <<< "$expected_assets")
done

remote_assets=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/releases/${release_id}/assets?per_page=100")
jq -e --argjson expected "$expected_assets" '
  def normalized: map({name, size, digest}) | sort_by(.name);
  type == "array" and normalized == ($expected | normalized)
' <<< "$remote_assets" >/dev/null || fail 'draft release asset allowlist or digest readback mismatch'

finalize_payload=$(jq -cn \
  --argjson prerelease "$prerelease" \
  --arg make_latest "$make_latest" \
  '{draft: false, prerelease: $prerelease, make_latest: $make_latest}')
finalize_response=$(curl --fail --silent --show-error --request PATCH \
  --header "Authorization: Bearer ${GH_TOKEN}" \
  --header 'Accept: application/vnd.github+json' \
  --header 'Content-Type: application/json' \
  --header 'X-GitHub-Api-Version: 2022-11-28' \
  --data "$finalize_payload" \
  "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/releases/${release_id}")
jq -e \
  --arg id "$release_id" \
  --arg tag "$GITHUB_REF_NAME" \
  --argjson prerelease "$prerelease" \
  '.id == ($id | tonumber) and .draft == false and .tag_name == $tag and .prerelease == $prerelease' \
  <<< "$finalize_response" >/dev/null || fail 'finalized release identity mismatch'
draft_owned=false

published=""
published_assets=""
for attempt in 1 2 3 4 5 6 7 8; do
  candidate_release=""
  candidate_assets=""
  if candidate_release=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/releases/${release_id}") \
    && candidate_assets=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/releases/${release_id}/assets?per_page=100") \
    && jq -e \
      --arg id "$release_id" \
      --arg tag "$GITHUB_REF_NAME" \
      --argjson prerelease "$prerelease" \
      '.id == ($id | tonumber) and .draft == false and .immutable == true and .tag_name == $tag and .prerelease == $prerelease' \
      <<< "$candidate_release" >/dev/null \
    && jq -e --argjson expected "$expected_assets" '
      def normalized: map({name, size, digest}) | sort_by(.name);
      type == "array" and normalized == ($expected | normalized)
    ' <<< "$candidate_assets" >/dev/null; then
    published=$candidate_release
    published_assets=$candidate_assets
    break
  fi
  sleep "$attempt"
done
[[ -n "$published" && -n "$published_assets" ]] \
  || fail "published release ${release_id} metadata did not converge; quarantine it before using a new tag"
[[ "$(remote_tag_sha)" == "$GITHUB_SHA" ]] || fail 'release tag moved before published postcondition'

latest_verified=false
for attempt in 1 2 3 4 5 6 7 8; do
  latest_with_status=$(curl --silent --show-error \
    --header "Authorization: Bearer ${GH_TOKEN}" \
    --header 'Accept: application/vnd.github+json' \
    --header 'X-GitHub-Api-Version: 2022-11-28' \
    --write-out $'\n%{http_code}' \
    "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/releases/latest" || true)
  latest_status=${latest_with_status##*$'\n'}
  latest_body=${latest_with_status%$'\n'*}
  if [[ "$prerelease" == true ]]; then
    if [[ "$latest_status" == 404 ]] \
      || { [[ "$latest_status" == 200 ]] && ! jq -e --arg id "$release_id" '.id == ($id | tonumber)' <<< "$latest_body" >/dev/null; }; then
      latest_verified=true
      break
    fi
  elif [[ "$latest_status" == 200 ]] \
    && jq -e --arg id "$release_id" '.id == ($id | tonumber)' <<< "$latest_body" >/dev/null; then
    latest_verified=true
    break
  fi
  sleep "$attempt"
done
[[ "$latest_verified" == true ]] \
  || fail "published release ${release_id} did not reach the required prerelease/latest state; quarantine it before retrying"

release_asset_id=${GITHUB_REF_NAME//[^A-Za-z0-9._-]/-}
bundle_name="foxos-full-amd64-${release_asset_id}.tar.gz"
checksum_name="${bundle_name}.sha256"
verification_dir=$(mktemp -d "${RUNNER_TEMP:-/tmp}/foxos-release-download.XXXXXX")
for asset_name in "$bundle_name" "$checksum_name"; do
  download_url=$(jq -er --arg name "$asset_name" '
    [.[] | select(.name == $name and (.browser_download_url | type == "string"))]
    | if length == 1 then .[0].browser_download_url else error("asset download URL is not unique") end
  ' <<< "$published_assets") || fail "published asset has no unique download URL: ${asset_name}"
  [[ "$download_url" == https://* ]] || fail "published asset download URL is not HTTPS: ${asset_name}"
  downloaded=false
  for attempt in 1 2 3 4 5 6; do
    if curl --fail --location --silent --show-error \
      --connect-timeout 15 \
      --max-time 300 \
      --output "${verification_dir}/${asset_name}" \
      "$download_url"; then
      downloaded=true
      break
    fi
    sleep "$attempt"
  done
  [[ "$downloaded" == true ]] \
    || fail "published release ${release_id} asset is not anonymously downloadable: ${asset_name}"

  expected_size=$(jq -er --arg name "$asset_name" '.[] | select(.name == $name) | .size' <<< "$expected_assets")
  expected_digest=$(jq -er --arg name "$asset_name" '.[] | select(.name == $name) | .digest' <<< "$expected_assets")
  actual_size=$(wc -c < "${verification_dir}/${asset_name}" | tr -d '[:space:]')
  actual_digest="sha256:$(sha256sum "${verification_dir}/${asset_name}" | awk '{print $1}')"
  [[ "$actual_size" == "$expected_size" && "$actual_digest" == "$expected_digest" ]] \
    || fail "anonymous download identity mismatch: ${asset_name}"
done
(cd "$verification_dir" && sha256sum --check "$checksum_name") \
  || fail "anonymous RouterOS bundle checksum verification failed for release ${release_id}"

release_url=$(jq -er '.html_url | select(type == "string" and length > 0)' <<< "$published")
printf 'Published immutable release %s with %s verified assets and an anonymously verified RouterOS bundle: %s\n' "$GITHUB_REF_NAME" "${#assets[@]}" "$release_url"
