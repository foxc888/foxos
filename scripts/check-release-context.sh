#!/usr/bin/env bash
set -Eeuo pipefail

release_branch=agent/foxos-core
branch_ref="refs/heads/${release_branch}"
default_branch=
default_branch_ref=
semver_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$'
release_ruleset_name='FoxOS immutable release tags'
release_tag_pattern='refs/tags/v*'
release_actor_id=137797974
release_environment=release
release_environment_pattern='v*'

fail() {
  printf 'release context check failed: %s\n' "$1" >&2
  exit 1
}

for command in curl git jq; do
  command -v "$command" >/dev/null 2>&1 || fail "required command is unavailable: $command"
done

for variable in GITHUB_API_URL GITHUB_EVENT_NAME GITHUB_REF GITHUB_REF_NAME GITHUB_REPOSITORY GITHUB_SHA GH_TOKEN; do
  [[ -n "${!variable:-}" ]] || fail "required environment is empty: $variable"
done

api_get() {
  curl --fail --silent --show-error \
    --header "Authorization: Bearer ${GH_TOKEN}" \
    --header 'Accept: application/vnd.github+json' \
    --header 'X-GitHub-Api-Version: 2022-11-28' \
    "$1"
}

repository_default_branch() {
  local repository branch branch_ref_candidate
  repository=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}")
  branch=$(jq -er '.default_branch | select(type == "string" and length > 0)' <<< "$repository") \
    || fail 'repository default branch is missing from GitHub metadata'
  branch_ref_candidate="refs/heads/${branch}"
  git check-ref-format "$branch_ref_candidate" >/dev/null 2>&1 \
    || fail "repository default branch is not a valid Git ref: ${branch}"
  printf '%s\n' "$branch"
}

require_release_tag_ruleset() {
  local page=1
  local response length rulesets ruleset_id ruleset
  rulesets='[]'
  while ((page <= 100)); do
    response=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/rulesets?includes_parents=false&targets=tag&per_page=100&page=${page}")
    length=$(jq -er 'if type == "array" then length else error("unexpected response") end' <<< "$response")
    rulesets=$(jq -cn --argjson current "$rulesets" --argjson next "$response" '$current + $next')
    ((length < 100)) && break
    ((page += 1))
  done
  ((page <= 100)) || fail 'tag ruleset identity could not be proven within 100 API pages'

  ruleset_id=$(jq -er --arg name "$release_ruleset_name" '
    [
      .[]
      | select(
          .name == $name
          and .target == "tag"
          and .source_type == "Repository"
          and .enforcement == "active"
        )
    ]
    | if length == 1 then .[0].id else error("expected exactly one active repository tag ruleset") end
  ' <<< "$rulesets") || fail "exactly one active repository tag ruleset named ${release_ruleset_name} is required"

  ruleset=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/rulesets/${ruleset_id}")
  jq -e \
    --arg name "$release_ruleset_name" \
    --arg repository "$GITHUB_REPOSITORY" \
    --arg pattern "$release_tag_pattern" \
    --arg actor_id "$release_actor_id" '
      .name == $name
      and .target == "tag"
      and .source_type == "Repository"
      and .source == $repository
      and .enforcement == "active"
      and .conditions.ref_name.include == [$pattern]
      and ((.conditions.ref_name.exclude // []) | length == 0)
      and (["creation", "update", "deletion", "non_fast_forward"] - [.rules[]?.type] | length == 0)
      and (.bypass_actors | length == 1)
      and .bypass_actors[0].actor_type == "User"
      and .bypass_actors[0].actor_id == ($actor_id | tonumber)
      and .bypass_actors[0].bypass_mode == "always"
    ' <<< "$ruleset" >/dev/null \
    || fail "${release_ruleset_name} must exclusively target ${release_tag_pattern}, restrict creation/update/deletion/non-fast-forward, and authorize only FoxOS release actor ${release_actor_id}"
}

require_immutable_releases() {
  local response
  response=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/immutable-releases")
  jq -e '.enabled == true' <<< "$response" >/dev/null \
    || fail 'GitHub immutable releases must be enabled for the repository'
}

require_release_environment() {
  local environment policies
  environment=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/environments/${release_environment}")
  jq -e --arg name "$release_environment" --arg actor_id "$release_actor_id" '
    .name == $name
    and any(
      .protection_rules[]?;
      .type == "required_reviewers"
      and .prevent_self_review == true
      and ((.reviewers // []) | length == 1)
      and .reviewers[0].type == "User"
      and ((.reviewers[0].reviewer.id | type) == "number")
      and .reviewers[0].reviewer.id > 0
      and .reviewers[0].reviewer.id != ($actor_id | tonumber)
    )
    and .deployment_branch_policy.protected_branches == false
    and .deployment_branch_policy.custom_branch_policies == true
  ' <<< "$environment" >/dev/null \
    || fail "${release_environment} environment must require exactly one independent reviewer, prevent self-review, and use selected deployment refs"

  policies=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/environments/${release_environment}/deployment-branch-policies?per_page=100")
  jq -e --arg pattern "$release_environment_pattern" '
    .total_count == 1
    and (.branch_policies | length == 1)
    and .branch_policies[0].name == $pattern
    and .branch_policies[0].type == "tag"
  ' <<< "$policies" >/dev/null \
    || fail "${release_environment} environment must allow exactly the ${release_environment_pattern} deployment tag pattern"
}

require_release_absent() {
  local tag=$1
  local page=1
  local response length matches
  while ((page <= 100)); do
    response=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/releases?per_page=100&page=${page}")
    length=$(jq -er 'if type == "array" then length else error("unexpected response") end' <<< "$response")
    matches=$(jq -er --arg tag "$tag" '[.[] | select(.tag_name == $tag)] | length' <<< "$response")
    [[ "$matches" == 0 ]] || fail "a draft or published release already uses tag ${tag}"
    ((length < 100)) && return 0
    ((page += 1))
  done
  fail 'release absence could not be proven within 100 API pages'
}

remote_branch_sha() {
  git ls-remote --heads origin "$branch_ref" | awk 'NR == 1 { print $1 }'
}

remote_default_branch_sha() {
  git ls-remote --heads origin "$default_branch_ref" | awk 'NR == 1 { print $1 }'
}

require_default_branch_workflow_identity() {
  local expected_sha=$1
  local fetched_sha
  git fetch --quiet --no-tags origin "$default_branch_ref" \
    || fail "cannot fetch default branch ${default_branch_ref}"
  fetched_sha=$(git rev-parse 'FETCH_HEAD^{commit}')
  [[ "$fetched_sha" == "$expected_sha" ]] \
    || fail "default branch moved while its workflow tree was fetched"
  git diff --quiet "$fetched_sha" "$GITHUB_SHA" -- .github/workflows \
    || fail "release workflow tree must already match ${default_branch_ref} before a tag can be published with GITHUB_TOKEN"
}

remote_tag_sha() {
  local tag=$1
  local resolved
  resolved=$(git ls-remote origin "refs/tags/${tag}^{}" | awk 'NR == 1 { print $1 }')
  if [[ -z "$resolved" ]]; then
    resolved=$(git ls-remote --refs origin "refs/tags/${tag}" | awk 'NR == 1 { print $1 }')
  fi
  printf '%s\n' "$resolved"
}

require_successful_push_run() {
  local workflow=$1
  local response
  response=$(api_get "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/actions/workflows/${workflow}/runs?head_sha=${GITHUB_SHA}&event=push&status=success&per_page=100")
  jq -e --arg branch "$release_branch" --arg sha "$GITHUB_SHA" '
    .workflow_runs
    | any(.head_branch == $branch and .head_sha == $sha and .event == "push" and .conclusion == "success")
  ' <<< "$response" >/dev/null || fail "${workflow} has no successful push run for ${GITHUB_SHA}"
}

require_no_open_severe_alerts() {
  local severity response count
  for severity in critical high; do
    response=$(curl --fail --silent --show-error --get \
      --header "Authorization: Bearer ${GH_TOKEN}" \
      --header 'Accept: application/vnd.github+json' \
      --header 'X-GitHub-Api-Version: 2022-11-28' \
      --data-urlencode 'state=open' \
      --data-urlencode "severity=${severity}" \
      --data-urlencode "ref=${branch_ref}" \
      --data-urlencode 'per_page=100' \
      "${GITHUB_API_URL}/repos/${GITHUB_REPOSITORY}/code-scanning/alerts")
    count=$(jq -e 'if type == "array" then length else error("unexpected response") end' <<< "$response")
    [[ "$count" == 0 ]] || fail "${branch_ref} has ${count} open ${severity} code-scanning alert(s)"
  done
}

default_branch=$(repository_default_branch)
default_branch_ref="refs/heads/${default_branch}"
initial_branch_sha=$(remote_branch_sha)
initial_default_branch_sha=$(remote_default_branch_sha)
[[ -n "$initial_branch_sha" ]] || fail "release branch does not exist: ${branch_ref}"
[[ -n "$initial_default_branch_sha" ]] || fail "default branch does not exist: ${default_branch_ref}"
[[ "$(git rev-parse 'HEAD^{commit}')" == "$GITHUB_SHA" ]] || fail "checkout HEAD does not match GITHUB_SHA"
[[ "$initial_branch_sha" == "$GITHUB_SHA" ]] || fail "GITHUB_SHA is not the current ${branch_ref} tip"
require_default_branch_workflow_identity "$initial_default_branch_sha"

tag_name=""
initial_tag_sha=""
case "$GITHUB_EVENT_NAME" in
  push)
    [[ "$GITHUB_REF" == refs/tags/* && "${GITHUB_REF_TYPE:-}" == tag ]] || fail "push release must run from a tag"
    tag_name=$GITHUB_REF_NAME
    [[ "$tag_name" =~ $semver_pattern ]] || fail "tag must be vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.NUMBER"
    [[ "${GITHUB_REF_PROTECTED:-false}" == true ]] || fail "release tag must be protected by a repository ruleset"
    initial_tag_sha=$(remote_tag_sha "$tag_name")
    [[ -n "$initial_tag_sha" ]] || fail "remote release tag does not exist: ${tag_name}"
    [[ "$initial_tag_sha" == "$GITHUB_SHA" ]] || fail "remote release tag does not resolve to GITHUB_SHA"
    require_release_tag_ruleset
    require_immutable_releases
    require_release_environment
    require_release_absent "$tag_name"
    ;;
  *)
    fail "unsupported release event: ${GITHUB_EVENT_NAME}"
    ;;
esac

require_successful_push_run core-ci.yml
require_successful_push_run codeql.yml
require_no_open_severe_alerts

[[ "$(remote_branch_sha)" == "$initial_branch_sha" ]] || fail "release branch moved during validation"
[[ "$(remote_default_branch_sha)" == "$initial_default_branch_sha" ]] || fail "default branch moved during validation"
[[ "$(repository_default_branch)" == "$default_branch" ]] || fail "repository default branch changed during validation"
if [[ -n "$tag_name" ]]; then
  [[ "$(remote_tag_sha "$tag_name")" == "$initial_tag_sha" ]] || fail "release tag moved during validation"
fi

printf 'Release context is immutable for %s at %s.\n' "$GITHUB_REF" "$GITHUB_SHA"
