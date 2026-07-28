#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
checker="${script_dir}/check-release-context.sh"
publisher="${script_dir}/publish-release-assets.sh"
fake_sha=0123456789abcdef0123456789abcdef01234567
fake_default_sha=fedcba9876543210fedcba9876543210fedcba98
fake_default_branch=trunk
fake_changed_default_branch=stable
fake_tag=v1.2.3-rc.4
test_root=$(mktemp -d "${TMPDIR:-/tmp}/foxos-release-context-test.XXXXXX")
trap 'rm -rf -- "$test_root"' EXIT

fail() {
  printf 'release context behavior test failed: %s\n' "$1" >&2
  exit 1
}

git() {
  case "$*" in
    "rev-parse HEAD^{commit}")
      printf '%s\n' "$fake_sha"
      ;;
    "ls-remote --heads origin refs/heads/agent/foxos-core")
      printf '%s\trefs/heads/agent/foxos-core\n' "$fake_sha"
      ;;
    "check-ref-format refs/heads/${fake_default_branch}"|"check-ref-format refs/heads/${fake_changed_default_branch}")
      return 0
      ;;
    "check-ref-format refs/heads/bad..branch")
      return 1
      ;;
    "ls-remote --heads origin refs/heads/${fake_default_branch}")
      printf '%s\trefs/heads/%s\n' "$fake_default_sha" "$fake_default_branch"
      ;;
    "fetch --quiet --no-tags origin refs/heads/${fake_default_branch}")
      return 0
      ;;
    "rev-parse FETCH_HEAD^{commit}")
      printf '%s\n' "$fake_default_sha"
      ;;
    "diff --quiet ${fake_default_sha} ${fake_sha} -- .github/workflows")
      [[ "${FAKE_SCENARIO:-success}" != workflow-tree-mismatch ]]
      ;;
    "ls-remote origin refs/tags/${fake_tag}^{}")
      printf '%s\trefs/tags/%s^{}\n' "$fake_sha" "$fake_tag"
      ;;
    "ls-remote --refs origin refs/tags/${fake_tag}")
      printf '%s\trefs/tags/%s\n' "$fake_sha" "$fake_tag"
      ;;
    *)
      printf 'unexpected fake git invocation: %s\n' "$*" >&2
      return 64
      ;;
  esac
}

curl() {
  local data="" request=GET url=""
  local bypass count policy_type prevent_self_review reviewers rules
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
      https://*)
        url=$1
        shift
        ;;
      *)
        shift
        ;;
    esac
  done
  [[ -n "$url" ]] || {
    printf 'fake curl did not receive an API URL\n' >&2
    return 64
  }

  if [[ "${FAKE_SCENARIO:-success}" == publisher-create-payload ]]; then
    case "${request} ${url}" in
      "GET "*/releases\?*)
        printf '%s\n' '[]'
        ;;
      "POST "*/releases)
        [[ -n "${FAKE_PUBLISH_PAYLOAD_FILE:-}" ]] || return 64
        printf '%s\n' "$data" > "$FAKE_PUBLISH_PAYLOAD_FILE"
        printf '%s\n' '{}'
        ;;
      *)
        printf 'unexpected fake publisher curl request: %s %s\n' "$request" "$url" >&2
        return 64
        ;;
    esac
    return
  fi

  case "$url" in
    */repos/foxc888/foxos)
      case "${FAKE_SCENARIO:-success}" in
        invalid-default-branch)
          printf '%s\n' '{"default_branch":"bad..branch"}'
          ;;
        default-branch-changed)
          count=0
          [[ ! -f "${FAKE_DEFAULT_BRANCH_COUNTER:-}" ]] || count=$(<"$FAKE_DEFAULT_BRANCH_COUNTER")
          count=$((count + 1))
          printf '%s\n' "$count" > "$FAKE_DEFAULT_BRANCH_COUNTER"
          if ((count == 1)); then
            printf '{"default_branch":"%s"}\n' "$fake_default_branch"
          else
            printf '{"default_branch":"%s"}\n' "$fake_changed_default_branch"
          fi
          ;;
        *)
          printf '{"default_branch":"%s"}\n' "$fake_default_branch"
          ;;
      esac
      ;;
    */rulesets\?*)
      printf '%s\n' '[{"id":101,"name":"FoxOS immutable release tags","target":"tag","source_type":"Repository","source":"foxc888/foxos","enforcement":"active"}]'
      ;;
    */rulesets/101)
      if [[ "${FAKE_SCENARIO:-success}" == weak-ruleset ]]; then
        rules='[{"type":"creation"},{"type":"update"},{"type":"non_fast_forward"}]'
      else
        rules='[{"type":"creation"},{"type":"update"},{"type":"deletion"},{"type":"non_fast_forward"}]'
      fi
      if [[ "${FAKE_SCENARIO:-success}" == broad-bypass ]]; then
        bypass='[{"actor_id":5,"actor_type":"RepositoryRole","bypass_mode":"always"}]'
      else
        bypass='[{"actor_id":137797974,"actor_type":"User","bypass_mode":"always"}]'
      fi
      jq -cn --argjson rules "$rules" --argjson bypass "$bypass" '{
        id: 101,
        name: "FoxOS immutable release tags",
        target: "tag",
        source_type: "Repository",
        source: "foxc888/foxos",
        enforcement: "active",
        bypass_actors: $bypass,
        conditions: {ref_name: {include: ["refs/tags/v*"], exclude: []}},
        rules: $rules
      }'
      ;;
    */immutable-releases)
      if [[ "${FAKE_SCENARIO:-success}" == mutable-releases ]]; then
        printf '%s\n' '{"enabled":false,"enforced_by_owner":false}'
      else
        printf '%s\n' '{"enabled":true,"enforced_by_owner":false}'
      fi
      ;;
    */environments/release/deployment-branch-policies\?*)
      if [[ "${FAKE_SCENARIO:-success}" == branch-policy-type ]]; then
        policy_type=branch
      else
        policy_type=tag
      fi
      jq -cn --arg policy_type "$policy_type" '{total_count: 1, branch_policies: [{id: 17, name: "v*", type: $policy_type}]}'
      ;;
    */environments/release)
      if [[ "${FAKE_SCENARIO:-success}" == unprotected-environment ]]; then
        reviewers='[]'
      elif [[ "${FAKE_SCENARIO:-success}" == release-actor-reviewer ]]; then
        reviewers='[{"type":"User","reviewer":{"id":137797974,"login":"release-actor"}}]'
      elif [[ "${FAKE_SCENARIO:-success}" == team-reviewer ]]; then
        reviewers='[{"type":"Team","reviewer":{"id":424242,"slug":"release-team"}}]'
      elif [[ "${FAKE_SCENARIO:-success}" == multiple-reviewers ]]; then
        reviewers='[{"type":"User","reviewer":{"id":424242,"login":"release-reviewer"}},{"type":"User","reviewer":{"id":434343,"login":"second-reviewer"}}]'
      else
        reviewers='[{"type":"User","reviewer":{"id":424242,"login":"release-reviewer"}}]'
      fi
      if [[ "${FAKE_SCENARIO:-success}" == self-review-environment ]]; then
        prevent_self_review=false
      else
        prevent_self_review=true
      fi
      jq -cn --argjson reviewers "$reviewers" --argjson prevent_self_review "$prevent_self_review" '{
        name: "release",
        protection_rules: [{type: "required_reviewers", prevent_self_review: $prevent_self_review, reviewers: $reviewers}],
        deployment_branch_policy: {protected_branches: false, custom_branch_policies: true}
      }'
      ;;
    */releases\?*)
      if [[ "${FAKE_SCENARIO:-success}" == existing-release ]]; then
        printf '%s\n' '[{"id":88,"tag_name":"v1.2.3-rc.4","draft":true}]'
      else
        printf '%s\n' '[]'
      fi
      ;;
    */actions/workflows/core-ci.yml/runs\?*)
      printf '%s\n' "{\"workflow_runs\":[{\"head_branch\":\"agent/foxos-core\",\"head_sha\":\"${fake_sha}\",\"event\":\"push\",\"conclusion\":\"success\"}]}"
      ;;
    */actions/workflows/codeql.yml/runs\?*)
      if [[ "${FAKE_SCENARIO:-success}" == missing-codeql ]]; then
        printf '%s\n' '{"workflow_runs":[]}'
      else
        printf '%s\n' "{\"workflow_runs\":[{\"head_branch\":\"agent/foxos-core\",\"head_sha\":\"${fake_sha}\",\"event\":\"push\",\"conclusion\":\"success\"}]}"
      fi
      ;;
    */code-scanning/alerts)
      printf '%s\n' '[]'
      ;;
    *)
      printf 'unexpected fake curl URL: %s\n' "$url" >&2
      return 64
      ;;
  esac
}

export -f git curl
export fake_sha fake_default_sha fake_default_branch fake_changed_default_branch fake_tag

run_checker() {
  local scenario=$1
  local default_branch_counter="${test_root}/default-branch-${scenario}.count"
  rm -f -- "$default_branch_counter"
  FAKE_SCENARIO=$scenario \
  FAKE_DEFAULT_BRANCH_COUNTER="$default_branch_counter" \
  GITHUB_API_URL=https://api.github.test \
  GITHUB_EVENT_NAME=push \
  GITHUB_REF="refs/tags/${fake_tag}" \
  GITHUB_REF_NAME="$fake_tag" \
  GITHUB_REF_TYPE=tag \
  GITHUB_REF_PROTECTED=true \
  GITHUB_REPOSITORY=foxc888/foxos \
  GITHUB_SHA="$fake_sha" \
  GH_TOKEN=fake-token \
    bash "$checker"
}

run_failure_case() {
  local scenario=$1
  local expected=$2
  local output
  if output=$(run_checker "$scenario" 2>&1); then
    fail "scenario unexpectedly succeeded: ${scenario}"
  fi
  [[ "$output" == *"$expected"* ]] || fail "scenario ${scenario} did not report: ${expected}"
}

run_checker success >/dev/null || fail 'valid protected release context was rejected'
run_failure_case weak-ruleset 'restrict creation/update/deletion/non-fast-forward'
run_failure_case broad-bypass 'authorize only FoxOS release actor'
run_failure_case mutable-releases 'immutable releases must be enabled'
run_failure_case unprotected-environment 'must require exactly one independent reviewer'
run_failure_case self-review-environment 'prevent self-review'
run_failure_case release-actor-reviewer 'must require exactly one independent reviewer'
run_failure_case team-reviewer 'must require exactly one independent reviewer'
run_failure_case multiple-reviewers 'must require exactly one independent reviewer'
run_failure_case branch-policy-type 'must allow exactly the v* deployment tag pattern'
run_failure_case invalid-default-branch 'default branch is not a valid Git ref'
run_failure_case default-branch-changed 'repository default branch changed during validation'
run_failure_case existing-release 'draft or published release already uses tag'
run_failure_case missing-codeql 'codeql.yml has no successful push run'
run_failure_case workflow-tree-mismatch 'release workflow tree must already match refs/heads/trunk'

if GITHUB_API_URL=https://api.github.test \
  GITHUB_EVENT_NAME=workflow_dispatch \
  GITHUB_REF=refs/heads/agent/foxos-core \
  GITHUB_REF_NAME=agent/foxos-core \
  GITHUB_REPOSITORY=foxc888/foxos \
  GITHUB_SHA="$fake_sha" \
  GH_TOKEN=fake-token \
  bash "$checker" >/dev/null 2>&1; then
  fail 'workflow_dispatch context was accepted by the tag-only release checker'
fi

bash "$script_dir/test-release-publisher.sh" \
  || fail 'release publisher behavior checks failed'

printf 'Release context behavior checks passed.\n'
