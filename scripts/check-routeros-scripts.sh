#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
rsc_root="$repo_root/deploy/routeros"
failed=0

report() {
  printf 'RouterOS static check: %s\n' "$1" >&2
  failed=1
}

workflow_job_body() {
  local workflow=$1
  local job=$2
  awk -v marker="  ${job}:" '
    $0 == marker { inside = 1; next }
    inside && $0 ~ /^  [[:alnum:]_-]+:$/ { exit }
    inside { print }
  ' "$workflow"
}

workflow_actions_pinned_contract() {
  local workflow=$1
  local ref
  while IFS= read -r ref; do
    [[ "$ref" == ./* ]] && continue
    [[ "$ref" =~ ^[^@[:space:]]+@[0-9a-f]{40}$ ]] || return 1
  done < <(awk '
    $1 == "-" && $2 == "uses:" { print $3 }
    $1 == "uses:" { print $2 }
  ' "$workflow")
}

dockerfile_base_images_pinned_contract() {
  local dockerfile=$1
  local image
  while IFS= read -r image; do
    [[ "$image" == scratch ]] && continue
    [[ "$image" =~ ^[^@[:space:]]+@sha256:[0-9a-f]{64}$ ]] || return 1
  done < <(awk '$1 == "FROM" { print $2 }' "$dockerfile")
}

core_ci_go_tools_contract() {
  local workflow=$1
  local body install_line test_line
  body=$(workflow_job_body "$workflow" go)
  rg -Fq -- '- name: Install Go test tools' <<< "$body" || return 1
  install_line=$(awk '/apt-get install --yes ripgrep/ { print NR; exit }' <<< "$body")
  test_line=$(awk '/go test / { print NR; exit }' <<< "$body")
  [[ -n "$install_line" && -n "$test_line" ]] && ((install_line < test_line))
}

release_context_script_contract() {
  local script=$1
  local invariant
  for invariant in \
    'release_branch=agent/foxos-core' \
    'repository_default_branch()' \
    '/repos/${GITHUB_REPOSITORY}' \
    '.default_branch | select(type == "string" and length > 0)' \
    'git check-ref-format "$branch_ref_candidate"' \
    'default_branch_ref="refs/heads/${default_branch}"' \
    "semver_pattern='^v" \
    "release_ruleset_name='FoxOS immutable release tags'" \
    "release_tag_pattern='refs/tags/v*'" \
    'release_actor_id=137797974' \
    'release_environment=release' \
    'GITHUB_REF_PROTECTED' \
    '/rulesets?includes_parents=false&targets=tag&per_page=100&page=${page}' \
    '["creation", "update", "deletion", "non_fast_forward"]' \
    '.bypass_actors[0].actor_type == "User"' \
    '.bypass_actors[0].actor_id == ($actor_id | tonumber)' \
    '/immutable-releases' \
    ".enabled == true" \
    '/environments/${release_environment}' \
    '.type == "required_reviewers"' \
    '.prevent_self_review == true' \
    '.reviewers[0].type == "User"' \
    '.reviewers[0].reviewer.id != ($actor_id | tonumber)' \
    '/deployment-branch-policies?per_page=100' \
    '.branch_policies[0].type == "tag"' \
    'git ls-remote --heads origin "$branch_ref"' \
    'git ls-remote --heads origin "$default_branch_ref"' \
    'git fetch --quiet --no-tags origin "$default_branch_ref"' \
    'git diff --quiet "$fetched_sha" "$GITHUB_SHA" -- .github/workflows' \
    'git ls-remote origin "refs/tags/${tag}^{}"' \
    '/actions/workflows/${workflow}/runs?head_sha=${GITHUB_SHA}&event=push&status=success' \
    'require_successful_push_run core-ci.yml' \
    'require_successful_push_run codeql.yml' \
    '/code-scanning/alerts' \
    "--data-urlencode 'state=open'" \
    'for severity in critical high' \
    '/releases?per_page=100&page=${page}' \
    'select(.tag_name == $tag)' \
    'a draft or published release already uses tag' \
    'release branch moved during validation' \
    'default branch moved during validation' \
    'repository default branch changed during validation' \
    'release tag moved during validation'; do
    rg -Fq -- "$invariant" "$script" || return 1
  done
}

release_publisher_script_contract() {
  local script=$1
  local invariant rc_behavior stable_behavior
  for invariant in \
    'require_release_absent' \
    '/releases?per_page=100&page=${page}' \
    'a draft or published release already uses tag' \
    'draft: true' \
    'generate_release_notes: true' \
    'make_latest: "false"' \
    '--data-binary "@${asset}"' \
    '.digest == $digest' \
    '/assets?per_page=100' \
    '--request PATCH' \
    '{draft: false, prerelease: $prerelease, make_latest: $make_latest}' \
    '.immutable == true' \
    '/releases/latest' \
    'did not reach the required prerelease/latest state' \
    '.browser_download_url' \
    'anonymous download identity mismatch' \
    'sha256sum --check "$checksum_name"' \
    'draft_owned' \
    '--request DELETE' \
    'Published immutable release'; do
    rg -Fq -- "$invariant" "$script" || return 1
  done
  ! rg -Fq 'target_commitish' "$script" || return 1
  rc_behavior=$("$script" --classify-tag v1.2.3-rc.4)
  stable_behavior=$("$script" --classify-tag v1.2.3)
  [[ "$rc_behavior" == $'prerelease=true\nmake_latest=false' ]] \
    && [[ "$stable_behavior" == $'prerelease=false\nmake_latest=true' ]] \
    && ! "$script" --classify-tag v1.2.3-rc.01 >/dev/null 2>&1
}

release_workflow_contract() {
  local workflow=$1
  local context_line quality_line codeql_line verify_line revalidate_line publish_line
  local invariant
  for invariant in \
    'group: foxos-release-${{ github.ref }}' \
    'cancel-in-progress: false' \
    '  release-context:' \
    'name: Validate immutable release context' \
    'fetch-depth: 0' \
    'run: scripts/check-release-context.sh' \
    "if: github.event_name == 'push' && github.ref_type == 'tag' && startsWith(github.ref, 'refs/tags/v')" \
    'environment: release' \
    'name: Verify exact release asset allowlist' \
    'name: Revalidate immutable release context' \
    'name: Publish immutable GitHub release' \
    'run: scripts/publish-release-assets.sh release-assets'; do
    rg -Fq -- "$invariant" "$workflow" || return 1
  done
  [[ "$(rg -Fc 'needs: release-context' "$workflow")" == 2 ]] || return 1
  [[ "$(rg -Fc 'run: scripts/check-release-context.sh' "$workflow")" == 2 ]] || return 1
  ! rg -Fq 'workflow_dispatch:' "$workflow" || return 1
  ! rg -Fq 'softprops/action-gh-release' "$workflow" || return 1

  local codeql_body
  codeql_body=$(workflow_job_body "$workflow" codeql)
  for invariant in 'contents: read' 'packages: read' 'security-events: write'; do
    rg -Fq -- "$invariant" <<< "$codeql_body" || return 1
  done

  context_line=$(rg -n '^  release-context:$' "$workflow" | cut -d: -f1)
  quality_line=$(rg -n '^  quality:$' "$workflow" | cut -d: -f1)
  codeql_line=$(rg -n '^  codeql:$' "$workflow" | cut -d: -f1)
  verify_line=$(rg -n -F 'name: Verify exact release asset allowlist' "$workflow" | cut -d: -f1)
  revalidate_line=$(rg -n -F 'name: Revalidate immutable release context' "$workflow" | cut -d: -f1)
  publish_line=$(rg -n -F 'name: Publish immutable GitHub release' "$workflow" | cut -d: -f1)
  [[ -n "$context_line" && -n "$quality_line" && -n "$codeql_line" && -n "$verify_line" && -n "$revalidate_line" && -n "$publish_line" ]] \
    && ((context_line < quality_line && context_line < codeql_line && verify_line < revalidate_line && revalidate_line < publish_line))
}

quick_install_upload_manifest_contract() {
  local guide=$1
  local actual expected
  local expected_entries=(
    'disk1/site-config.rsc'
    'disk1/site-config.rsc.sha512'
    'disk1/site-config.example.rsc'
    'disk1/seal-site-config.sh'
    'disk1/load-site-config.rsc'
    'disk1/chr-envlists-smoke.md'
    'disk1/chr-envlists-smoke.rsc'
    'disk1/foxos-doctor.rsc'
    'disk1/mihomo_amd64.tar'
    'disk1/mosdns-amd64.tar'
    'disk1/foxos-upgrade-<release-id>/foxos-amd64.tar'
    'disk1/foxos-upgrade-<release-id>/SHA256SUMS'
    'disk1/foxos-upgrade-<release-id>/UPGRADE-MANIFEST.txt'
    'disk1/foxos-upgrade-<release-id>/upgrade-inspect.rsc'
    'disk1/foxos-upgrade-<release-id>/upgrade-plan.rsc'
    'disk1/foxos-upgrade-<release-id>/upgrade.rsc'
    'disk1/foxos-upgrade-<release-id>/upgrade-promote-inspect.rsc'
    'disk1/foxos-upgrade-<release-id>/upgrade-promote-plan.rsc'
    'disk1/foxos-upgrade-<release-id>/upgrade-promote.rsc'
    'disk1/foxos-upgrade-<release-id>/rollback-inspect.rsc'
    'disk1/foxos-upgrade-<release-id>/rollback-plan.rsc'
    'disk1/foxos-upgrade-<release-id>/rollback.rsc'
    'disk1/foxos-upgrade-<release-id>/upgrade-cleanup-inspect.rsc'
    'disk1/foxos-upgrade-<release-id>/upgrade-cleanup-plan.rsc'
    'disk1/foxos-upgrade-<release-id>/upgrade-cleanup-apply.rsc'
    'disk1/provenance/mihomo-container.lock.json'
    'disk1/provenance/mosdns-container.lock.json'
    'disk1/mihomo-config/config.yaml'
    'disk1/mihomo-config/base.yaml'
    'disk1/mosdns-config/config_custom.yaml'
    'disk1/preflight.rsc'
    'disk1/foxos-install-inspect.rsc'
    'disk1/foxos-plan.rsc'
    'disk1/foxos-full-install.rsc'
    'disk1/foxos-start-all.rsc'
    'disk1/foxos-verify.rsc'
    'disk1/foxos-dns-plan.rsc'
    'disk1/foxos-dns-apply.rsc'
    'disk1/foxos-uninstall-inspect.rsc'
    'disk1/uninstall-plan.rsc'
    'disk1/uninstall-apply.rsc'
    'disk1/SHA256SUMS'
    'disk1/RELEASE-MANIFEST.txt'
    'disk1/QUICK-INSTALL.md'
  )
  actual=$(awk '
    /^## 3\. 先备份、检查零碰撞，再上传$/ { section = 1; next }
    section && /^```text$/ { manifest = 1; next }
    manifest && /^```$/ { exit }
    manifest { print }
  ' "$guide" | sed '/^[[:space:]]*$/d' | LC_ALL=C sort)
  expected=$(printf '%s\n' "${expected_entries[@]}" | LC_ALL=C sort)
  [[ "$actual" == "$expected" ]]
}

quick_install_collision_manifest_contract() {
  local guide=$1
  local actual expected
  local expected_entries=(
    'QUICK-INSTALL.md'
    'RELEASE-MANIFEST.txt'
    'SHA256SUMS'
    'chr-envlists-smoke.md'
    'chr-envlists-smoke.rsc'
    'foxos-dns-apply.rsc'
    'foxos-dns-plan.rsc'
    'foxos-doctor.rsc'
    'foxos-full-install.rsc'
    'foxos-install-inspect.rsc'
    'foxos-plan.rsc'
    'foxos-start-all.rsc'
    'foxos-uninstall-inspect.rsc'
    'foxos-verify.rsc'
    'load-site-config.rsc'
    'mihomo-config'
    'mihomo_amd64.tar'
    'mosdns-amd64.tar'
    'mosdns-config'
    'preflight.rsc'
    'provenance'
    'seal-site-config.sh'
    'site-config.example.rsc'
    'site-config.rsc'
    'site-config.rsc.sha512'
    'foxos-upgrade-<release-id>'
    'uninstall-apply.rsc'
    'uninstall-plan.rsc'
  )
  actual=$(sed -n 's/^:local uploadTargets {\(.*\)}$/\1/p' "$guide" \
    | tr ';' '\n' \
    | sed 's/^"//; s/"$//' \
    | sed '/^[[:space:]]*$/d' \
    | LC_ALL=C sort)
  expected=$(printf '%s\n' "${expected_entries[@]}" | LC_ALL=C sort)
  [[ "$actual" == "$expected" ]]
}

quick_install_download_contract() {
  local guide=$1
  local ci release
  local ci_mkdir ci_unzip ci_cd_download ci_outer_checksum ci_extract ci_cd_bundle ci_inner_checksum
  local release_outer_checksum release_extract release_cd_bundle release_inner_checksum

  ci=$(awk '
    /^### Core CI artifact$/ { inside = 1; next }
    inside && /^### / { exit }
    inside { print }
  ' "$guide")
  release=$(awk '
    /^### GitHub Release$/ { inside = 1; next }
    inside && /^## / { exit }
    inside { print }
  ' "$guide")

  for invariant in \
    'ci_commit="REPLACE_WITH_40_CHARACTER_COMMIT_SHA"' \
    'ci_artifact="foxos-full-amd64-${ci_commit}"' \
    'ci_download_dir="${ci_artifact}-download"' \
    'mkdir -- "${ci_download_dir}"' \
    'unzip "${ci_artifact}.zip" -d "${ci_download_dir}"' \
    'cd "${ci_download_dir}"' \
    'sha256sum --check "${ci_artifact}.tar.gz.sha256"' \
    'tar -xzf "${ci_artifact}.tar.gz"' \
    'cd "${ci_artifact}"' \
    'sha256sum --check SHA256SUMS'; do
    rg -Fq -- "$invariant" <<< "$ci" || return 1
  done
  for invariant in \
    'release_id="REPLACE_WITH_RELEASE_TAG"' \
    'release_artifact="foxos-full-amd64-${release_id}"' \
    'sha256sum --check "${release_artifact}.tar.gz.sha256"' \
    'tar -xzf "${release_artifact}.tar.gz"' \
    'cd "${release_artifact}"' \
    'sha256sum --check SHA256SUMS'; do
    rg -Fq -- "$invariant" <<< "$release" || return 1
  done

  ci_mkdir=$(rg -n -F 'mkdir -- "${ci_download_dir}"' <<< "$ci" | head -n1 | cut -d: -f1)
  ci_unzip=$(rg -n -F 'unzip "${ci_artifact}.zip" -d "${ci_download_dir}"' <<< "$ci" | head -n1 | cut -d: -f1)
  ci_cd_download=$(rg -n -F 'cd "${ci_download_dir}"' <<< "$ci" | head -n1 | cut -d: -f1)
  ci_outer_checksum=$(rg -n -F 'sha256sum --check "${ci_artifact}.tar.gz.sha256"' <<< "$ci" | head -n1 | cut -d: -f1)
  ci_extract=$(rg -n -F 'tar -xzf "${ci_artifact}.tar.gz"' <<< "$ci" | head -n1 | cut -d: -f1)
  ci_cd_bundle=$(rg -n -F 'cd "${ci_artifact}"' <<< "$ci" | head -n1 | cut -d: -f1)
  ci_inner_checksum=$(rg -n -F 'sha256sum --check SHA256SUMS' <<< "$ci" | head -n1 | cut -d: -f1)
  release_outer_checksum=$(rg -n -F 'sha256sum --check "${release_artifact}.tar.gz.sha256"' <<< "$release" | head -n1 | cut -d: -f1)
  release_extract=$(rg -n -F 'tar -xzf "${release_artifact}.tar.gz"' <<< "$release" | head -n1 | cut -d: -f1)
  release_cd_bundle=$(rg -n -F 'cd "${release_artifact}"' <<< "$release" | head -n1 | cut -d: -f1)
  release_inner_checksum=$(rg -n -F 'sha256sum --check SHA256SUMS' <<< "$release" | head -n1 | cut -d: -f1)

  [[ -n "$ci_mkdir" && -n "$ci_unzip" && -n "$ci_cd_download" && -n "$ci_outer_checksum" \
    && -n "$ci_extract" && -n "$ci_cd_bundle" && -n "$ci_inner_checksum" \
    && -n "$release_outer_checksum" && -n "$release_extract" && -n "$release_cd_bundle" && -n "$release_inner_checksum" ]] \
    && ((ci_mkdir < ci_unzip \
      && ci_unzip < ci_cd_download \
      && ci_cd_download < ci_outer_checksum \
      && ci_outer_checksum < ci_extract \
      && ci_extract < ci_cd_bundle \
      && ci_cd_bundle < ci_inner_checksum \
      && release_outer_checksum < release_extract \
      && release_extract < release_cd_bundle \
      && release_cd_bundle < release_inner_checksum)) \
    && ! rg -Fq 'unzip ' <<< "$release" \
    && ! rg -Fq 'ci_download_dir' <<< "$release" \
    && ! rg -Fq '跳过 ZIP' "$guide"
}

quick_install_preupload_safety_contract() {
  local guide=$1
  local export_line backup_line export_download_line backup_download_line
  local backup_dir_line collision_line collision_guard_line winbox_line scp_line
  local invariant
  for invariant in \
    '封存唯一站点清单 -> 加密备份并下载 -> 零碰撞检查 -> 上传 -> 只读 doctor/plan' \
    '/export hide-sensitive file=before-foxos-YYYYMMDD-HHMM' \
    '/system/backup/save name=before-foxos-YYYYMMDD-HHMM password="<unique-offline-password>" encryption=aes-sha256' \
    'backup_dir="../routeros-backups/${backup_id}"' \
    'umask 077' \
    'mkdir -p -- "$backup_dir"' \
    'chmod 700 "$backup_dir"' \
    'test -s "${backup_dir}/${backup_id}.rsc" && test -s "${backup_dir}/${backup_id}.backup"' \
    '`backup_dir` 必须位于当前 bundle 目录之外，也不能是它的子目录' \
    ':set uploadCollisions ($uploadCollisions + $count)' \
    'UPLOAD-COLLISIONS total=' \
    '任何计数不为零都必须中止' \
    '不要删除、改名或直接覆盖'; do
    rg -Fq -- "$invariant" "$guide" || return 1
  done

  export_line=$(rg -n -F '/export hide-sensitive file=before-foxos-YYYYMMDD-HHMM' "$guide" | head -n1 | cut -d: -f1)
  backup_line=$(rg -n -F '/system/backup/save name=before-foxos-YYYYMMDD-HHMM' "$guide" | head -n1 | cut -d: -f1)
  backup_dir_line=$(rg -n -F 'backup_dir="../routeros-backups/${backup_id}"' "$guide" | head -n1 | cut -d: -f1)
  export_download_line=$(rg -n -F 'scp "admin@${router_address}:${backup_id}.rsc" "${backup_dir}/${backup_id}.rsc"' "$guide" | head -n1 | cut -d: -f1)
  backup_download_line=$(rg -n -F 'scp "admin@${router_address}:${backup_id}.backup" "${backup_dir}/${backup_id}.backup"' "$guide" | head -n1 | cut -d: -f1)
  collision_line=$(rg -n -F ':local uploadCollisions 0' "$guide" | head -n1 | cut -d: -f1)
  collision_guard_line=$(rg -n -F ':if ($uploadCollisions > 0) do={' "$guide" | head -n1 | cut -d: -f1)
  winbox_line=$(rg -n -F '零碰撞后，使用 WinBox 时' "$guide" | head -n1 | cut -d: -f1)
  scp_line=$(rg -n -F 'scp -r ./* "admin@${router_address}:${storage_root}/"' "$guide" | head -n1 | cut -d: -f1)

  [[ -n "$export_line" && -n "$backup_line" && -n "$backup_dir_line" && -n "$export_download_line" && -n "$backup_download_line" \
    && -n "$collision_line" && -n "$collision_guard_line" && -n "$winbox_line" && -n "$scp_line" ]] \
    && ((export_line < backup_line \
      && backup_line < backup_dir_line \
      && backup_dir_line < export_download_line \
      && backup_dir_line < backup_download_line \
      && export_download_line < collision_line \
      && backup_download_line < collision_line \
      && collision_line < collision_guard_line \
      && collision_guard_line < winbox_line \
      && collision_guard_line < scp_line)) \
    && ! rg -Fq '"./${backup_id}.' "$guide"
}

amd64_architecture_contract() {
  local script=$1
  local invariant
  for invariant in \
    ':local architecture ' \
    ':local imageArchitecture ""' \
    ':if ($architecture = "x86" || $architecture = "x86_64") do={ :set imageArchitecture "amd64" }' \
    '$imageArchitecture != "amd64"'; do
    rg -Fq -- "$invariant" "$script" || return 1
  done
  ! rg -n 'architecture[^#]*!=[^#]*"x86"' "$script" >/dev/null
}

reserved_internal_storage_contract() {
  local script=$1
  local invariant
  for invariant in \
    ':local storageMode "disk"' \
    ':if ($storageRoot = "foxos") do={ :set storageMode "internal" }' \
    '/system/resource get free-hdd-space' \
    '/disk find where slot=$storageRoot'; do
    rg -Fq -- "$invariant" "$script" || return 1
  done
}

chr_envlists_smoke_contract() {
  local script=$1
  local confirmation_line first_write residual_line failure_line pass_line
  local invariant
  for invariant in \
    ':if ($boardName != "CHR") do={' \
    ':if ($imageArchitecture != "amd64") do={' \
    ':if ($containerPackageVersion != $versionBase) do={' \
    ':if ($FoxOSCHREnvlistsSmokeConfirm != "RUN-ON-DISPOSABLE-CHR") do={' \
    ':local parentPathMarker ("." . ".")' \
    ':local rootPattern ("^" . $rootDirectory . "(\$|/)")' \
    '[:find $imagePath $parentPathMarker]' \
    '/container/envs/add list=$envListName key=$envKey value=$runID' \
    '/container/envs get $envItem value] != $runID' \
    ':local mountListName ("chr-envlists-smoke-mount-" . $runID)' \
    ':local writableMountMode "rw"' \
    ':local mountSourcePath do={' \
    ':local sourceMount $1' \
    ':local currentSource [/container/mounts get $sourceMount src]' \
    ':if ([:typeof $currentSource] != "str") do={ :return "" }' \
    ':if ([:len $currentSource] > 0 && [:pick $currentSource 0 1] = "/") do={' \
    ':return [:pick $currentSource 1 [:len $currentSource]]' \
    ':return $currentSource' \
    ':local mountMode do={' \
    ':local currentMode [/container/mounts get $mount mode]' \
    '/container/mounts/add list=$mountListName src=$mountSource dst=$mountDestination mode=$writableMountMode comment=$owner' \
    '[$mountSourcePath $mountByList] != $mountSource' \
    '[$mountMode $mountByList] != $writableMountMode' \
    '" src-raw=" . [/container/mounts get $mountByList src]' \
    '" src-normalized=" . [$mountSourcePath $mountByList]' \
    '/container/add name=$containerName file=$imagePath interface=$vethName root-dir=$rootDirectory envlists=$envListName mountlists=$mountListName logging=no start-on-boot=no comment=$owner' \
    ':local containerState do={' \
    ':local running [/container get $container running]' \
    ':local stopped [/container get $container stopped]' \
    ':local containerRoot do={' \
    ':local containerEnvLists [/container get $container envlists]' \
    ':local containerMountLists [/container get $container mountlists]' \
    '$containerEnvLists != $envListName' \
    '$containerMountLists != $mountListName' \
    '$cleanupByName != $cleanupByOwner' \
    '[/container get $cleanupByName interface] != $vethName' \
    '[$containerRoot $cleanupByName] != $rootDirectory' \
    '[$containerState $cleanupContainer] != "stopped"' \
    '[/interface/veth get $cleanupVeth comment] != $owner' \
    '[/container/envs get $cleanupEnvItems value] != $runID' \
    '$cleanupMountByList != $cleanupMountByOwner' \
    '[$mountSourcePath $cleanupMountByList] != $mountSource' \
    '[$mountMode $cleanupMountByList] != $writableMountMode' \
    '/container/remove $cleanupContainer' \
    '/container/mounts/remove $cleanupMountByList' \
    '/interface/veth/remove $cleanupVeth' \
    '/container/envs/remove $cleanupEnvItems' \
    ':local residualMounts ([:len [/container/mounts find where list=$mountListName]] + [:len [/container/mounts find where comment=$owner]])' \
    'CLEANUP residual-containers=' \
    'residual-mounts=' \
    'mount-source-readback=normalized' \
    'CHR_ENVLISTS_SMOKE PASS'; do
    rg -Fq -- "$invariant" "$script" || return 1
  done

  confirmation_line=$(rg -n -F ':if ($FoxOSCHREnvlistsSmokeConfirm != "RUN-ON-DISPOSABLE-CHR") do={' "$script" | head -n1 | cut -d: -f1)
  first_write=$(rg -n '^[[:space:]]*/(container/envs/add|container/mounts/add|interface/veth/add|container/add)[[:space:]]' "$script" | head -n1 | cut -d: -f1)
  residual_line=$(rg -n -F ':local residualContainers ' "$script" | head -n1 | cut -d: -f1)
  failure_line=$(rg -n -F ':if ($operationComplete = false || [:len $primaryFailure] > 0 || $cleanupFailed || $residualContainers > 0 || $residualMounts > 0 || $residualVeths > 0 || $residualEnvs > 0 || $residualRoots > 0) do={' "$script" | head -n1 | cut -d: -f1)
  pass_line=$(rg -n -F ':put ("CHR_ENVLISTS_SMOKE PASS ' "$script" | head -n1 | cut -d: -f1)
  [[ -n "$confirmation_line" && -n "$first_write" && -n "$residual_line" && -n "$failure_line" && -n "$pass_line" ]] \
    && ((confirmation_line < first_write && first_write < residual_line && residual_line < failure_line && failure_line < pass_line)) \
    && ! rg -Fq 'FoxOSSiteManifestVersion' "$script" \
    && ! rg -n '^[[:space:]]*/container/start([[:space:]]|$)|^[[:space:]]*/interface/bridge(/port)?/(add|set|remove)([[:space:]]|$)|^[[:space:]]*/system/device-mode/update([[:space:]]|$)|start-on-boot=yes' "$script" >/dev/null
}

foxos_doctor_contract() {
  local script=$1
  local invariant
  for invariant in \
    'PASS|doctor|name=foxos-doctor|mode=read-only|scope=host-prerequisites' \
    ':if ($FoxOSSiteManifestVersion != 2 || $FoxOSSiteLoaderVersion != 1) do={' \
    'NEEDS-ACTION|site-manifest|reason=load-sealed-site-config-first' \
    'PASS|routeros-version|' \
    'PASS|architecture|' \
    'PASS|container-package|' \
    'PASS|device-mode-container|' \
    'PASS|device-mode-scheduler|' \
    'PASS|footprint-veth-ports|state=' \
    'reusable-veth-count=' \
    '[/interface/veth get $expectedVeth gateway] != $routerAddress' \
    '$namedVethCount != $reusableVethCount' \
    'CONFLICT|footprint-containers|' \
    'CONFLICT|footprint-env-lists|' \
    'CONFLICT|footprint-files|' \
    'CONFLICT|summary|needs-action-count=' \
    'NEEDS-ACTION|summary|needs-action-count=' \
    'PASS|summary|needs-action-count=0|conflict-count=0|first-install=ready-for-plan'; do
    rg -Fq -- "$invariant" "$script" || return 1
  done

  ! rg -n '^[[:space:]]*/[A-Za-z0-9_/-]+/(add|set|remove|enable|disable|update|start|stop|reset|move|run)([[:space:]]|$)|^[[:space:]]*/(import|tool/fetch|system/reboot|system/package/apply-changes)([[:space:]]|$)' "$script" >/dev/null \
    && ! rg -n '/container/envs get .* value\]|/user get .* password\]|/system/script get .* source\]|/file get .* contents\]|/log/print|/export|show-sensitive' "$script" >/dev/null
}

upgrade_promote_api_contract() {
  local script=$1
  local invariant final_acceptance promoted_write
  for invariant in \
    ':for checkpointAttempt from=1 to=6 do={' \
    ':local checkpointAbortIdentityOK false' \
    ':for checkpointAbortAttempt from=1 to=6 do={' \
    ':local activeAbortIdentityOK false' \
    ':for activeAbortAttempt from=1 to=6 do={' \
    ':local failedStartAbortIdentityOK false' \
    ':for failedStartAbortAttempt from=1 to=6 do={' \
    ':local rejectedAbortIdentityOK false' \
    ':for rejectedAbortAttempt from=1 to=6 do={' \
    ':local finalAcceptanceOK false' \
    ':for finalAcceptanceAttempt from=1 to=18 do={' \
    ':if ($finalLiveOK && $finalReadyOK && $finalPageOK && $finalReadOnlyOK) do={' \
    ':local finalActiveOwner [/container find where comment="foxos:active"]' \
    ':local finalRollbackOwner [/container find where comment="foxos:rollback"]' \
    '禁止写入 promoted、自动 abort 或回滚' \
    ':for promotedAttempt from=1 to=6 do={' \
    '禁止自动回滚'; do
    rg -Fq -- "$invariant" "$script" || return 1
  done

  local checkpoint_live checkpoint_abort checkpoint_ready abort_response_checks
  local active_live active_abort active_ready
  local failed_live failed_abort failed_ready
  local rejected_live rejected_abort rejected_ready
  checkpoint_live=$(rg -n -F ':for checkpointLiveAttempt ' "$script" | head -n1 | cut -d: -f1)
  checkpoint_abort=$(rg -n -F ':for checkpointAbortAttempt ' "$script" | head -n1 | cut -d: -f1)
  checkpoint_ready=$(rg -n -F ':for checkpointReadyAttempt ' "$script" | head -n1 | cut -d: -f1)
  active_live=$(rg -n -F ':for activeLiveAttempt ' "$script" | head -n1 | cut -d: -f1)
  active_abort=$(rg -n -F ':for activeAbortAttempt ' "$script" | head -n1 | cut -d: -f1)
  active_ready=$(rg -n -F ':for activeReadyAttempt ' "$script" | head -n1 | cut -d: -f1)
  failed_live=$(rg -n -F ':for failedStartLiveAttempt ' "$script" | head -n1 | cut -d: -f1)
  failed_abort=$(rg -n -F ':for failedStartAbortAttempt ' "$script" | head -n1 | cut -d: -f1)
  failed_ready=$(rg -n -F ':for failedStartReadyAttempt ' "$script" | head -n1 | cut -d: -f1)
  rejected_live=$(rg -n -F ':for rejectedLiveAttempt ' "$script" | head -n1 | cut -d: -f1)
  rejected_abort=$(rg -n -F ':for rejectedAbortAttempt ' "$script" | head -n1 | cut -d: -f1)
  rejected_ready=$(rg -n -F ':for rejectedReadyAttempt ' "$script" | head -n1 | cut -d: -f1)
  final_acceptance=$(rg -n -F ':local finalAcceptanceOK false' "$script" | head -n1 | cut -d: -f1)
  promoted_write=$(rg -n -F ':local promotedRecorded false' "$script" | head -n1 | cut -d: -f1)
  abort_response_checks=$(rg -c 'abortResult.*status.*aborted.*status.*restored' "$script" || true)
  [[ -n "$checkpoint_live" && -n "$checkpoint_abort" && -n "$checkpoint_ready" \
    && -n "$active_live" && -n "$active_abort" && -n "$active_ready" \
    && -n "$failed_live" && -n "$failed_abort" && -n "$failed_ready" \
    && -n "$rejected_live" && -n "$rejected_abort" && -n "$rejected_ready" \
    && -n "$final_acceptance" && -n "$promoted_write" \
    && "$abort_response_checks" -ge 4 ]] \
    && ((checkpoint_live < checkpoint_abort && checkpoint_abort < checkpoint_ready)) \
    && ((active_live < active_abort && active_abort < active_ready)) \
    && ((failed_live < failed_abort && failed_abort < failed_ready)) \
    && ((rejected_live < rejected_abort && rejected_abort < rejected_ready)) \
    && ((final_acceptance < promoted_write)) \
    && awk '
      /:local finalAcceptanceOK false/ { final_gate = 1 }
      /:local promotedRecorded false/ { final_gate = 0 }
      final_gate && index($0, "/api/v1/health/live") { live = 1 }
      final_gate && index($0, "/api/v1/health/ready") { ready = 1 }
      final_gate && index($0, "id=\\\"root\\\"") { page = 1 }
      final_gate && index($0, "/api/v1/audit-events?limit=1") { read_only = 1 }
      final_gate && index($0, "$finalLiveOK && $finalReadyOK && $finalPageOK && $finalReadOnlyOK") { combined = 1 }
      final_gate && index($0, ":local finalActiveOwner") { active_owner = 1 }
      final_gate && index($0, ":local finalRollbackOwner") { rollback_owner = 1 }
      final_gate && index($0, ":if (") &&
        index($0, "[:len $finalActiveOwner] != 1") &&
        index($0, "[:len $finalRollbackOwner] != 1") &&
        index($0, "$finalActiveOwner != $pending") &&
        index($0, "$finalRollbackOwner != $active") &&
        index($0, "[$FoxOSContainerState $finalActiveOwner] != \"running\"") &&
        index($0, "[/container get $finalActiveOwner name] != $pendingName") &&
        index($0, "[$FoxOSContainerRoot $finalActiveOwner] != $pendingRoot") &&
        index($0, "[$FoxOSContainerState $finalRollbackOwner] != \"stopped\"") &&
        index($0, "[/container get $finalRollbackOwner name] != $rollbackName") &&
        index($0, "[$FoxOSContainerRoot $finalRollbackOwner] != ($FoxOSSiteStorageRoot . \"/containers/\" . $rollbackName)") {
          owner_gate = 1
        }
      END { exit !(live && ready && page && read_only && combined && active_owner && rollback_owner && owner_gate) }
    ' "$script" \
    && awk '
      /:local promotedRecorded false/ { promoted = 1 }
      promoted && /:put \(\"升级完成：release=/ { exit }
      promoted && /^[[:space:]]*\/container\/(set|start|stop)[[:space:]]/ { bad = 1 }
      END { exit bad }
    ' "$script"
}

container_commands_guarded() {
  local verb=$1
  local script=$2
  awk -v verb="$verb" '
    function brace_delta(line,    i, c, escaped, in_string, delta) {
      for (i = 1; i <= length(line); i++) {
        c = substr(line, i, 1)
        if (escaped) { escaped = 0; continue }
        if (in_string && c == "\\") { escaped = 1; continue }
        if (c == "\"") { in_string = !in_string; continue }
        if (!in_string && c == "#") break
        if (!in_string && c == "{") delta++
        if (!in_string && c == "}") delta--
      }
      return delta
    }
    BEGIN { onerror_depth = 0; bad = 0 }
    {
      starts_guard = onerror_depth == 0 && $0 ~ /^[[:space:]]*:onerror[[:space:]][A-Za-z0-9_]+[[:space:]]+in=\{/
      guarded = starts_guard || onerror_depth > 0
      if ($0 ~ ("/container/" verb "[[:space:]]") && !guarded) {
        printf "%s:%d: /container/%s is outside :onerror\n", FILENAME, NR, verb > "/dev/stderr"
        bad = 1
      }
      if (guarded) onerror_depth += brace_delta($0)
      if (onerror_depth < 0) onerror_depth = 0
    }
    END { exit bad }
  ' "$script"
}

scheduler_preflight_gate() {
  awk '
    /:if \(\$schedulerDeviceMode != true/ { in_gate = 1; saw_gate = 1 }
    in_gate && /:set failed true/ { saw_failure = 1 }
    in_gate && /^[[:space:]]*}/ { in_gate = 0 }
    END { exit !(saw_gate && saw_failure) }
  ' "$1"
}

mosdns_env_contract() {
  local script=$1
  rg -Fq 'allowedMosDNSEnvKeys "|FOXOS_INSTALL_MARKER|MOSDNS_AUTO_INIT|"' "$script" \
    && rg -Fq '|mosdns-env-item:' "$script" \
    && rg -Fq 'mosdnsEnvState "FAIL"' "$script"
}

upgrade_marker_contract() {
  local script=$1
  rg -Fq ':local installMarkers [/container/envs find where list="foxos-env" key="FOXOS_INSTALL_MARKER"]' "$script" \
    && rg -Fq ':if ([:len $installMarkers] != 1) do={ :error "FOXOS_INSTALL_MARKER 必须且只能存在一个" }' "$script" \
    && rg -Fq ':if ([/container/envs get $installMarkers value] != "foxos:complete")' "$script"
}

confirmed_lifecycle_contract() {
  local script=$1
  local inspector=$2
  local current_global=$3
  local approved_global=$4
  local confirmation_global=$5
  local inspector_lines inspector_count second_inspector last_digest_guard confirmation_guard
  local clear_confirmation clear_approved first_write
  inspector_lines=$( (rg -n -F "/$inspector" "$script" || true) | cut -d: -f1 )
  inspector_count=$(printf '%s\n' "$inspector_lines" | sed '/^$/d' | wc -l | tr -d ' ')
  second_inspector=$(printf '%s\n' "$inspector_lines" | sed -n '2p')
  last_digest_guard=$( (rg -n -F "$current_global" "$script" || true) | tail -n1 | cut -d: -f1 )
  confirmation_guard=$( (rg -n '\$confirmation(Digest)? != \$approved(Digest)?|\$confirmation != \$approved' "$script" || true) | head -n1 | cut -d: -f1 )
  clear_confirmation=$( (rg -n -F ":set $confirmation_global \"\"" "$script" || true) | head -n1 | cut -d: -f1 )
  clear_approved=$( (rg -n -F ":set $approved_global \"\"" "$script" || true) | head -n1 | cut -d: -f1 )
  first_write=$( (rg -n '^[[:space:]]*/container/(add|set|start|stop)[[:space:]]|/tool/fetch[^#]*http-method=post' "$script" || true) | head -n1 | cut -d: -f1 )
  [[ "$inspector_count" == 2 ]] \
    && rg -Fq ":global $current_global" "$script" \
    && rg -Fq ":global $approved_global" "$script" \
    && rg -Fq ":global $confirmation_global" "$script" \
    && [[ -n "$second_inspector" && -n "$last_digest_guard" && -n "$confirmation_guard" && -n "$clear_confirmation" && -n "$clear_approved" && -n "$first_write" ]] \
    && ((second_inspector < last_digest_guard && confirmation_guard < second_inspector && last_digest_guard < clear_confirmation && clear_confirmation < first_write && clear_approved < first_write))
}

routeros_private_cidr_contract() {
  local script=$1
  rg -Fq ':local private10Address [:toip "10.0.0.0"]' "$script" \
    && rg -Fq ':local private10Netmask [:toip "255.0.0.0"]' "$script" \
    && rg -Fq ':local private172Address [:toip "172.16.0.0"]' "$script" \
    && rg -Fq ':local private172Netmask [:toip "255.240.0.0"]' "$script" \
    && rg -Fq ':local private192Address [:toip "192.168.0.0"]' "$script" \
    && rg -Fq ':local private192Netmask [:toip "255.255.0.0"]' "$script" \
    && rg -Fq ':local privateULA [:toip6 "fc00::/7"]' "$script" \
    && rg -Fq ':local ipv4MaskDefinitions {' "$script" \
    && rg -Fq ':local privateCIDRIsIPv6 ([:typeof [:find $privateCIDR ":"]] != "nil")' "$script" \
    && rg -Fq ':set privateCIDRPrefixValue [:toip6 $privateCIDR]' "$script" \
    && rg -Fq ':set privateCIDRAddress [:toip6 [:pick $privateCIDR 0 $privateCIDRSlash]]' "$script" \
    && rg -Fq ':set privateCIDRAddress [:toip [:pick $privateCIDR 0 $privateCIDRSlash]]' "$script" \
    && rg -Fq '[:typeof $privateCIDRPrefixValue] != "ip6-prefix"' "$script" \
    && rg -Fq '[:typeof $privateCIDRAddress] != "ip6"' "$script" \
    && rg -Fq ':local privateCIDRNetmask' "$script" \
    && rg -Fq '(($privateCIDRAddress & $privateCIDRNetmask) != $privateCIDRAddress)' "$script" \
    && rg -Fq '(($privateCIDRAddress & $private10Netmask) = $private10Address)' "$script" \
    && rg -Fq '(($privateCIDRAddress & $private172Netmask) = $private172Address)' "$script" \
    && rg -Fq '(($privateCIDRAddress & $private192Netmask) = $private192Address)' "$script" \
    && ! rg -Fq '[:toip $privateCIDR]' "$script" \
    && ! rg -q ':toip "[0-9.]+/[0-9]+"' "$script"
}

routeros_site_ipv4_cidr_contract() {
  local script=$1
  rg -Fq ':set networkAddressValue [:toip [:pick $siteNetwork 0 $prefixSeparator]]' "$script" \
    && rg -Fq '(($networkAddressValue & $netmaskValue) != $networkAddressValue)' "$script" \
    && rg -Fq '(($serviceAddress & $netmaskValue) != $networkAddressValue)' "$script" \
    && ! rg -Fq '[:toip $siteNetwork]' "$script" \
    && ! rg -Fq 'sitePrefixValue' "$script"
}

routeros_find_filter_variable_collision_free() {
  python3 - "$@" <<'PY'
from pathlib import Path
import re
import sys

collisions = []
for filename in sys.argv[1:]:
    path = Path(filename)
    text = path.read_text(encoding="utf-8")
    variable_names = set(
        re.findall(r"(?m)^\s*:(?:local|global)\s+([A-Za-z][A-Za-z0-9]*)\b", text)
    )
    for line_number, line in enumerate(text.splitlines(), start=1):
        if "find where" not in line:
            continue
        for variable_name in variable_names:
            same_name_filter = re.compile(
                rf"(?<![A-Za-z0-9_-]){re.escape(variable_name)}\s*"
                rf"(?:!=|>=|<=|=|~|>|<)\s*(?:\(\s*)*\${re.escape(variable_name)}"
                rf"(?![A-Za-z0-9_-])"
            )
            if same_name_filter.search(line):
                collisions.append(f"{path}:{line_number}:{variable_name}")

if collisions:
    print("RouterOS find filters reuse a property name as the variable name:")
    print("\n".join(collisions))
    raise SystemExit(1)
PY
}

routeros_arp_occupancy_contract() {
  local script=$1
  local query_count guarded_count
  query_count=$(rg --count-matches '/ip/arp find where address=' "$script" || true)
  guarded_count=$(rg --count-matches '/ip/arp find where address=[^]]+ && status!="failed"' "$script" || true)
  [[ -n "$query_count" && "$query_count" != 0 && "$query_count" == "$guarded_count" ]]
}

routeros_hostname_contract() {
  local script=$1
  rg -Fq ':local publicHostnameLength [:len $publicHostname]' "$script" \
    && rg -Fq ':local publicHostnameDoubleDot ("." . ".")' "$script" \
    && rg -Fq '$publicHostnameLength < 3' "$script" \
    && rg -Fq '$publicHostnameLength > 253' "$script" \
    && rg -Fq '!($publicHostname ~ "^[a-z0-9][a-z0-9.-]*[a-z0-9]\$")' "$script" \
    && rg -Fq '[:find $publicHostname $publicHostnameDoubleDot]' "$script" \
    && rg -Fq '[:find $publicHostname ".-"]' "$script" \
    && rg -Fq '[:find $publicHostname "-."]' "$script" \
    && rg -Fq '!($publicHostname ~ "\\.home\\.arpa\$")' "$script" \
    && rg -Fq ':local hostnameLabelEnd [:find $publicHostname "." $hostnameLabelCursor]' "$script" \
    && rg -Fq ':local hostnameLabel [:pick $publicHostname $hostnameLabelCursor $hostnameLabelEnd]' "$script" \
    && rg -Fq '$hostnameLabelLength < 1 || $hostnameLabelLength > 63' "$script" \
    && rg -Fq '!($hostnameLabel ~ "^[a-z0-9-]+\$")' "$script" \
    && rg -Fq '[:pick $hostnameLabel 0 1] = "-"' "$script" \
    && rg -Fq '[:pick $hostnameLabel ($hostnameLabelLength - 1) $hostnameLabelLength] = "-"' "$script"
}

site_loader_cursor_contract() {
  local script=$1
  rg -U -Fq -- $'  :if ([:pick $configContents $cursor ($cursor + 1)] = "\\n") do={\n    :set cursor ($cursor + 1)\n    :continue\n  }\n  :local lineEnd [:find $configContents "\\n" $cursor]' "$script"
}

site_loader_manifest_model() {
  local manifest=$1
  python3 - "$manifest" <<'PY'
from pathlib import Path
import sys

content = Path(sys.argv[1]).read_bytes()
cursor = 0
assignments = []

while cursor < len(content):
    # RouterOS :find excludes the supplied start offset. The loader guard must
    # consume an LF at the cursor before looking for the next line ending.
    if content[cursor : cursor + 1] == b"\n":
        cursor += 1
        continue
    line_end = content.find(b"\n", cursor + 1)
    if line_end < 0:
        line_end = len(content)
    line = content[cursor:line_end]
    cursor = line_end + 1
    if not line or line.startswith(b"#"):
        continue
    assignments.append(line)

if len(assignments) != 11 or assignments[0] != b":global FoxOSSiteManifestVersion 2":
    raise SystemExit(1)
PY
}

container_envlist_contract() {
  local script=$1
  rg -q 'envlists([=]|\])' "$script" \
    && ! rg -q 'envlist([=]|\])' "$script"
}

container_mount_list_contract() {
  local script=$1
  rg -q '/container/mounts (add|find)' "$script" \
    && ! rg -q '/container/mounts (add|find)[^#]*(name=|where name)' "$script"
}

container_mount_readback_contract() {
  local script=$1
  rg -q '\[\$FoxOSMountSource \$[A-Za-z][A-Za-z0-9]*\] (!=|=) \$[A-Za-z][A-Za-z0-9]*' "$script" \
    && rg -q '\[\$FoxOSMountMode \$[A-Za-z][A-Za-z0-9]*\] (!=|=) \$FoxOSWritableMountMode' "$script"
}

mount_digest_source_contract() {
  local script=$1
  rg -q ':set (material|mountEvidence).*\[\$FoxOSMountSource \$mountID\]' "$script"
}

lifecycle_shared_mount_contract() {
  local script=$1
  local source_contract=0
  local completion_line first_mutation
  if rg -Fq ':local expectedSource ($storageRoot . "/" . [:pick $definition ($p1 + 1) $p2])' "$script" \
    || rg -Fq ':local expectedSource ($FoxOSSiteStorageRoot . "/" . [:pick $definition ($p1 + 1) $p2])' "$script"; then
    source_contract=1
  fi
  ((source_contract == 1)) \
    && rg -Fq ':local sharedMountDefinitions {"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}' "$script" \
    && rg -Fq ':local verifiedSharedMounts 0' "$script" \
    && rg -Fq ':local expectedDestination [:pick $definition ($p2 + 1) [:len $definition]]' "$script" \
    && rg -Fq ':local mountID [/container/mounts find where list=$mountName]' "$script" \
    && rg -Fq ':if ([:len $mountID] != 1 || [$FoxOSMountSource $mountID] != $expectedSource || [/container/mounts get $mountID dst] != $expectedDestination || [$FoxOSMountMode $mountID] != $FoxOSWritableMountMode) do={' "$script" \
    && rg -Fq ':set verifiedSharedMounts ($verifiedSharedMounts + 1)' "$script" \
    && rg -Fq ':if ($verifiedSharedMounts != 3) do={ :error "三个共享挂载未全部通过身份与可写检查" }' "$script" \
    || return 1
  completion_line=$( (rg -n -F ':if ($verifiedSharedMounts != 3)' "$script" || true) | head -n1 | cut -d: -f1 )
  first_mutation=$( (rg -n '/container/(set|start|stop)[[:space:]]' "$script" || true) | head -n1 | cut -d: -f1 )
  [[ -n "$completion_line" && -n "$first_mutation" ]] && ((completion_line < first_mutation))
}

full_install_mount_contract() {
  local script=$1
  container_mount_readback_contract "$script" \
    && rg -q '/container/mounts add list=\$mountName[^#]*mode=\$FoxOSWritableMountMode' "$script"
}

container_identity_fields_contract() {
  local script=$1
  rg -q '/container get \$[A-Za-z][A-Za-z0-9]* interface\]' "$script" \
    && rg -q '/container get \$[A-Za-z][A-Za-z0-9]* envlists\]' "$script" \
    && rg -q '/container get \$[A-Za-z][A-Za-z0-9]* mountlists\]' "$script" \
    && rg -q '\[\$FoxOSContainerRoot \$[A-Za-z][A-Za-z0-9]*\]' "$script" \
    && rg -q '/container get \$[A-Za-z][A-Za-z0-9]* start-on-boot\]' "$script" \
    && rg -q '/container get \$[A-Za-z][A-Za-z0-9]* logging\]' "$script"
}

container_compatibility_contract() {
  local script=$1
  local invariant
  for invariant in \
    ':global FoxOSContainerCompatVersion 1' \
    ':global FoxOSContainerState do={' \
    ':local running [/container get $container running]' \
    ':local stopped [/container get $container stopped]' \
    ':if ($isRunning && $isStopped) do={ :return "invalid" }' \
    ':if ($isRunning) do={ :return "running" }' \
    ':if ($isStopped) do={ :return "stopped" }' \
    ':return "transitional"' \
    ':global FoxOSContainerRoot do={' \
    ':local rootDirectory [/container get $container root-dir]' \
    '[:pick $rootDirectory 0 1] = "/"' \
    ':return [:pick $rootDirectory 1 [:len $rootDirectory]]'; do
    rg -Fq -- "$invariant" "$script" || return 1
  done
}

mount_compatibility_contract() {
  local script=$1
  local invariant
  for invariant in \
    ':global FoxOSMountCompatVersion 2' \
    ':global FoxOSWritableMountMode "rw"' \
    ':global FoxOSMountSource do={' \
    ':local sourceMount $1' \
    ':local mountSource [/container/mounts get $sourceMount src]' \
    ':if ([:typeof $mountSource] != "str") do={ :return "" }' \
    ':if ([:len $mountSource] > 0 && [:pick $mountSource 0 1] = "/") do={' \
    ':return [:pick $mountSource 1 [:len $mountSource]]' \
    ':return $mountSource' \
    ':global FoxOSMountMode do={' \
    ':local mountMode [/container/mounts get $mount mode]' \
    ':if ([:typeof $mountMode] != "str") do={ :return "invalid" }' \
    '$mountMode = "ro" || $mountMode = "ro,noexec" || $mountMode = "rw" || $mountMode = "rw,noexec"' \
    ':return "invalid"'; do
    rg -Fq -- "$invariant" "$script" || return 1
  done
}

file_native_handle_contract() {
  local target=$1
  ! rg -q -g '*.rsc' '/file get[[:space:]]+(?:[^[:space:]\[]+|\[[^]]+\])[[:space:]]+(?:\.id|value-name[[:space:]]*=[[:space:]]*\.id)[[:space:]]*\]' "$target"
}

mount_native_handle_contract() {
  local target=$1
  ! rg -q -g '*.rsc' '/container/mounts get[[:space:]]+\$[A-Za-z][A-Za-z0-9]*[[:space:]]+\.id\]' "$target"
}

install_env_native_handle_contract() {
  local script=$1
  ! rg -q '/container/envs get[[:space:]]+\$[A-Za-z][A-Za-z0-9]*[[:space:]]+(?:\.id|value-name[[:space:]]*=[[:space:]]*\.id)[[:space:]]*\]' "$script"
}

container_compatibility_consumer_contract() {
  local script=$1
  rg -Fq ':global FoxOSContainerCompatVersion' "$script" \
    && rg -Fq ':global FoxOSContainerState' "$script" \
    && rg -Fq ':global FoxOSContainerRoot' "$script" \
    && rg -Fq '$FoxOSContainerCompatVersion != 1' "$script" \
    && rg -q '\[\$FoxOSContainer(State|Root) \$[A-Za-z][A-Za-z0-9]*\]' "$script"
}

mount_compatibility_consumer_contract() {
  local script=$1
  rg -Fq ':global FoxOSMountCompatVersion' "$script" \
    && rg -Fq ':global FoxOSWritableMountMode' "$script" \
    && rg -Fq ':global FoxOSMountSource' "$script" \
    && rg -Fq ':global FoxOSMountMode' "$script" \
    && rg -Fq '$FoxOSMountCompatVersion != 2 || $FoxOSWritableMountMode != "rw"' "$script" \
    && rg -q '\[\$FoxOSMountSource \$[A-Za-z][A-Za-z0-9]*\]' "$script" \
    && rg -q '\[\$FoxOSMountMode \$[A-Za-z][A-Za-z0-9]*\]' "$script"
}

admin_container_logging_contract() {
  local root=$1
  local script
  if rg -q -g '*.rsc' '/container/add[^#]*interface=veth-foxos[^#]*logging=yes' "$root"; then
    return 1
  fi
  for script in upgrade-inspect.rsc upgrade.rsc upgrade-promote-inspect.rsc upgrade-promote.rsc rollback-inspect.rsc rollback.rsc upgrade-cleanup-inspect.rsc; do
    if rg -q 'logging=yes|logging\] != true|Logging != true' "$root/$script"; then
      return 1
    fi
    rg -q 'logging=no|logging\] != false|Logging != false' "$root/$script" || return 1
  done
  rg -Fq 'interface=veth-foxos root-dir=($storageRoot . "/containers/foxos-initial") envlists=foxos-env mountlists=foxos-mihomo-config,foxos-data,foxos-backups logging=no' "$root/foxos-full-install.rsc" \
    && rg -Fq '[/container get $foxosContainer logging] != false' "$root/foxos-full-install.rsc" \
    && rg -Fq '[/container get $activeContainer logging] = false' "$root/foxos-install-inspect.rsc" \
    && rg -Fq '[/container get $adminSlot logging] != false' "$root/foxos-start-all.rsc" \
    && rg -Fq '[/container get $active logging] != false' "$root/foxos-start-all.rsc" \
    && rg -Fq '[/container get $active logging] != false' "$root/foxos-verify.rsc" \
    || return 1
  for script in foxos-uninstall-inspect.rsc uninstall-apply.rsc; do
    rg -Fq ':local loggingMatches false' "$root/$script" \
      && rg -Fq '$owner = "foxos:mihomo" || $owner = "foxos:mosdns"' "$root/$script" \
      && rg -Fq '$containerLogging = false || $containerLogging = "no"' "$root/$script" \
      || return 1
  done
}

cleanup_inspector_contract() {
  local script=$1
  container_envlist_contract "$script" \
    && container_mount_list_contract "$script" \
    && container_mount_readback_contract "$script" \
    && container_identity_fields_contract "$script" \
    && scheduler_contract "$script" \
    && system_script_policy_contract "$script" \
    && rg -Fq ':local releaseID "__FOXOS_RELEASE_ID__"' "$script" \
    && rg -Fq ':local transitions [/container find where comment~"^foxos:transition:"]' "$script" \
    && rg -Fq ':if ([:len $active] != 1)' "$script" \
    && rg -Fq '([:len $rollback] + [:len $rollbackComplete]) != 1' "$script" \
    && rg -Fq ':if ([:len $pending] > 0)' "$script" \
    && rg -Fq ':if ([:len $transitions] > 0)' "$script" \
    && rg -Fq '[$FoxOSContainerState $active] != "running"' "$script" \
    && rg -Fq '[$FoxOSContainerState $retirement] != "stopped"' "$script" \
    && rg -Fq '$sourceMarker = "foxos:rollback" && $activeName != $releaseName' "$script" \
    && rg -Fq '$sourceMarker = "foxos:rollback-complete" && $retirementName != $releaseName' "$script" \
    && rg -Fq ':local knownAdminSlots [/container find where comment~"^foxos:(active|pending|rollback|rollback-complete|transition:promote|transition:rollback|transition:rollback:previous|retained|failed)\$"]' "$script" \
    && rg -Fq ':local interfaceAdminSlots [/container find where interface="veth-foxos"]' "$script" \
    && rg -Fq ':if ([:len $knownAdminSlots] != [:len $interfaceAdminSlots])' "$script" \
    && rg -Fq ':if ($runningAdminCount != 1 || $runningAdminID != $active)' "$script" \
    && rg -Fq 'foxos-upgrade-cleanup-v3|release=' "$script" \
    && rg -Fq '|site=" . $FoxOSSiteLoadedDigest' "$script" \
    && rg -Fq '|active=" . [:pick $active 0]' "$script" \
    && rg -Fq '|rollback=" . [:pick $retirement 0]' "$script" \
    && rg -Fq '|admin-slot=" . $adminSlot' "$script" \
    && rg -Fq '|veth=" . [/interface/veth get $foxosVeth .id]' "$script" \
    && rg -Fq '|mount=" . [:pick $mountID 0]' "$script" \
    && rg -Fq ':local startScriptByName [/system/script find where name="foxos-start-sequence"]' "$script" \
    && rg -Fq ':local startScriptByOwner [/system/script find where comment="foxos:start-sequence"]' "$script" \
    && rg -Fq '[/system/script get $startScriptByName .id] != [/system/script get $startScriptByOwner .id]' "$script" \
    && rg -Fq '|start-script=" . [/system/script get $startScriptByName .id]' "$script" \
    && rg -Fq ':local schedulerByName [/system/scheduler find where name="foxos-start-sequence"]' "$script" \
    && rg -Fq ':local schedulerByOwner [/system/scheduler find where comment="foxos:start-sequence"]' "$script" \
    && rg -Fq '[/system/scheduler get $schedulerByName .id] != [/system/scheduler get $schedulerByOwner .id]' "$script" \
    && rg -Fq '[/system/scheduler get $schedulerByName disabled] != false' "$script" \
    && rg -Fq '|scheduler=" . [/system/scheduler get $schedulerByName .id]' "$script" \
    && rg -Fq '[/system/scheduler get $schedulerByName disabled])' "$script" \
    && rg -Fq ':set FoxOSUpgradeCleanupActiveID $active' "$script" \
    && rg -Fq ':set FoxOSUpgradeCleanupRollbackID $retirement' "$script"
}

cleanup_apply_snapshot_contract() {
  local script=$1
  local loader_count inspector_count snapshot_guard first_write
  loader_count=$(rg -F '/load-site-config.rsc")' "$script" | wc -l | tr -d ' ')
  inspector_count=$(rg -F '/upgrade-cleanup-inspect.rsc")' "$script" | wc -l | tr -d ' ')
  snapshot_guard=$(rg -n -F ':if ($FoxOSUpgradeCleanupCurrentDigest != $approved || $FoxOSUpgradeCleanupActiveID != $activeSnapshot || $FoxOSUpgradeCleanupRollbackID != $rollbackSnapshot || $FoxOSUpgradeCleanupRollbackMarker != $markerSnapshot) do={' "$script" | head -n1 | cut -d: -f1)
  first_write=$(rg -n -F '/container/set $rollbackSnapshot comment="foxos:retained"' "$script" | head -n1 | cut -d: -f1)
  [[ "$loader_count" == 2 && "$inspector_count" == 2 && -n "$snapshot_guard" && -n "$first_write" && "$snapshot_guard" -lt "$first_write" ]] \
    && rg -Fq ':local activeSnapshot $FoxOSUpgradeCleanupActiveID' "$script" \
    && rg -Fq ':local rollbackSnapshot $FoxOSUpgradeCleanupRollbackID' "$script" \
    && rg -Fq ':local markerSnapshot $FoxOSUpgradeCleanupRollbackMarker' "$script" \
    && rg -Fq ':local active [/container find where comment="foxos:active"]' "$script" \
    && rg -Fq ':local retirement [/container find where comment=$markerSnapshot]' "$script" \
    && rg -Fq ':if ($active != $activeSnapshot || $retirement != $rollbackSnapshot)' "$script" \
    && ! rg -q '^[[:space:]]*/container/set[[:space:]]+\$(retirement|active)[[:space:]]' "$script"
}

uninstall_snapshot_contract() {
  local script=$1
  local declaration
  for declaration in \
    ':local ownedContainers [/container find where comment~"^foxos:"]' \
    ':local envItems [/container/envs find where list="foxos-env"]' \
    ':local mosdnsEnvItems [/container/envs find where list="foxos-mosdns-env"]' \
    ':local schedulerSnapshot [/system/scheduler find where name="foxos-start-sequence"]' \
    ':local startScriptSnapshot [/system/script find where name="foxos-start-sequence"]' \
    ':local dnsRecordSnapshot [/ip/dns/static find where comment="foxos:dns:admin"]' \
    ':local serviceUserSnapshot [/user find where name="foxos-service"]' \
    ':local serviceGroupSnapshot [/user/group find where name="foxos-rest"]' \
    ':local mihomoPortSnapshot [/interface/bridge/port find where interface="veth-mihomo"]' \
    ':local mosdnsPortSnapshot [/interface/bridge/port find where interface="veth-mosdns"]' \
    ':local foxosPortSnapshot [/interface/bridge/port find where interface="veth-foxos"]' \
    ':local mihomoVethSnapshot [/interface/veth find where name="veth-mihomo"]' \
    ':local mosdnsVethSnapshot [/interface/veth find where name="veth-mosdns"]' \
    ':local foxosVethSnapshot [/interface/veth find where name="veth-foxos"]' \
    ':local mihomoRuntimeMountSnapshot [/container/mounts find where list="foxos-mihomo-runtime"]' \
    ':local mihomoConfigMountSnapshot [/container/mounts find where list="foxos-mihomo-config"]' \
    ':local mosdnsRuntimeMountSnapshot [/container/mounts find where list="foxos-mosdns-runtime"]' \
    ':local dataMountSnapshot [/container/mounts find where list="foxos-data"]' \
    ':local backupsMountSnapshot [/container/mounts find where list="foxos-backups"]'; do
    rg -Fq "$declaration" "$script" || return 1
  done
  rg -Fq ':global FoxOSUninstallContainerCount' "$script" \
    && rg -Fq '容器完整身份在删除前变化' "$script"
}

retained_state_install_contract() {
  local script=$1
  local env_line data_line backups_line guard_line material_line
  env_line=$( (rg -n -F ':local envItems [/container/envs find where list="foxos-env"]' "$script" || true) | head -n1 | cut -d: -f1 )
  data_line=$( (rg -n -F ':local persistedDataRoot [/file find where name=($storageRoot . "/foxos-data")]' "$script" || true) | head -n1 | cut -d: -f1 )
  backups_line=$( (rg -n -F ':local persistedBackupRoot [/file find where name=($storageRoot . "/foxos-backups")]' "$script" || true) | head -n1 | cut -d: -f1 )
  guard_line=$( (rg -n -F ':if ([:len $envItems] = 0 && ([:len $persistedDataRoot] > 0 || [:len $persistedBackupRoot] > 0)) do={' "$script" || true) | head -n1 | cut -d: -f1 )
  material_line=$( (rg -n -F ':set material ($material . "|retained-roots=" . [:len $persistedDataRoot] . ":" . [:len $persistedBackupRoot])' "$script" || true) | head -n1 | cut -d: -f1 )
  [[ -n "$env_line" && -n "$data_line" && -n "$backups_line" && -n "$guard_line" && -n "$material_line" \
    && "$env_line" -lt "$guard_line" && "$data_line" -lt "$guard_line" && "$backups_line" -lt "$guard_line" && "$guard_line" -lt "$material_line" ]] \
    && rg -Fq 'FAIL retained foxos-data or foxos-backups exists without foxos-env' "$script"
}

pending_journal_uninstall_contract() {
  local inspector=$1
  local apply=$2
  local inspector_find inspector_guard stopped_guard apply_find first_protected_mutation
  inspector_find=$( (rg -n -F ':local pendingMihomoApplyJournal [/file find where name=($FoxOSSiteStorageRoot . "/foxos-backups/mihomo/.foxos-mihomo-apply.json")]' "$inspector" || true) | head -n1 | cut -d: -f1 )
  inspector_guard=$( (rg -n -F ':if ([:len $pendingMihomoApplyJournal] > 0) do={' "$inspector" || true) | head -n1 | cut -d: -f1 )
  stopped_guard=$( (rg -n -F ':if ($allStopped = false) do={' "$apply" || true) | head -n1 | cut -d: -f1 )
  apply_find=$( (rg -n -F ':local pendingMihomoApplyJournalAfterStop [/file find where name=($FoxOSSiteStorageRoot . "/foxos-backups/mihomo/.foxos-mihomo-apply.json")]' "$apply" || true) | head -n1 | cut -d: -f1 )
  first_protected_mutation=$( (rg -n '^[[:space:]]*/(system/scheduler[[:space:]]+(set|remove)|container/remove|container/envs/remove)' "$apply" || true) | head -n1 | cut -d: -f1 )
  [[ -n "$inspector_find" && -n "$inspector_guard" && "$inspector_find" -lt "$inspector_guard" \
    && -n "$stopped_guard" && -n "$apply_find" && -n "$first_protected_mutation" \
    && "$stopped_guard" -lt "$apply_find" && "$apply_find" -lt "$first_protected_mutation" ]] \
    && rg -Fq ':if ([:len $pendingMihomoApplyJournalAfterStop] > 0) do={' "$apply" \
    && rg -Fq 'scheduler、env 与 confirmation key 均保留' "$apply"
}

uninstall_predelete_contract() {
  local script=$1
  local last_snapshot inspector_lines inspector_count second_inspector digest_guard first_resource_write binding
  uninstall_snapshot_contract "$script" || return 1
  last_snapshot=$( (rg -n -F ':local backupsMountSnapshot [/container/mounts find where list="foxos-backups"]' "$script" || true) | head -n1 | cut -d: -f1 )
  inspector_lines=$( (rg -n -F '/import file-name=($FoxOSSiteStorageRoot . "/foxos-uninstall-inspect.rsc")' "$script" || true) | cut -d: -f1 )
  inspector_count=$(printf '%s\n' "$inspector_lines" | sed '/^$/d' | wc -l | tr -d ' ')
  second_inspector=$(printf '%s\n' "$inspector_lines" | sed -n '2p')
  digest_guard=$( (rg -n -F ':if ($FoxOSUninstallCurrentDigest != $approved) do={' "$script" || true) | head -n1 | cut -d: -f1 )
  first_resource_write=$( (rg -n '^[[:space:]]*/(system/(scheduler|script)|container(/(envs|mounts))?|ip/dns/static|interface/(bridge/port|veth)|user(/group)?)[/[:space:]]+(set|remove|stop)' "$script" || true) | head -n1 | cut -d: -f1 )
  [[ "$inspector_count" == 3 && -n "$last_snapshot" && -n "$second_inspector" && -n "$digest_guard" && -n "$first_resource_write" \
    && "$last_snapshot" -lt "$second_inspector" && "$second_inspector" -lt "$digest_guard" && "$digest_guard" -lt "$first_resource_write" ]] \
    || return 1
  for binding in \
    ':local scheduler $schedulerSnapshot' \
    ':local dnsRecord $dnsRecordSnapshot' \
    ':local serviceUser $serviceUserSnapshot' \
    ':local serviceGroup $serviceGroupSnapshot' \
    ':local startScript $startScriptSnapshot' \
    ':set portID $mihomoPortSnapshot' \
    ':set portID $mosdnsPortSnapshot' \
    ':set portID $foxosPortSnapshot' \
    ':set vethID $mihomoVethSnapshot' \
    ':set vethID $mosdnsVethSnapshot' \
    ':set vethID $foxosVethSnapshot' \
    ':set mountID $mihomoRuntimeMountSnapshot' \
    ':set mountID $mihomoConfigMountSnapshot' \
    ':set mountID $mosdnsRuntimeMountSnapshot' \
    ':set mountID $dataMountSnapshot' \
    ':set mountID $backupsMountSnapshot' \
    '/system/scheduler remove $scheduler' \
    '/container/remove $containerID' \
    '/ip/dns/static/remove $dnsRecord' \
    '/interface/bridge/port/remove $portID' \
    '/interface/veth/remove $vethID' \
    '/user/remove $serviceUser' \
    '/user/group/remove $serviceGroup' \
    '/container/mounts/remove $mountID' \
    '/system/script remove $startScript'; do
    rg -Fq "$binding" "$script" || return 1
  done
}

transition_contract() {
  local promote=$1
  local rollback=$2
  local start_all=$3
  local promote_pending promote_old promote_active
  local rollback_target rollback_previous rollback_active rollback_complete
  promote_pending=$(rg -n '/container/set \$pending comment="foxos:transition:promote"' "$promote" | head -n1 | cut -d: -f1)
  promote_old=$(rg -n '/container/set \$active comment="foxos:rollback"' "$promote" | head -n1 | cut -d: -f1)
  promote_active=$(rg -n '/container/set \$pending comment="foxos:active"' "$promote" | head -n1 | cut -d: -f1)
  rollback_target=$(rg -n '/container/set \$rollback comment="foxos:transition:rollback"' "$rollback" | head -n1 | cut -d: -f1)
  rollback_previous=$(rg -n '/container/set \$active comment="foxos:transition:rollback:previous"' "$rollback" | head -n1 | cut -d: -f1)
  rollback_active=$(rg -n '/container/set \$rollback comment="foxos:active"' "$rollback" | head -n1 | cut -d: -f1)
  rollback_complete=$(rg -n '/container/set \$active comment="foxos:rollback-complete"' "$rollback" | head -n1 | cut -d: -f1)
  [[ -n "$promote_pending" && -n "$promote_old" && -n "$promote_active" \
    && "$promote_pending" -lt "$promote_old" && "$promote_old" -lt "$promote_active" \
    && -n "$rollback_target" && -n "$rollback_previous" && -n "$rollback_active" && -n "$rollback_complete" \
    && "$rollback_target" -lt "$rollback_previous" && "$rollback_previous" -lt "$rollback_active" && "$rollback_active" -lt "$rollback_complete" ]] \
    && rg -Fq 'comment="foxos:transition:promote"' "$start_all" \
    && rg -Fq 'comment="foxos:transition:rollback"' "$start_all" \
    && rg -Fq 'comment="foxos:transition:rollback:previous"' "$start_all" \
    && rg -Fq 'comment="foxos:rollback-complete"' "$start_all"
}

rest_address_contract() {
  local script=$1
  rg -Fq ':local foxosRESTAddress ($foxosAddress . "/32")' "$script" \
    && rg -Fq ':local wwwAddress [:pick $wwwAddresses $wwwCursor $wwwEnd]' "$script" \
    && rg -Fq '($wwwAddress != $siteNetwork && $wwwAddress != $foxosRESTAddress)' "$script" \
    && rg -Fq '[:find $wwwSeen ("," . $wwwAddress . ",")]' "$script" \
    && rg -Fq '$wwwCount > 2' "$script"
}

rest_www_static_service_contract() {
  local script=$1
  rg -Fq ':local wwwService [/ip/service find where name="www" && dynamic=no]' "$script"
}

rest_address_model() {
  local value=$1
  local site_network=$2
  local foxos_host=$3
  local entry
  local count=0
  local seen=','
  [[ -n "$value" && "$value" != ,* && "$value" != *, && "$value" != *,,* ]] || return 1
  IFS=',' read -r -a entries <<< "$value"
  for entry in "${entries[@]}"; do
    ((count += 1))
    [[ "$entry" == "$site_network" || "$entry" == "$foxos_host" ]] || return 1
    [[ "$seen" != *",$entry,"* ]] || return 1
    seen+="$entry,"
  done
  ((count >= 1 && count <= 2))
}

scheduler_contract() {
  local script=$1
  rg -Fq '[/system/scheduler get $' "$script" \
    && rg -Fq ' interval] ' "$script" \
    && rg -Fq '"0s"' "$script" \
    && rg -Fq ' policy] ' "$script" \
    && rg -Fq '"read,write,test"' "$script"
}

system_script_policy_contract() {
  local script=$1
  rg -q '/system/script get \$[A-Za-z][A-Za-z0-9]* policy\] (==|=|!=) "read,write,test"' "$script"
}

lifecycle_start_sequence_contract() {
  local script=$1
  local scheduler_name_var scheduler_owner_var
  if rg -Fq ':local startSchedulerByName [/system/scheduler find where name="foxos-start-sequence"]' "$script"; then
    scheduler_name_var=startSchedulerByName
    scheduler_owner_var=startSchedulerByOwner
  elif rg -Fq ':local schedulerByName [/system/scheduler find where name="foxos-start-sequence"]' "$script"; then
    scheduler_name_var=schedulerByName
    scheduler_owner_var=schedulerByOwner
  else
    return 1
  fi

  rg -Fq ':local expectedStartSource (":delay 20s; /import file-name=' "$script" \
    && rg -Fq 'load-site-config.rsc; /import file-name=' "$script" \
    && rg -Fq ':local startScriptByName [/system/script find where name="foxos-start-sequence"]' "$script" \
    && rg -Fq ':local startScriptByOwner [/system/script find where comment="foxos:start-sequence"]' "$script" \
    && rg -Fq '[/system/script get $startScriptByName .id] != [/system/script get $startScriptByOwner .id]' "$script" \
    && rg -Fq '[/system/script get $startScriptByName source] != $expectedStartSource' "$script" \
    && rg -Fq '[/system/script get $startScriptByName policy] != "read,write,test"' "$script" \
    && rg -q '\|start-script=" \. \[/system/script get \$startScriptByName \.id\]' "$script" \
    && rg -Fq ":local $scheduler_owner_var [/system/scheduler find where comment=\"foxos:start-sequence\"]" "$script" \
    && rg -Fq "[/system/scheduler get \$$scheduler_name_var .id] != [/system/scheduler get \$$scheduler_owner_var .id]" "$script" \
    && rg -Fq "[/system/scheduler get \$$scheduler_name_var on-event] != \"foxos-start-sequence\"" "$script" \
    && rg -Fq "[/system/scheduler get \$$scheduler_name_var start-time] != \"startup\"" "$script" \
    && rg -Fq "[/system/scheduler get \$$scheduler_name_var interval] != \"0s\"" "$script" \
    && rg -Fq "[/system/scheduler get \$$scheduler_name_var policy] != \"read,write,test\"" "$script" \
    && rg -q '(disabled\] != false|Disabled != false)' "$script" \
    && rg -q '(disabled\] != "no"|Disabled != "no")' "$script" \
    && rg -q '\|(start-scheduler|scheduler)=" \. \[/system/scheduler get \$[A-Za-z][A-Za-z0-9]* \.id\]' "$script"
}

routeros_parse_safety_contract() {
  local script=$1
  awk '
    BEGIN { braces = 0; brackets = 0; parens = 0; bad = 0 }
    {
      in_string = 0
      escaped = 0
      for (i = 1; i <= length($0); i++) {
        c = substr($0, i, 1)
        if (escaped) { escaped = 0; continue }
        if (in_string && c == "\\") { escaped = 1; continue }
        if (in_string && c == "$" && substr($0, i + 1, 1) == "\"") {
          printf "%s:%d:%d: unescaped $ before closing quote\n", FILENAME, NR, i > "/dev/stderr"
          bad = 1
        }
        if (c == "\"") { in_string = !in_string; continue }
        if (!in_string && c == "#") { break }
        if (!in_string && c == "!") {
          j = i + 1
          while (j <= length($0) && substr($0, j, 1) ~ /[ \t]/) j++
          if (substr($0, j, 1) == "~") {
            printf "%s:%d:%d: unsupported !~ operator; negate a parenthesized ~ expression\n", FILENAME, NR, i > "/dev/stderr"
            bad = 1
          }
        }
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
  ' "$script"
}

while IFS= read -r -d '' script; do
  if ! routeros_parse_safety_contract "$script"; then
    failed=1
  fi
done < <(find "$rsc_root" -maxdepth 1 -type f -name '*.rsc' -print0)

if rg -n -g '*.rsc' -g '!foxos-dns-apply.rsc' -g '!uninstall-apply.rsc' '^[[:space:]]*/ip/(dns|route|firewall|dhcp-server/network)(/|[[:space:]])[^#]*(add|set|remove|enable|disable|reset|move)' "$rsc_root"; then
  report "deployment scripts contain a forbidden DNS/DHCP route/firewall write"
fi
if [[ "$(rg -n '^[[:space:]]*/ip/dns/static add name=\$hostname type=A address=\$dnsAddress ttl=5m comment="foxos:dns:admin"$' "$rsc_root/foxos-dns-apply.rsc" | wc -l | tr -d ' ')" != 1 ]]; then
  report "the confirmed DNS script must contain exactly one bounded owned-record write"
fi
if [[ "$(rg -n '^[[:space:]]*/ip/dns/static/remove \$dnsRecord$' "$rsc_root/uninstall-apply.rsc" | wc -l | tr -d ' ')" != 1 ]] || ! rg -Fq 'comment="foxos:dns:admin"' "$rsc_root/uninstall-apply.rsc"; then
  report "uninstall must remove at most the exact confirmed foxos:dns:admin record"
fi
if rg -n -g '*.rsc' '^[[:space:]]*/system/device-mode/update' "$rsc_root"; then
  report "deployment scripts must not change device-mode"
fi
if rg -n -g '*.rsc' 'file=[^[:space:]]*/\$[A-Za-z_]' "$rsc_root"; then
  report "found an invalid RouterOS path expression"
fi
if rg -n -F -g '*.rsc' '".."' "$rsc_root"; then
  report "found a RouterOS parser-hostile double-dot string literal"
fi
if rg -n -g '*.rsc' 'architecture[^#]*!=[^#]*"x86"' "$rsc_root"; then
  report "found a stale direct architecture-name=x86 rejection instead of amd64 normalization"
fi
if rg -n -g '*.rsc' '/container get \$[A-Za-z][A-Za-z0-9]* (\.id|status)\]|value-name=(\.id|status)' "$rsc_root"; then
  report "container identity or state uses unsupported RouterOS .id/status readback instead of native handles and dynamic flags"
fi
if ! file_native_handle_contract "$rsc_root"; then
  report "file identity uses unsupported RouterOS .id readback instead of native handles"
fi
if ! mount_native_handle_contract "$rsc_root"; then
  report "mount identity uses unsupported RouterOS .id readback instead of native handles"
fi
if ! install_env_native_handle_contract "$rsc_root/foxos-install-inspect.rsc"; then
  report "first-install env identity uses unsupported RouterOS .id readback instead of native handles"
fi
if rg -n -g '*.rsc' -g '!load-site-config.rsc' -g '!chr-envlists-smoke.rsc' '/container get \$[A-Za-z][A-Za-z0-9]* (root-dir|running|stopped)\]' "$rsc_root"; then
  report "container path or state bypasses the loader-owned compatibility contract"
fi
if rg -n -g '*.rsc' '/container/add[^#]*(foxos-mihomo|foxos-mosdns)[^#]*envlists=foxos-env' "$rsc_root"; then
  report "Mihomo or MosDNS would inherit FoxOS credentials"
fi
if ! rg -Fq 'envlists=foxos-mosdns-env' "$rsc_root/foxos-full-install.rsc" || ! rg -Fq 'key=MOSDNS_AUTO_INIT value="0"' "$rsc_root/foxos-full-install.rsc"; then
  report "MosDNS external auto-initialization is not disabled"
fi
for envlist_script in foxos-full-install.rsc foxos-install-inspect.rsc foxos-start-all.rsc foxos-verify.rsc foxos-uninstall-inspect.rsc uninstall-apply.rsc upgrade-inspect.rsc upgrade.rsc upgrade-promote-inspect.rsc upgrade-promote.rsc rollback-inspect.rsc rollback.rsc upgrade-cleanup-inspect.rsc; do
  if ! container_envlist_contract "$rsc_root/$envlist_script"; then
    report "$envlist_script does not use the RouterOS 7.21+ envlists property consistently"
  fi
done
if rg -n -g '*.rsc' '/container/mounts (add|find)[^#]*(name=|where name)' "$rsc_root"; then
  report "RouterOS named mounts must use the official list property, not name"
fi
if rg -n -g '*.rsc' '/container/mounts (add|get)[^#]*read-only' "$rsc_root"; then
  report "RouterOS named mounts must use mode=rw and the loader-owned mode getter, not read-only"
fi
if rg -n -g '*.rsc' -g '!load-site-config.rsc' -g '!chr-envlists-smoke.rsc' '/container/mounts get \$[A-Za-z][A-Za-z0-9]* mode\]' "$rsc_root"; then
  report "deployment scripts bypass the loader-owned mount mode compatibility contract"
fi
if rg -n -g '*.rsc' -g '!load-site-config.rsc' -g '!chr-envlists-smoke.rsc' '/container/mounts get \$[A-Za-z][A-Za-z0-9]* src\]' "$rsc_root"; then
  report "deployment scripts bypass the loader-owned mount source normalization contract"
fi
for mount_list_script in foxos-full-install.rsc foxos-install-inspect.rsc foxos-uninstall-inspect.rsc uninstall-apply.rsc upgrade-inspect.rsc upgrade-promote-inspect.rsc rollback-inspect.rsc upgrade-cleanup-inspect.rsc preflight.rsc; do
  if ! container_mount_list_contract "$rsc_root/$mount_list_script"; then
    report "$mount_list_script does not use the RouterOS mount list property consistently"
  fi
done
if ! full_install_mount_contract "$rsc_root/foxos-full-install.rsc"; then
  report "full install does not create and read back every named mount as mode=rw"
fi
for mount_readback_script in foxos-install-inspect.rsc foxos-uninstall-inspect.rsc uninstall-apply.rsc upgrade-inspect.rsc upgrade-promote-inspect.rsc rollback-inspect.rsc upgrade-cleanup-inspect.rsc; do
  if ! container_mount_readback_contract "$rsc_root/$mount_readback_script"; then
    report "$mount_readback_script does not bind named mounts to normalized source and mode=rw"
  fi
done
for mount_digest_script in foxos-install-inspect.rsc foxos-uninstall-inspect.rsc upgrade-inspect.rsc upgrade-promote-inspect.rsc rollback-inspect.rsc upgrade-cleanup-inspect.rsc; do
  if ! mount_digest_source_contract "$rsc_root/$mount_digest_script"; then
    report "$mount_digest_script does not bind normalized mount source into plan evidence"
  fi
done
for lifecycle_mount_script in foxos-start-all.rsc upgrade-promote.rsc rollback.rsc; do
  if ! lifecycle_shared_mount_contract "$rsc_root/$lifecycle_mount_script"; then
    report "$lifecycle_mount_script does not verify all shared mount IDs, paths, and RW state before its first container mutation"
  fi
done
for identity_script in foxos-full-install.rsc foxos-install-inspect.rsc foxos-start-all.rsc foxos-verify.rsc foxos-uninstall-inspect.rsc uninstall-apply.rsc upgrade-inspect.rsc upgrade.rsc upgrade-promote-inspect.rsc upgrade-promote.rsc rollback-inspect.rsc rollback.rsc upgrade-cleanup-inspect.rsc; do
  if ! container_identity_fields_contract "$rsc_root/$identity_script"; then
    report "$identity_script does not bind container interface/env/mount/root/start/logging fields"
  fi
done
if ! container_compatibility_contract "$rsc_root/load-site-config.rsc"; then
  report "the immutable loader does not own the RouterOS native-handle, dynamic-state, and root-dir normalization contract"
fi
if ! mount_compatibility_contract "$rsc_root/load-site-config.rsc"; then
  report "the immutable loader does not own the RouterOS named-mount source and mode compatibility contract"
fi
for compatibility_script in foxos-full-install.rsc foxos-install-inspect.rsc foxos-start-all.rsc foxos-verify.rsc foxos-uninstall-inspect.rsc uninstall-apply.rsc upgrade-inspect.rsc upgrade.rsc upgrade-promote-inspect.rsc upgrade-promote.rsc rollback-inspect.rsc rollback.rsc upgrade-cleanup-inspect.rsc upgrade-cleanup-apply.rsc; do
  if ! container_compatibility_consumer_contract "$rsc_root/$compatibility_script"; then
    report "$compatibility_script bypasses or does not require the loader-owned container compatibility contract"
  fi
done
for mount_compatibility_script in foxos-full-install.rsc foxos-install-inspect.rsc foxos-start-all.rsc foxos-uninstall-inspect.rsc uninstall-apply.rsc upgrade-inspect.rsc upgrade-promote-inspect.rsc upgrade-promote.rsc rollback-inspect.rsc rollback.rsc upgrade-cleanup-inspect.rsc; do
  if ! mount_compatibility_consumer_contract "$rsc_root/$mount_compatibility_script"; then
    report "$mount_compatibility_script bypasses or does not require the loader-owned mount compatibility contract"
  fi
done
if ! admin_container_logging_contract "$rsc_root"; then
  report "FoxOS admin container lifecycle does not enforce logging=no while preserving auxiliary diagnostics"
fi
if ! uninstall_snapshot_contract "$rsc_root/uninstall-apply.rsc"; then
  report "uninstall apply does not freeze the inspector-approved RouterOS object IDs before its first write"
fi
if ! uninstall_predelete_contract "$rsc_root/uninstall-apply.rsc"; then
  report "uninstall apply does not revalidate the approved digest after snapshot or remove only snapshot-bound IDs"
fi
for endpoint in \
  'FOXOS_MIHOMO_PROXY_URL|http://' \
  'FOXOS_MOSDNS_URL|http://' \
  'FOXOS_SITE_PUBLIC_HOSTNAME|' \
  'FOXOS_HTTPS_ENABLED|true'; do
  if ! rg -Fq "$endpoint" "$rsc_root/foxos-full-install.rsc"; then
    report "missing FoxOS runtime endpoint: $endpoint"
  fi
done
if ! rg -Fq '!= $expectedEnvCount' "$rsc_root/foxos-full-install.rsc" || ! rg -Fq 'FOXOS_SUBSCRIPTION_PRIVATE_CIDRS' "$rsc_root/foxos-full-install.rsc"; then
	report "FoxOS env allowlist does not enforce the 27-key baseline plus the reviewed optional private-subscription key"
fi
if ! rg -Fq 'address] != $routerCIDR' "$rsc_root/preflight.rsc"; then
  report "preflight does not require the exact RouterOS management prefix"
fi
for rest_www_script in foxos-doctor.rsc preflight.rsc foxos-full-install.rsc; do
  if ! rest_www_static_service_contract "$rsc_root/$rest_www_script"; then
    report "$rest_www_script can confuse dynamic www connection rows with the static RouterOS REST service"
  fi
done
if ! rest_address_contract "$rsc_root/preflight.rsc"; then
  report "preflight does not parse the RouterOS REST address list as an exact allowlist"
fi
for rest_positive in '10.0.0.0/24' '10.0.0.4/32' '10.0.0.0/24,10.0.0.4/32'; do
  if ! rest_address_model "$rest_positive" '10.0.0.0/24' '10.0.0.4/32'; then
    report "REST address model rejected allowed case $rest_positive"
  fi
done
for rest_negative in '0.0.0.0/0' '0.0.0.0/0,10.0.0.0/24' '10.0.0.0/24,203.0.113.0/24' '10.0.0.0/24,10.0.0.0/24'; do
  if rest_address_model "$rest_negative" '10.0.0.0/24' '10.0.0.4/32'; then
    report "REST address model accepted forbidden mixed or broad case $rest_negative"
  fi
done

while IFS= read -r -d '' script; do
  case "$(basename "$script")" in
    site-config.example.rsc|chr-envlists-smoke.rsc) continue ;;
  esac
  if ! rg -Fq 'FoxOSSiteManifestVersion' "$script"; then
    report "script does not require the imported site manifest: $(basename "$script")"
  fi
done < <(find "$rsc_root" -maxdepth 1 -type f -name '*.rsc' -print0)

if rg -n -g '*.rsc' -g '!site-config.example.rsc' '10\.0\.0\.[1-4]|bridge-lan|disk1' "$rsc_root"; then
  report "site topology is duplicated outside site-config.example.rsc"
fi
for script in upgrade-promote.rsc rollback.rsc foxos-verify.rsc; do
  if rg -n '/tool/fetch' "$rsc_root/$script" | rg -v 'check-certificate=yes-without-crl'; then
    report "$script contains a fetch that can bypass local CA verification"
  fi
done
if rg -n -g '*.rsc' 'http://[^[:space:]"$]+/api/|http://[^[:space:]"$]+:8090' "$rsc_root"; then
  report "FoxOS API access over ordinary LAN HTTP remains in a RouterOS script"
fi
if rg -n '^[[:space:]]*(while .*tun0|ip (rule|route))' "$repo_root/mihomo/config/start.sh"; then
  report "Mihomo startup still invents an unverified transparent data plane"
fi

for invariant in \
  'foxos-mihomo-runtime|mihomo-config|/root/.config/mihomo' \
  'foxos-mihomo-config|mihomo-config|/data/mihomo' \
  'foxos-mosdns-runtime|mosdns-config|/cus/mosdns'; do
  if ! rg -Fq "$invariant" "$rsc_root/foxos-full-install.rsc"; then
    report "missing required mount contract: $invariant"
  fi
done

for installed_resource in 'containers foxos-mihomo' 'foxos-mosdns' 'initial admin slot foxos-initial' 'owned sequential-start scheduler/script'; do
  if ! rg -Fq "$installed_resource" "$rsc_root/foxos-plan.rsc"; then
    report "the read-only plan does not name the installed resource exactly: $installed_resource"
  fi
done
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
for invariant in \
  'value="foxos:applying"' \
  'value="foxos:mosdns:applying"' \
  'value="foxos:complete"' \
  'value="foxos:mosdns:complete"'; do
  if ! rg -Fq "$invariant" "$rsc_root/foxos-full-install.rsc"; then
    report "missing resumable install state transition: $invariant"
  fi
done
for invariant in \
  '"foxos:applying"' \
  '"foxos:complete"' \
  '"RESUME"' \
  'allowedEnvKeys'; do
  if ! rg -Fq "$invariant" "$rsc_root/foxos-install-inspect.rsc"; then
    report "install inspector cannot validate a resumable owned prefix: $invariant"
  fi
done
if ! retained_state_install_contract "$rsc_root/foxos-install-inspect.rsc"; then
  report "install inspector can rotate the confirmation key over retained FoxOS data or backups"
fi
if ! pending_journal_uninstall_contract "$rsc_root/foxos-uninstall-inspect.rsc" "$rsc_root/uninstall-apply.rsc"; then
  report "uninstall does not preserve containers, env and the confirmation key around a pending Mihomo apply journal"
fi
if ! chr_envlists_smoke_contract "$rsc_root/chr-envlists-smoke.rsc"; then
  report "CHR envlists smoke does not preserve confirmation, isolation, readback and zero-residual cleanup contracts"
fi
for architecture_script in preflight.rsc foxos-doctor.rsc foxos-full-install.rsc upgrade-inspect.rsc chr-envlists-smoke.rsc; do
  if ! amd64_architecture_contract "$rsc_root/$architecture_script"; then
    report "$architecture_script does not normalize RouterOS x86 and x86_64 to amd64 while rejecting unknown architectures"
  fi
done
for storage_script in preflight.rsc foxos-doctor.rsc foxos-full-install.rsc; do
  if ! reserved_internal_storage_contract "$rsc_root/$storage_script"; then
    report "$storage_script does not reserve exact root foxos for internal storage while retaining strict disk-slot validation"
  fi
done
if ! foxos_doctor_contract "$rsc_root/foxos-doctor.rsc"; then
  report "FoxOS doctor is not strictly read-only or does not preserve its output contract"
fi
for guide_invariant in \
  'foxos-backups/mihomo/.foxos-mihomo-apply.json' \
  '不能把保留的 `foxos-data` 或 `foxos-backups` 当作全新安装直接覆盖'; do
  if ! rg -Fq "$guide_invariant" "$rsc_root/QUICK-INSTALL.md"; then
    report "QUICK-INSTALL omits the retained-state key lifecycle invariant: $guide_invariant"
  fi
done

first_install_write=$(rg -n '^[[:space:]]*/(container/envs add|container/mounts add|user(/group)? add|file set|interface/veth add|interface/bridge/port add|container/add)' "$rsc_root/foxos-full-install.rsc" | head -n1 | cut -d: -f1)
install_inspection=$(rg -n '/foxos-install-inspect\.rsc' "$rsc_root/foxos-full-install.rsc" | head -n1 | cut -d: -f1)
install_confirmation=$(rg -n '\$confirmationDigest != \$approvedDigest' "$rsc_root/foxos-full-install.rsc" | head -n1 | cut -d: -f1)
if [[ -z "$first_install_write" || -z "$install_inspection" || -z "$install_confirmation" ]] || ((first_install_write <= install_inspection || first_install_write <= install_confirmation)); then
  report "the full installer can write before shared inspection and digest confirmation"
fi
if rg -n '^[[:space:]]*/(container(/[^[:space:]]+)?|user(/group)?|file|interface/(veth|bridge/port)|system/(script|scheduler))[[:space:]/]+(add|set|remove|enable|disable|start|stop)' \
  "$rsc_root/preflight.rsc" "$rsc_root/foxos-doctor.rsc" "$rsc_root/foxos-plan.rsc" "$rsc_root/foxos-install-inspect.rsc" \
  "$rsc_root/upgrade-inspect.rsc" "$rsc_root/upgrade-plan.rsc" \
  "$rsc_root/upgrade-promote-inspect.rsc" "$rsc_root/upgrade-promote-plan.rsc" \
  "$rsc_root/rollback-inspect.rsc" "$rsc_root/rollback-plan.rsc" \
  "$rsc_root/upgrade-cleanup-inspect.rsc" "$rsc_root/upgrade-cleanup-plan.rsc"; then
  report "the read-only preflight, plan, or inspector contains a RouterOS write"
fi
if rg -n '/tool/fetch[^#]*http-method=post' \
  "$rsc_root/upgrade-inspect.rsc" "$rsc_root/upgrade-plan.rsc" \
  "$rsc_root/upgrade-promote-inspect.rsc" "$rsc_root/upgrade-promote-plan.rsc" \
  "$rsc_root/rollback-inspect.rsc" "$rsc_root/rollback-plan.rsc" \
  "$rsc_root/upgrade-cleanup-inspect.rsc" "$rsc_root/upgrade-cleanup-plan.rsc"; then
  report "a read-only upgrade or rollback plan writes application state"
fi
full_scheduler_gate=$(rg -n 'device-mode scheduler=yes 未启用' "$rsc_root/foxos-full-install.rsc" | head -n1 | cut -d: -f1)
if [[ -z "$full_scheduler_gate" || -z "$first_install_write" ]] || ((full_scheduler_gate >= first_install_write)); then
  report "the full installer can write before enforcing device-mode scheduler=yes"
fi
if ! rg -Fq '/system/device-mode get scheduler' "$rsc_root/preflight.rsc" \
  || ! rg -Fq 'ERROR device-mode scheduler=yes is required' "$rsc_root/preflight.rsc" \
  || ! scheduler_preflight_gate "$rsc_root/preflight.rsc"; then
  report "preflight cannot fail a container=yes/scheduler=no plan"
fi
if ! rg -Fq '/system/device-mode get scheduler' "$rsc_root/foxos-verify.rsc" || ! rg -Fq 'scheduler=yes 必须在验证' "$rsc_root/foxos-verify.rsc"; then
  report "verify does not recheck scheduler device-mode before enabling cold start"
fi
applying_marker_write=$(rg -n 'container/envs add list=foxos-env key=FOXOS_INSTALL_MARKER value="foxos:applying"' "$rsc_root/foxos-full-install.rsc" | cut -d: -f1)
complete_marker_write=$(rg -n 'container/envs set \$finalInstallMarker value="foxos:complete"' "$rsc_root/foxos-full-install.rsc" | cut -d: -f1)
last_resource_create=$(rg -n '^[[:space:]]*/(container/envs add|container/mounts add|user(/group)? add|file set|interface/veth add|interface/bridge/port add|container/add|system/script add|system/scheduler add)' "$rsc_root/foxos-full-install.rsc" | tail -n1 | cut -d: -f1)
if [[ -z "$applying_marker_write" || "$applying_marker_write" != "$first_install_write" ]]; then
  report "the first install write is not the recoverable applying marker"
fi
if [[ -z "$complete_marker_write" || -z "$last_resource_create" ]] || ((complete_marker_write <= last_resource_create)); then
  report "the install can mark complete before every managed resource is created or reused"
fi
if ! mosdns_env_contract "$rsc_root/foxos-install-inspect.rsc"; then
  report "the install inspector does not bind unknown MosDNS env keys into a FAIL summary"
fi
mosdns_recheck=$(rg -n 'currentMosDNSEnvItems \[/container/envs find where list="foxos-mosdns-env"\]' "$rsc_root/foxos-full-install.rsc" | head -n1 | cut -d: -f1)
mosdns_first_write=$(rg -n '^[[:space:]]*/container/envs add list=foxos-mosdns-env' "$rsc_root/foxos-full-install.rsc" | head -n1 | cut -d: -f1)
if [[ -z "$mosdns_recheck" || -z "$mosdns_first_write" ]] || ((mosdns_recheck >= mosdns_first_write)) || ! rg -Fq 'foxos-mosdns-env 出现未知键，拒绝在补写前继续' "$rsc_root/foxos-full-install.rsc"; then
  report "the installer does not recheck the exact MosDNS env allowlist before resuming writes"
fi
if ! rg -Fq 'configDigest [:convert $configContents transform=sha512 to=hex]' "$rsc_root/foxos-install-inspect.rsc" || ! rg -Fq ':" . $configDigest' "$rsc_root/foxos-install-inspect.rsc"; then
  report "the install plan material does not bind Mihomo configuration contents by SHA-512"
fi
for identity_material in \
  '[/container get $containerID interface]' \
  '[/container get $containerID envlists]' \
  '[/container get $containerID mountlists]' \
  '[/container get $containerID logging]'; do
  if ! rg -Fq "$identity_material" "$rsc_root/foxos-uninstall-inspect.rsc"; then
    report "the uninstall digest material omits container identity field: $identity_material"
  fi
done
if ! rg -Fq '[$FoxOSMountMode $mountID])' "$rsc_root/foxos-install-inspect.rsc" \
  || ! rg -Fq '[$FoxOSMountMode $mountID])' "$rsc_root/foxos-uninstall-inspect.rsc" \
  || ! rg -Fq '[$FoxOSMountSource $mountID]' "$rsc_root/foxos-install-inspect.rsc" \
  || ! rg -Fq '[$FoxOSMountSource $mountID]' "$rsc_root/foxos-uninstall-inspect.rsc"; then
  report "install or uninstall digest material does not bind normalized mount source and mode"
fi

for invariant in \
  '__FOXOS_RELEASE_ID__' \
  'pendingName ("foxos-" . $releaseID)' \
  'foxos-upgrade-' \
  '已有即时 rollback 或 rollback-complete；先执行确认式 cleanup'; do
  if ! rg -Fq "$invariant" "$rsc_root/upgrade-inspect.rsc"; then
    report "versioned upgrade invariant is missing: $invariant"
  fi
done
for invariant in 'root-dir=$pendingRoot' 'FoxOSUpgradeConfirmation' 'FoxOSUpgradeActiveID' 'FoxOSUpgradeImageID'; do
  if ! rg -Fq "$invariant" "$rsc_root/upgrade.rsc"; then
    report "confirmed upgrade apply invariant is missing: $invariant"
  fi
done
if rg -n 'foxos-next|containers/foxos-next' "$rsc_root"; then
  report "fixed one-shot upgrade slot remains"
fi
if ! upgrade_marker_contract "$rsc_root/upgrade-inspect.rsc"; then
  report "upgrade must require the complete install marker"
fi
if rg -n 'key="FOXOS_INSTALL_MARKER"[^#]*value="foxos"' "$rsc_root"; then
  report "a lifecycle script still accepts the legacy unqualified install marker"
fi
for script in upgrade-inspect.rsc upgrade-plan.rsc upgrade.rsc upgrade-promote-inspect.rsc upgrade-promote-plan.rsc upgrade-promote.rsc rollback-inspect.rsc rollback-plan.rsc rollback.rsc upgrade-cleanup-inspect.rsc upgrade-cleanup-plan.rsc upgrade-cleanup-apply.rsc uninstall-plan.rsc uninstall-apply.rsc foxos-uninstall-inspect.rsc; do
  [[ -s "$rsc_root/$script" ]] || report "missing lifecycle script: $script"
done
for lifecycle_contract in \
  'upgrade.rsc|upgrade-inspect.rsc|FoxOSUpgradeCurrentDigest|FoxOSUpgradeApprovedDigest|FoxOSUpgradeConfirmation' \
  'upgrade-promote.rsc|upgrade-promote-inspect.rsc|FoxOSUpgradePromoteCurrentDigest|FoxOSUpgradePromoteApprovedDigest|FoxOSUpgradePromoteConfirmation' \
  'rollback.rsc|rollback-inspect.rsc|FoxOSRollbackCurrentDigest|FoxOSRollbackApprovedDigest|FoxOSRollbackConfirmation' \
  'upgrade-cleanup-apply.rsc|upgrade-cleanup-inspect.rsc|FoxOSUpgradeCleanupCurrentDigest|FoxOSUpgradeCleanupApprovedDigest|FoxOSUpgradeCleanupConfirmation'; do
  IFS='|' read -r apply_script inspector_script current_global approved_global confirmation_global <<< "$lifecycle_contract"
  if ! confirmed_lifecycle_contract "$rsc_root/$apply_script" "$inspector_script" "$current_global" "$approved_global" "$confirmation_global"; then
    report "$apply_script can write without a fresh inspector digest, object snapshot, and one-time confirmation"
  fi
done
for snapshot_invariant in \
  'upgrade.rsc|FoxOSUpgradeActiveID' \
  'upgrade.rsc|FoxOSUpgradeImageID' \
  'upgrade-promote.rsc|FoxOSUpgradePromoteActiveID' \
  'upgrade-promote.rsc|FoxOSUpgradePromotePendingID' \
  'upgrade-promote.rsc|FoxOSUpgradePromoteRollbackID' \
  'rollback.rsc|FoxOSRollbackActiveID' \
  'rollback.rsc|FoxOSRollbackSlotID' \
  'upgrade-cleanup-apply.rsc|FoxOSUpgradeCleanupActiveID' \
  'upgrade-cleanup-apply.rsc|FoxOSUpgradeCleanupRollbackID'; do
  IFS='|' read -r apply_script snapshot_name <<< "$snapshot_invariant"
  if [[ "$(rg -F "$snapshot_name" "$rsc_root/$apply_script" | wc -l | tr -d ' ')" -lt 3 ]]; then
    report "$apply_script does not freeze and revalidate lifecycle snapshot $snapshot_name"
  fi
done
if ! cleanup_inspector_contract "$rsc_root/upgrade-cleanup-inspect.rsc"; then
  report "upgrade cleanup inspector does not bind the complete stable RouterOS state"
fi
if ! cleanup_apply_snapshot_contract "$rsc_root/upgrade-cleanup-apply.rsc"; then
  report "upgrade cleanup apply does not re-inspect after confirmation or write only the frozen rollback ID"
fi
if [[ "$(rg -F '/upgrade-cleanup-inspect.rsc")' "$rsc_root/upgrade-cleanup-plan.rsc" | wc -l | tr -d ' ')" != 1 ]]; then
  report "upgrade cleanup plan does not use the shared versioned inspector exactly once"
fi
last_uninstall_snapshot=$(rg -n ':local backupsMountSnapshot ' "$rsc_root/uninstall-apply.rsc" | head -n1 | cut -d: -f1)
first_uninstall_resource_write=$(rg -n '^[[:space:]]*/(system/(scheduler|script)|container(/(envs|mounts))?|ip/dns/static|interface/(bridge/port|veth)|user(/group)?)[/[:space:]]+(set|remove|stop)' "$rsc_root/uninstall-apply.rsc" | head -n1 | cut -d: -f1)
if [[ -z "$last_uninstall_snapshot" || -z "$first_uninstall_resource_write" ]] || ((last_uninstall_snapshot >= first_uninstall_resource_write)); then
  report "uninstall apply can mutate RouterOS before freezing every approved object ID"
fi
for invariant in \
  'start-on-boot=no' \
  'startedMihomo' \
  'startedMosDNS' \
  '已停止并回读本次启动的前序容器'; do
  if ! rg -Fq "$invariant" "$rsc_root/foxos-start-all.rsc"; then
    report "start-all retry/compensation invariant is missing: $invariant"
  fi
done
for script in foxos-start-all.rsc upgrade-promote.rsc rollback.rsc; do
  if ! container_commands_guarded start "$rsc_root/$script"; then
    report "$script contains a synchronous container start outside :onerror"
  fi
  if ! container_commands_guarded stop "$rsc_root/$script"; then
    report "$script contains a synchronous container stop outside :onerror"
  fi
  stop_count=$(rg -c '/container/stop[[:space:]]' "$rsc_root/$script" || true)
  stop_poll_count=$(rg -c ':for attempt from=1 to=12' "$rsc_root/$script" || true)
  stop_timeout_count=$(rg -c '60 秒内未停|60 秒内全部停稳' "$rsc_root/$script" || true)
  if ((stop_count < 1 || stop_poll_count < stop_count || stop_timeout_count != stop_count)); then
    report "$script does not bind every stop path to a maximum 60-second stopped readback"
  fi
done
if rg -n -g '*.rsc' 'start-on-boot=yes' "$rsc_root"; then
  report "individual containers still use unordered cold-boot autostart"
fi
if ! transition_contract "$rsc_root/upgrade-promote.rsc" "$rsc_root/rollback.rsc" "$rsc_root/foxos-start-all.rsc"; then
  report "upgrade and rollback do not expose a complete power-loss recovery state machine"
fi
for cidr_script in load-site-config.rsc preflight.rsc; do
  if ! routeros_private_cidr_contract "$rsc_root/$cidr_script"; then
    report "$cidr_script does not validate IPv4 CIDRs from address and netmask values while preserving IPv6 :toip6 validation"
  fi
done
if ! routeros_site_ipv4_cidr_contract "$rsc_root/preflight.rsc"; then
  report "preflight.rsc depends on RouterOS converting an IPv4 CIDR to ip-prefix or does not enforce bitmask membership"
fi
if ! routeros_find_filter_variable_collision_free "$rsc_root"/*.rsc; then
  report "RouterOS find filters reuse a property name as the variable name"
fi
for arp_script in preflight.rsc foxos-install-inspect.rsc foxos-full-install.rsc; do
  if ! routeros_arp_occupancy_contract "$rsc_root/$arp_script"; then
    report "$arp_script treats failed dynamic ARP probes as occupied addresses"
  fi
done
for hostname_script in load-site-config.rsc preflight.rsc; do
  if ! routeros_hostname_contract "$rsc_root/$hostname_script"; then
    report "$hostname_script does not enforce the RouterOS hostname length, suffix, and per-label contract"
  fi
done
for invariant in \
  '/system/script add name=foxos-start-sequence' \
  '/system/scheduler add name=foxos-start-sequence' \
  'disabled=yes comment="foxos:start-sequence"'; do
  if ! rg -Fq "$invariant" "$rsc_root/foxos-full-install.rsc"; then
    report "full install is missing the disabled owned cold-boot coordinator: $invariant"
  fi
done
if ! rg -Fq '/system/scheduler set $startScheduler disabled=no' "$rsc_root/foxos-verify.rsc" || ! rg -Fq 'Mihomo, then MosDNS, then FoxOS' "$rsc_root/foxos-verify.rsc"; then
  report "verify does not enable the ordered scheduler after runtime gates"
fi
for scheduler_script in foxos-install-inspect.rsc foxos-full-install.rsc foxos-verify.rsc foxos-uninstall-inspect.rsc uninstall-apply.rsc upgrade-inspect.rsc upgrade-promote-inspect.rsc rollback-inspect.rsc upgrade-cleanup-inspect.rsc; do
  if ! scheduler_contract "$rsc_root/$scheduler_script"; then
    report "$scheduler_script does not bind the owned scheduler to interval=0s and policy=read,write,test"
  fi
done
for policy_script in foxos-install-inspect.rsc foxos-full-install.rsc foxos-verify.rsc foxos-uninstall-inspect.rsc uninstall-apply.rsc upgrade-inspect.rsc upgrade-promote-inspect.rsc rollback-inspect.rsc upgrade-cleanup-inspect.rsc; do
  if ! system_script_policy_contract "$rsc_root/$policy_script"; then
    report "$policy_script does not require the owned system script policy to equal read,write,test"
  fi
done
for script in foxos-install-inspect.rsc foxos-full-install.rsc foxos-verify.rsc foxos-uninstall-inspect.rsc uninstall-apply.rsc upgrade-inspect.rsc upgrade-promote-inspect.rsc rollback-inspect.rsc upgrade-cleanup-inspect.rsc; do
  if ! rg -Fq 'load-site-config.rsc; /import file-name=' "$rsc_root/$script"; then
    report "$script does not bind the cold-boot coordinator to loader then start-all"
  fi
done
for lifecycle_inspector in upgrade-inspect.rsc upgrade-promote-inspect.rsc rollback-inspect.rsc upgrade-cleanup-inspect.rsc; do
  if ! lifecycle_start_sequence_contract "$rsc_root/$lifecycle_inspector"; then
    report "$lifecycle_inspector does not bind the unique enabled cold-start script and scheduler IDs into its approval digest"
  fi
done
if ! rg -Fq 'PRESERVE all files under' "$rsc_root/uninstall-plan.rsc" || ! rg -Fq 'FoxOSUninstallConfirmation' "$rsc_root/uninstall-apply.rsc"; then
  report "uninstall is not digest-confirmed with persistent data retained by default"
fi
for invariant in 'DONE containers' 'FoxOSUninstallRemainingCount' 'ambiguous or non-owned conflict'; do
  if ! rg -Fq "$invariant" "$rsc_root/foxos-uninstall-inspect.rsc"; then
    report "uninstall inspection cannot converge from a partial delete: $invariant"
  fi
done
if ! rg -Fq 'if ($FoxOSUninstallRemainingCount != 0)' "$rsc_root/uninstall-apply.rsc" || ! rg -Fq 'Missing resources above are DONE' "$rsc_root/uninstall-plan.rsc"; then
  report "uninstall apply does not verify convergence to an empty remaining set"
fi
if rg -n -g '*.rsc' 'FoxOSSiteManifestVersion != 1|FoxOSSiteManifestVersion 1' "$rsc_root"; then
  report "legacy site manifest version remains"
fi
if ! rg -Fq 'FoxOSSiteSubscriptionPrivateCIDRs' "$rsc_root/preflight.rsc" || ! rg -Fq 'currentExpectedDigest != $FoxOSSiteLoadedDigest' "$rsc_root/preflight.rsc" || ! rg -Fq 'currentActualDigest != $FoxOSSiteLoadedDigest' "$rsc_root/preflight.rsc"; then
  report "preflight does not bind the loader proof to the current sealed manifest"
fi
if rg -n '/import[^#]*site-config\.rsc' "$rsc_root/load-site-config.rsc"; then
  report "the immutable loader executes the editable site manifest"
fi
if rg -n '/import[^#\n]*site-config\.rsc' \
  "$repo_root/README.md" "$repo_root/deploy/routeros/QUICK-INSTALL.md" "$repo_root/docs" \
  | rg -v 'load-site-config\.rsc'; then
  report "deployment documentation bypasses the immutable site-config loader"
fi
quick_install="$rsc_root/QUICK-INSTALL.md"
if ! quick_install_upload_manifest_contract "$quick_install"; then
  report "standalone QUICK-INSTALL upload manifest does not exactly match the release bundle contract"
fi
if ! quick_install_collision_manifest_contract "$quick_install"; then
  report "standalone QUICK-INSTALL collision allowlist does not exactly cover every top-level upload target"
fi
if ! quick_install_download_contract "$quick_install"; then
  report "standalone QUICK-INSTALL does not provide independent executable Core CI and GitHub Release verification paths"
fi
if ! quick_install_preupload_safety_contract "$quick_install"; then
  report "standalone QUICK-INSTALL can upload before backups are downloaded and every target is proven collision-free"
fi
if ! upgrade_promote_api_contract "$rsc_root/upgrade-promote.rsc"; then
  report "upgrade promote does not preserve idempotent checkpoint/promote/abort recovery ordering"
fi
for invariant in \
  'disk1/QUICK-INSTALL.md' \
  '/console/inspect request=completion input="/container/add "' \
  'APPROVED UPGRADE SHA-512' \
  'APPROVED PROMOTE SHA-512' \
  'APPROVED ROLLBACK SHA-512' \
  'APPROVED CLEANUP SHA-512' \
  'upgrade-cleanup-apply.rsc'; do
  if ! rg -Fq -- "$invariant" "$quick_install"; then
    report "standalone QUICK-INSTALL is missing a required deployment step: $invariant"
  fi
done
if rg -n '\.\./\.\./docs/' "$quick_install"; then
  report "standalone QUICK-INSTALL links to documentation outside the release bundle"
fi
for invariant in \
  'actualDigest [:convert $configContents transform=sha512 to=hex]' \
  'site-config.rsc 必须且只能包含 11 个指定赋值各一次' \
  'FoxOSSiteLoadedDigest $actualDigest' \
  'FoxOSSiteLoaderVersion 1'; do
  if ! rg -Fq "$invariant" "$rsc_root/load-site-config.rsc"; then
    report "site loader is missing an assignment-only integrity invariant: $invariant"
  fi
done
if ! site_loader_cursor_contract "$rsc_root/load-site-config.rsc"; then
  report "site loader does not skip a cursor-position LF before the exclusive-start line search"
fi

site_seal_root=$(mktemp -d "${TMPDIR:-/tmp}/foxos-site-seal-check.XXXXXX")
trap 'rm -rf -- "$site_seal_root"' EXIT
parser_unsafe_dollar_fixture="$site_seal_root/routeros-unescaped-dollar.rsc"
parser_safe_dollar_fixture="$site_seal_root/routeros-escaped-dollar.rsc"
printf '%s\n' ':put "anchor$"' > "$parser_unsafe_dollar_fixture"
printf '%s\n' ':put "anchor\$"' > "$parser_safe_dollar_fixture"
if routeros_parse_safety_contract "$parser_unsafe_dollar_fixture" 2>/dev/null; then
  report "RouterOS parser safety accepted an unescaped dollar before a closing quote"
fi
if ! routeros_parse_safety_contract "$parser_safe_dollar_fixture"; then
  report "RouterOS parser safety rejected an escaped literal dollar"
fi
parser_unsafe_negated_regex_fixture="$site_seal_root/routeros-unsafe-negated-regex.rsc"
parser_safe_negated_regex_fixture="$site_seal_root/routeros-safe-negated-regex.rsc"
printf '%s\n' ':local value "abc"; :if ($value !~ "^a") do={ :put "bad" }' > "$parser_unsafe_negated_regex_fixture"
printf '%s\n' ':local value "abc"; :if (!($value ~ "^a")) do={ :put "good" }' > "$parser_safe_negated_regex_fixture"
if routeros_parse_safety_contract "$parser_unsafe_negated_regex_fixture" 2>/dev/null; then
  report "RouterOS parser safety accepted the unsupported !~ operator"
fi
if ! routeros_parse_safety_contract "$parser_safe_negated_regex_fixture"; then
  report "RouterOS parser safety rejected a parenthesized negated regex match"
fi
ascii_fixture="$site_seal_root/routeros-utf8.rsc"
ascii_encoded="$site_seal_root/routeros-utf8-ascii.rsc"
printf ':put "\346\265\213\350\257\225"\n' > "$ascii_fixture"
if ! "$repo_root/scripts/encode-routeros-rsc-ascii.sh" "$ascii_fixture" "$ascii_encoded"; then
  report "RouterOS ASCII encoder rejected a valid UTF-8 script fixture"
elif LC_ALL=C rg -n '[^\x00-\x7F]' "$ascii_encoded" >/dev/null; then
  report "RouterOS ASCII encoder left non-ASCII bytes in its output"
elif ! rg -Fq ':put "\E6\B5\8B\E8\AF\95"' "$ascii_encoded"; then
  report "RouterOS ASCII encoder did not emit byte-preserving hex escapes"
fi
ascii_passthrough="$site_seal_root/routeros-ascii.rsc"
ascii_passthrough_encoded="$site_seal_root/routeros-ascii-encoded.rsc"
printf '%s\n' ':put "ASCII"' > "$ascii_passthrough"
if ! "$repo_root/scripts/encode-routeros-rsc-ascii.sh" "$ascii_passthrough" "$ascii_passthrough_encoded" \
  || ! cmp -s "$ascii_passthrough" "$ascii_passthrough_encoded"; then
  report "RouterOS ASCII encoder changed an already-ASCII script"
fi
loader_ascii_encoded="$site_seal_root/load-site-config-ascii.rsc"
if ! "$repo_root/scripts/encode-routeros-rsc-ascii.sh" "$rsc_root/load-site-config.rsc" "$loader_ascii_encoded"; then
  report "RouterOS ASCII encoder rejected the site loader"
elif ! site_loader_cursor_contract "$loader_ascii_encoded"; then
  report "the release-encoded site loader lost its exclusive-start cursor guard"
fi
for bundle_ascii_invariant in \
  'scripts/encode-routeros-rsc-ascii.sh' \
  "find \"\$stage_root\" -type f -name '*.rsc' -print0" \
  'RouterOS release scripts must be ASCII-only'; do
  if ! rg -Fq -- "$bundle_ascii_invariant" "$repo_root/scripts/build-routeros-bundle.sh"; then
    report "RouterOS bundle assembly does not enforce ASCII-only release scripts"
    break
  fi
done
admin_logging_add_negative_root="$site_seal_root/admin-logging-add-negative"
mkdir -p -- "$admin_logging_add_negative_root"
cp -- "$rsc_root"/*.rsc "$admin_logging_add_negative_root/"
sed '/name=foxos-initial .*interface=veth-foxos/s/logging=no/logging=yes/' \
  "$rsc_root/foxos-full-install.rsc" > "$admin_logging_add_negative_root/foxos-full-install.rsc"
if cmp -s "$rsc_root/foxos-full-install.rsc" "$admin_logging_add_negative_root/foxos-full-install.rsc"; then
  report "admin logging add failure injection did not mutate the initial FoxOS container"
elif admin_container_logging_contract "$admin_logging_add_negative_root"; then
  report "admin logging contract accepted a credential-bearing FoxOS container with logging=yes"
fi
admin_logging_readback_negative_root="$site_seal_root/admin-logging-readback-negative"
mkdir -p -- "$admin_logging_readback_negative_root"
cp -- "$rsc_root"/*.rsc "$admin_logging_readback_negative_root/"
sed 's/logging] != false/logging] != true/' \
  "$rsc_root/upgrade-inspect.rsc" > "$admin_logging_readback_negative_root/upgrade-inspect.rsc"
if cmp -s "$rsc_root/upgrade-inspect.rsc" "$admin_logging_readback_negative_root/upgrade-inspect.rsc"; then
  report "admin logging readback failure injection did not mutate upgrade-inspect"
elif admin_container_logging_contract "$admin_logging_readback_negative_root"; then
  report "admin logging contract accepted a lifecycle inspector that requires logging=yes"
fi
cp -- "$rsc_root/site-config.example.rsc" "$site_seal_root/site-config.rsc"
if ! "$rsc_root/seal-site-config.sh" "$site_seal_root/site-config.rsc" >/dev/null; then
  report "the immutable site manifest example cannot be sealed"
elif [[ "$(wc -c < "$site_seal_root/site-config.rsc.sha512" | tr -d ' ')" != 129 ]]; then
  report "the site manifest seal is not a SHA-512 hex digest"
elif ! site_loader_manifest_model "$site_seal_root/site-config.rsc"; then
  report "the site loader cursor model rejected the default manifest layout"
fi
mkdir -p -- "$site_seal_root/blank-lines" "$site_seal_root/no-trailing-newline"
awk '
  /^:global FoxOSSiteManifestVersion 2$/ && !inserted { print ""; print ""; inserted = 1 }
  { print }
' "$rsc_root/site-config.example.rsc" > "$site_seal_root/blank-lines/site-config.rsc"
if ! "$rsc_root/seal-site-config.sh" "$site_seal_root/blank-lines/site-config.rsc" >/dev/null \
  || ! site_loader_manifest_model "$site_seal_root/blank-lines/site-config.rsc"; then
  report "the site loader cursor model rejected consecutive blank lines"
fi
perl -0pe 's/\n\z//' "$rsc_root/site-config.example.rsc" > "$site_seal_root/no-trailing-newline/site-config.rsc"
if ! "$rsc_root/seal-site-config.sh" "$site_seal_root/no-trailing-newline/site-config.rsc" >/dev/null \
  || ! site_loader_manifest_model "$site_seal_root/no-trailing-newline/site-config.rsc"; then
  report "the site loader cursor model rejected a manifest without a trailing LF"
fi
for private_cidr in 'fc00::/7' 'fd12:3456::/48'; do
  case_name=$(printf '%s' "$private_cidr" | tr ':/' '__')
  mkdir -p -- "$site_seal_root/private-positive-$case_name"
  sed "s#FoxOSSiteSubscriptionPrivateCIDRs \"\"#FoxOSSiteSubscriptionPrivateCIDRs \"$private_cidr\"#" \
    "$rsc_root/site-config.example.rsc" > "$site_seal_root/private-positive-$case_name/site-config.rsc"
  if ! "$rsc_root/seal-site-config.sh" "$site_seal_root/private-positive-$case_name/site-config.rsc" >/dev/null; then
    report "the site manifest seal rejected canonical ULA prefix $private_cidr"
  fi
done
hostname_label_63=$(printf 'a%.0s' {1..63})
hostname_label_64=$(printf 'a%.0s' {1..64})
hostname_total_253="$(printf 'a%.0s' {1..63}).$(printf 'b%.0s' {1..63}).$(printf 'c%.0s' {1..63}).$(printf 'd%.0s' {1..51}).home.arpa"
hostname_total_254="$(printf 'a%.0s' {1..63}).$(printf 'b%.0s' {1..63}).$(printf 'c%.0s' {1..63}).$(printf 'd%.0s' {1..52}).home.arpa"
hostname_positive_cases=(
  'short|a.home.arpa'
  "label-63|${hostname_label_63}.home.arpa"
  "total-253|${hostname_total_253}"
)
for hostname_case in "${hostname_positive_cases[@]}"; do
  IFS='|' read -r case_name public_hostname <<< "$hostname_case"
  mkdir -p -- "$site_seal_root/hostname-positive-$case_name"
  sed "s#FoxOSSitePublicHostname \"[^\"]*\"#FoxOSSitePublicHostname \"$public_hostname\"#" \
    "$rsc_root/site-config.example.rsc" > "$site_seal_root/hostname-positive-$case_name/site-config.rsc"
  if ! "$rsc_root/seal-site-config.sh" "$site_seal_root/hostname-positive-$case_name/site-config.rsc" >/dev/null; then
    report "the site manifest seal rejected valid hostname case $case_name"
  fi
done
non_ascii_hostname=$(printf '\303\251.home.arpa')
hostname_negative_cases=(
  'empty-label|foxos..home.arpa'
  "label-64|${hostname_label_64}.home.arpa"
  "total-254|${hostname_total_254}"
  'leading-hyphen|-foxos.home.arpa'
  'trailing-hyphen|foxos-.home.arpa'
  'uppercase|Foxos.home.arpa'
  'wrong-suffix|foxos.example'
  "non-ascii|${non_ascii_hostname}"
)
for hostname_case in "${hostname_negative_cases[@]}"; do
  IFS='|' read -r case_name public_hostname <<< "$hostname_case"
  mkdir -p -- "$site_seal_root/hostname-negative-$case_name"
  sed "s#FoxOSSitePublicHostname \"[^\"]*\"#FoxOSSitePublicHostname \"$public_hostname\"#" \
    "$rsc_root/site-config.example.rsc" > "$site_seal_root/hostname-negative-$case_name/site-config.rsc"
  if "$rsc_root/seal-site-config.sh" "$site_seal_root/hostname-negative-$case_name/site-config.rsc" >/dev/null 2>&1; then
    report "the site manifest seal accepted invalid hostname case $case_name"
  fi
done
private_cidr_negative_cases=(
  'link-local|fe80::/10'
  'documentation-public|2001:db8::/32'
  'noncanonical-ula|fd12:3456::1/48'
  'uppercase-duplicate|fd12:3456::/48,FD12:3456::/48'
  'empty-entry|10.0.0.0/8,,192.168.0.0/16'
)
too_many_private_cidrs=""
for private_index in $(seq 0 32); do
  [[ -z "$too_many_private_cidrs" ]] || too_many_private_cidrs+=","
  too_many_private_cidrs+="10.0.${private_index}.0/24"
done
private_cidr_negative_cases+=("over-32|$too_many_private_cidrs")
for private_case in "${private_cidr_negative_cases[@]}"; do
  IFS='|' read -r case_name private_cidr <<< "$private_case"
  mkdir -p -- "$site_seal_root/private-negative-$case_name"
  sed "s#FoxOSSiteSubscriptionPrivateCIDRs \"\"#FoxOSSiteSubscriptionPrivateCIDRs \"$private_cidr\"#" \
    "$rsc_root/site-config.example.rsc" > "$site_seal_root/private-negative-$case_name/site-config.rsc"
  if "$rsc_root/seal-site-config.sh" "$site_seal_root/private-negative-$case_name/site-config.rsc" >/dev/null 2>&1; then
    report "the site manifest seal accepted invalid private CIDR case $case_name"
  fi
done
mkdir -p -- "$site_seal_root/duplicate"
cp -- "$rsc_root/site-config.example.rsc" "$site_seal_root/duplicate/site-config.rsc"
printf '%s\n' ':global FoxOSSitePublicHostname "duplicate.home.arpa"' >> "$site_seal_root/duplicate/site-config.rsc"
if "$rsc_root/seal-site-config.sh" "$site_seal_root/duplicate/site-config.rsc" >/dev/null 2>&1; then
  report "the site manifest seal accepted a duplicate authoritative field"
fi
mkdir -p -- "$site_seal_root/extra-command" "$site_seal_root/same-line"
cp -- "$rsc_root/site-config.example.rsc" "$site_seal_root/extra-command/site-config.rsc"
printf '%s\n' ':put "unexpected execution"' >> "$site_seal_root/extra-command/site-config.rsc"
if "$rsc_root/seal-site-config.sh" "$site_seal_root/extra-command/site-config.rsc" >/dev/null 2>&1; then
  report "the site manifest seal accepted an extra RouterOS command"
fi
sed 's/"bridge-lan"/"bridge-lan"; :put "unexpected execution"/' \
  "$rsc_root/site-config.example.rsc" > "$site_seal_root/same-line/site-config.rsc"
if "$rsc_root/seal-site-config.sh" "$site_seal_root/same-line/site-config.rsc" >/dev/null 2>&1; then
  report "the site manifest seal accepted same-line command injection"
fi

mkdir -p -- "$site_seal_root/lifecycle"
sed '/:set cursor (\$cursor + 1)/d' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/site-loader-cursor-guard-missing.rsc"
if site_loader_cursor_contract "$site_seal_root/lifecycle/site-loader-cursor-guard-missing.rsc"; then
  report "site loader cursor contract accepted a loader without the blank-line advance"
fi
sed '/:local stopped \[\/container get \$container stopped\]/d' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/container-state-flag-missing.rsc"
if container_compatibility_contract "$site_seal_root/lifecycle/container-state-flag-missing.rsc"; then
  report "container compatibility contract accepted a state helper without stopped flag readback"
fi
sed '/:return \[:pick \$rootDirectory 1 \[:len \$rootDirectory\]\]/d' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/container-root-normalization-missing.rsc"
if container_compatibility_contract "$site_seal_root/lifecycle/container-root-normalization-missing.rsc"; then
  report "container compatibility contract accepted root-dir readback without leading-slash normalization"
fi
sed 's#/container/mounts get \$mount mode#/container/mounts get $mount read-only#' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/mount-mode-legacy-readback.rsc"
if cmp -s "$rsc_root/load-site-config.rsc" "$site_seal_root/lifecycle/mount-mode-legacy-readback.rsc"; then
  report "mount compatibility failure injection did not mutate the loader"
elif mount_compatibility_contract "$site_seal_root/lifecycle/mount-mode-legacy-readback.rsc"; then
  report "mount compatibility contract accepted the legacy read-only property"
fi
sed '/:return \[:pick \$mountSource 1 \[:len \$mountSource\]\]/d' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/mount-source-normalization-missing.rsc"
if cmp -s "$rsc_root/load-site-config.rsc" "$site_seal_root/lifecycle/mount-source-normalization-missing.rsc"; then
  report "mount source normalization failure injection did not mutate the loader"
elif mount_compatibility_contract "$site_seal_root/lifecycle/mount-source-normalization-missing.rsc"; then
  report "mount compatibility contract accepted source readback without leading-slash normalization"
fi
sed '/:local sourceMount \$1/d' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/mount-source-argument-missing.rsc"
if cmp -s "$rsc_root/load-site-config.rsc" "$site_seal_root/lifecycle/mount-source-argument-missing.rsc"; then
  report "mount source argument failure injection did not mutate the loader"
elif mount_compatibility_contract "$site_seal_root/lifecycle/mount-source-argument-missing.rsc"; then
  report "mount compatibility contract accepted source readback without argument binding"
fi
sed '/:return \$mountSource$/d' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/mount-source-passthrough-missing.rsc"
if cmp -s "$rsc_root/load-site-config.rsc" "$site_seal_root/lifecycle/mount-source-passthrough-missing.rsc"; then
  report "mount source passthrough failure injection did not mutate the loader"
elif mount_compatibility_contract "$site_seal_root/lifecycle/mount-source-passthrough-missing.rsc"; then
  report "mount compatibility contract accepted source readback without unchanged passthrough"
fi
sed 's#\[:pick \$imageID 0\]#[/file get $imageID .id]#' \
  "$rsc_root/foxos-install-inspect.rsc" > "$site_seal_root/lifecycle/install-file-id-readback.rsc"
if cmp -s "$rsc_root/foxos-install-inspect.rsc" "$site_seal_root/lifecycle/install-file-id-readback.rsc"; then
  report "install file-handle failure injection did not mutate the inspector"
elif file_native_handle_contract "$site_seal_root/lifecycle/install-file-id-readback.rsc"; then
  report "file native-handle contract accepted install .id readback"
fi
sed 's#\[:pick \$imageFile 0\]#[/file get [/file find where name=$imagePath] value-name=.id ]#' \
  "$rsc_root/upgrade-inspect.rsc" > "$site_seal_root/lifecycle/upgrade-file-id-readback.rsc"
if cmp -s "$rsc_root/upgrade-inspect.rsc" "$site_seal_root/lifecycle/upgrade-file-id-readback.rsc"; then
  report "upgrade file-handle failure injection did not mutate the inspector"
elif file_native_handle_contract "$site_seal_root/lifecycle/upgrade-file-id-readback.rsc"; then
  report "file native-handle contract accepted upgrade .id readback"
fi
sed 's#\[:pick \$mountID 0\]#[/container/mounts get $mountID .id]#' \
  "$rsc_root/foxos-install-inspect.rsc" > "$site_seal_root/lifecycle/install-mount-id-readback.rsc"
if cmp -s "$rsc_root/foxos-install-inspect.rsc" "$site_seal_root/lifecycle/install-mount-id-readback.rsc"; then
  report "install mount-handle failure injection did not mutate the inspector"
elif mount_native_handle_contract "$site_seal_root/lifecycle/install-mount-id-readback.rsc"; then
  report "mount native-handle contract accepted install .id readback"
fi
sed 's#\[:pick \$envID 0\]#[/container/envs get $envID .id]#' \
  "$rsc_root/foxos-install-inspect.rsc" > "$site_seal_root/lifecycle/install-env-id-readback.rsc"
if cmp -s "$rsc_root/foxos-install-inspect.rsc" "$site_seal_root/lifecycle/install-env-id-readback.rsc"; then
  report "install env-handle failure injection did not mutate the inspector"
elif install_env_native_handle_contract "$site_seal_root/lifecycle/install-env-id-readback.rsc"; then
  report "env native-handle contract accepted install .id readback"
fi
sed '/FoxOSCHREnvlistsSmokeConfirm != "RUN-ON-DISPOSABLE-CHR"/d' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-confirmation-missing.rsc"
if chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-confirmation-missing.rsc"; then
  report "CHR envlists smoke contract accepted a script without the destructive confirmation gate"
fi
sed '/:local parentPathMarker ("\." \. "\.")/d' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-parent-path-marker-missing.rsc"
if chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-parent-path-marker-missing.rsc"; then
  report "CHR envlists smoke contract accepted a parser-unsafe or missing parent-path marker"
fi
sed 's#(\\\$|/)#($|/)#' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-root-pattern-anchor-unescaped.rsc"
if cmp -s "$rsc_root/chr-envlists-smoke.rsc" "$site_seal_root/lifecycle/smoke-root-pattern-anchor-unescaped.rsc"; then
  report "CHR envlists smoke root-pattern failure injection did not remove the anchor escape"
elif chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-root-pattern-anchor-unescaped.rsc"; then
  report "CHR envlists smoke contract accepted an unescaped root-pattern anchor"
fi
sed '/:local containerEnvLists \[\/container get \$container envlists\]/d' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-envlists-readback-missing.rsc"
if chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-envlists-readback-missing.rsc"; then
  report "CHR envlists smoke contract accepted a script without envlists readback"
fi
sed 's/:local writableMountMode "rw"/:local writableMountMode "ro"/' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-mount-mode-ro.rsc"
if cmp -s "$rsc_root/chr-envlists-smoke.rsc" "$site_seal_root/lifecycle/smoke-mount-mode-ro.rsc"; then
  report "CHR mount mode failure injection did not mutate the smoke"
elif chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-mount-mode-ro.rsc"; then
  report "CHR smoke contract accepted a read-only named mount"
fi
sed '/:return \[:pick \$currentSource 1 \[:len \$currentSource\]\]/d' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-mount-source-normalization-missing.rsc"
if cmp -s "$rsc_root/chr-envlists-smoke.rsc" "$site_seal_root/lifecycle/smoke-mount-source-normalization-missing.rsc"; then
  report "CHR mount source normalization failure injection did not mutate the smoke"
elif chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-mount-source-normalization-missing.rsc"; then
  report "CHR smoke contract accepted mount source readback without leading-slash normalization"
fi
sed '/:local sourceMount \$1/d' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-mount-source-argument-missing.rsc"
if cmp -s "$rsc_root/chr-envlists-smoke.rsc" "$site_seal_root/lifecycle/smoke-mount-source-argument-missing.rsc"; then
  report "CHR mount source argument failure injection did not mutate the smoke"
elif chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-mount-source-argument-missing.rsc"; then
  report "CHR smoke contract accepted mount source readback without argument binding"
fi
sed '/:if (\[:typeof \$currentSource\] != "str") do={ :return "" }/d' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-mount-source-type-guard-missing.rsc"
if cmp -s "$rsc_root/chr-envlists-smoke.rsc" "$site_seal_root/lifecycle/smoke-mount-source-type-guard-missing.rsc"; then
  report "CHR mount source type-guard failure injection did not mutate the smoke"
elif chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-mount-source-type-guard-missing.rsc"; then
  report "CHR smoke contract accepted mount source readback without a string type guard"
fi
sed '/:return \$currentSource$/d' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-mount-source-passthrough-missing.rsc"
if cmp -s "$rsc_root/chr-envlists-smoke.rsc" "$site_seal_root/lifecycle/smoke-mount-source-passthrough-missing.rsc"; then
  report "CHR mount source passthrough failure injection did not mutate the smoke"
elif chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-mount-source-passthrough-missing.rsc"; then
  report "CHR smoke contract accepted mount source readback without unchanged passthrough"
fi
sed '/:put ("READBACK mount-list=/ s#\[\$mountSourcePath \$mountByList\]#[/container/mounts get $mountByList src]#' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-mount-source-evidence-raw.rsc"
if cmp -s "$rsc_root/chr-envlists-smoke.rsc" "$site_seal_root/lifecycle/smoke-mount-source-evidence-raw.rsc"; then
  report "CHR mount source evidence failure injection did not mutate the smoke"
elif chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-mount-source-evidence-raw.rsc"; then
  report "CHR smoke contract accepted raw source in the normalized evidence field"
fi
sed '/:local containerMountLists \[\/container get \$container mountlists\]/d' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-mountlists-readback-missing.rsc"
if chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-mountlists-readback-missing.rsc"; then
  report "CHR smoke contract accepted a script without mountlists readback"
fi
sed '/\$cleanupByName != \$cleanupByOwner/d' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-cleanup-binding-missing.rsc"
if chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-cleanup-binding-missing.rsc"; then
  report "CHR envlists smoke contract accepted identity-unbound cleanup"
fi
sed '/:local stopped \[\/container get \$container stopped\]/d' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-state-flag-missing.rsc"
if chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-state-flag-missing.rsc"; then
  report "CHR envlists smoke contract accepted status inference without stopped flag readback"
fi
sed 's/ || \$residualRoots > 0//' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-residual-root-guard-missing.rsc"
if chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-residual-root-guard-missing.rsc"; then
  report "CHR envlists smoke contract accepted PASS with an incomplete residual guard"
fi
sed 's/ || \$residualMounts > 0//' \
  "$rsc_root/chr-envlists-smoke.rsc" > "$site_seal_root/lifecycle/smoke-residual-mount-guard-missing.rsc"
if chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-residual-mount-guard-missing.rsc"; then
  report "CHR smoke contract accepted PASS with an incomplete mount residual guard"
fi
cp -- "$rsc_root/chr-envlists-smoke.rsc" "$site_seal_root/lifecycle/smoke-container-start-added.rsc"
printf '%s\n' '/container/start [find where name="unexpected"]' >> "$site_seal_root/lifecycle/smoke-container-start-added.rsc"
if chr_envlists_smoke_contract "$site_seal_root/lifecycle/smoke-container-start-added.rsc"; then
  report "CHR envlists smoke contract accepted a container start"
fi

sed '/PASS|summary|needs-action-count=0|conflict-count=0|first-install=ready-for-plan/d' \
  "$rsc_root/foxos-doctor.rsc" > "$site_seal_root/lifecycle/doctor-pass-summary-missing.rsc"
if foxos_doctor_contract "$site_seal_root/lifecycle/doctor-pass-summary-missing.rsc"; then
  report "FoxOS doctor contract accepted a script without the PASS summary"
fi
sed '/\$namedVethCount != \$reusableVethCount/d' \
  "$rsc_root/foxos-doctor.rsc" > "$site_seal_root/lifecycle/doctor-veth-reuse-binding-missing.rsc"
if foxos_doctor_contract "$site_seal_root/lifecycle/doctor-veth-reuse-binding-missing.rsc"; then
  report "FoxOS doctor contract accepted reusable VETHs without exact count and ownership binding"
fi
cp -- "$rsc_root/foxos-doctor.rsc" "$site_seal_root/lifecycle/doctor-write-added.rsc"
printf '%s\n' '/system/device-mode/update container=yes' >> "$site_seal_root/lifecycle/doctor-write-added.rsc"
if foxos_doctor_contract "$site_seal_root/lifecycle/doctor-write-added.rsc"; then
  report "FoxOS doctor contract accepted a RouterOS write"
fi
cp -- "$rsc_root/foxos-doctor.rsc" "$site_seal_root/lifecycle/doctor-secret-read-added.rsc"
printf '%s\n' ':put [/container/envs get [find where key="SECRET"] value]' >> "$site_seal_root/lifecycle/doctor-secret-read-added.rsc"
if foxos_doctor_contract "$site_seal_root/lifecycle/doctor-secret-read-added.rsc"; then
  report "FoxOS doctor contract accepted a sensitive env value read"
fi

sed '/:if (\$architecture = "x86" || \$architecture = "x86_64") do={ :set imageArchitecture "amd64" }/d' \
  "$rsc_root/preflight.rsc" > "$site_seal_root/lifecycle/preflight-architecture-normalization-missing.rsc"
if amd64_architecture_contract "$site_seal_root/lifecycle/preflight-architecture-normalization-missing.rsc"; then
  report "architecture contract accepted a preflight without x86 and x86_64 normalization"
fi
sed '/:if (\$storageRoot = "foxos") do={ :set storageMode "internal" }/d' \
  "$rsc_root/preflight.rsc" > "$site_seal_root/lifecycle/preflight-internal-root-reservation-missing.rsc"
if reserved_internal_storage_contract "$site_seal_root/lifecycle/preflight-internal-root-reservation-missing.rsc"; then
  report "storage contract accepted a preflight without the exact internal root reservation"
fi
sed '/\/disk find where slot=\$storageRoot/d' \
  "$rsc_root/preflight.rsc" > "$site_seal_root/lifecycle/preflight-disk-slot-check-missing.rsc"
if reserved_internal_storage_contract "$site_seal_root/lifecycle/preflight-disk-slot-check-missing.rsc"; then
  report "storage contract accepted a preflight that could not reject a misspelled external disk slot"
fi

sed '/disk1\/mihomo-config\/base.yaml/d' "$quick_install" > "$site_seal_root/lifecycle/quick-install-missing-config.md"
if quick_install_upload_manifest_contract "$site_seal_root/lifecycle/quick-install-missing-config.md"; then
  report "QUICK-INSTALL manifest accepted a missing required runtime config"
fi
awk '
  { print }
  /disk1\/mosdns-config\/config_custom.yaml/ && !injected {
    print "disk1/mosdns-config/unexpected.yaml"
    injected = 1
  }
' "$quick_install" > "$site_seal_root/lifecycle/quick-install-extra-config.md"
if quick_install_upload_manifest_contract "$site_seal_root/lifecycle/quick-install-extra-config.md"; then
  report "QUICK-INSTALL manifest accepted an unexpected runtime config"
fi
sed 's/;"site-config.rsc.sha512"//' "$quick_install" > "$site_seal_root/lifecycle/quick-install-collision-target-missing.md"
if quick_install_collision_manifest_contract "$site_seal_root/lifecycle/quick-install-collision-target-missing.md"; then
  report "QUICK-INSTALL collision allowlist accepted a missing top-level upload target"
fi
sed '/cd "${release_artifact}"/d' "$quick_install" > "$site_seal_root/lifecycle/quick-install-release-cd-missing.md"
if quick_install_download_contract "$site_seal_root/lifecycle/quick-install-release-cd-missing.md"; then
  report "QUICK-INSTALL download contract accepted a Release path that never enters the extracted bundle"
fi
sed '/cd "${ci_download_dir}"/d' "$quick_install" > "$site_seal_root/lifecycle/quick-install-ci-cd-missing.md"
if quick_install_download_contract "$site_seal_root/lifecycle/quick-install-ci-cd-missing.md"; then
  report "QUICK-INSTALL download contract accepted a Core CI path that never enters the artifact download directory"
fi
sed '/\/system\/backup\/save name=before-foxos-YYYYMMDD-HHMM/d' "$quick_install" > "$site_seal_root/lifecycle/quick-install-backup-missing.md"
if quick_install_preupload_safety_contract "$site_seal_root/lifecycle/quick-install-backup-missing.md"; then
  report "QUICK-INSTALL pre-upload contract accepted a missing encrypted backup"
fi
sed 's|"${backup_dir}/${backup_id}\.|"./${backup_id}.|g' \
  "$quick_install" > "$site_seal_root/lifecycle/quick-install-backup-inside-bundle.md"
if quick_install_preupload_safety_contract "$site_seal_root/lifecycle/quick-install-backup-inside-bundle.md"; then
  report "QUICK-INSTALL pre-upload contract accepted backups downloaded inside the upload glob"
fi
awk 'NR == 1 { print "scp -r ./* \"admin@${router_address}:${storage_root}/\"" } { print }' \
  "$quick_install" > "$site_seal_root/lifecycle/quick-install-early-scp.md"
if quick_install_preupload_safety_contract "$site_seal_root/lifecycle/quick-install-early-scp.md"; then
  report "QUICK-INSTALL pre-upload contract accepted SCP before backup and collision gates"
fi
sed '/:for promotedAttempt from=1 to=6 do={/d' \
  "$rsc_root/upgrade-promote.rsc" > "$site_seal_root/lifecycle/promote-retry-missing.rsc"
if upgrade_promote_api_contract "$site_seal_root/lifecycle/promote-retry-missing.rsc"; then
  report "upgrade promote contract accepted a missing promoted retry loop"
fi
sed '/:for finalAcceptanceAttempt from=1 to=18 do={/d' \
  "$rsc_root/upgrade-promote.rsc" > "$site_seal_root/lifecycle/final-acceptance-missing.rsc"
if upgrade_promote_api_contract "$site_seal_root/lifecycle/final-acceptance-missing.rsc"; then
  report "upgrade promote contract accepted a missing post-switch acceptance loop"
fi
sed 's/\$finalLiveOK && \$finalReadyOK && \$finalPageOK && \$finalReadOnlyOK/\$finalLiveOK \&\& \$finalPageOK \&\& \$finalReadOnlyOK/' \
  "$rsc_root/upgrade-promote.rsc" > "$site_seal_root/lifecycle/final-acceptance-ready-missing.rsc"
if cmp -s "$rsc_root/upgrade-promote.rsc" "$site_seal_root/lifecycle/final-acceptance-ready-missing.rsc"; then
  report "post-switch ready failure injection did not mutate upgrade-promote.rsc"
elif upgrade_promote_api_contract "$site_seal_root/lifecycle/final-acceptance-ready-missing.rsc"; then
  report "upgrade promote contract accepted a post-switch gate without ready"
fi
awk '
  !mutated && index($0, "$finalActiveOwner != $pending") {
    print ":if ([:len $finalActiveOwner] != 1 || [:len $finalRollbackOwner] != 1) do={"
    mutated = 1
    next
  }
  { print }
' "$rsc_root/upgrade-promote.rsc" > "$site_seal_root/lifecycle/final-owner-identity-weakened.rsc"
if cmp -s "$rsc_root/upgrade-promote.rsc" "$site_seal_root/lifecycle/final-owner-identity-weakened.rsc"; then
  report "post-switch owner identity failure injection did not mutate upgrade-promote.rsc"
elif upgrade_promote_api_contract "$site_seal_root/lifecycle/final-owner-identity-weakened.rsc"; then
  report "upgrade promote contract accepted a post-switch owner gate without identity, status, name, and root-dir checks"
fi
sed '/:local rejectedAbortIdentityOK false/d' \
  "$rsc_root/upgrade-promote.rsc" > "$site_seal_root/lifecycle/rejected-abort-identity-missing.rsc"
if upgrade_promote_api_contract "$site_seal_root/lifecycle/rejected-abort-identity-missing.rsc"; then
  report "upgrade promote contract accepted an abort without the rejected-slot identity gate"
fi
awk '
  { print }
  /:if \(\$promotedRecorded = false\) do=\{/ && !injected {
    print "  /container/stop $pending"
    injected = 1
  }
' "$rsc_root/upgrade-promote.rsc" > "$site_seal_root/lifecycle/promoted-uncertain-auto-stop.rsc"
if upgrade_promote_api_contract "$site_seal_root/lifecycle/promoted-uncertain-auto-stop.rsc"; then
  report "upgrade promote contract accepted an automatic rollback mutation after an uncertain promoted response"
fi
for start_case in \
  'foxos-start-all.rsc|mosdnsStartError|mosdns' \
  'foxos-start-all.rsc|foxosStartError|foxos' \
  'upgrade-promote.rsc|pendingStartError|pending' \
  'rollback.rsc|rollbackStartError|rollback'; do
  IFS='|' read -r source_script guard_name case_name <<< "$start_case"
  mutated_script="$site_seal_root/lifecycle/${case_name}.rsc"
  sed "s/:onerror ${guard_name} in={/:if (true) do={/" "$rsc_root/$source_script" > "$mutated_script"
  if cmp -s "$rsc_root/$source_script" "$mutated_script"; then
    report "start failure injection could not remove the $case_name :onerror guard"
  elif container_commands_guarded start "$mutated_script" >/dev/null 2>&1; then
    report "start failure injection was not rejected for $source_script ($case_name)"
  fi
done

sed '/:if (\$schedulerDeviceMode != true/,/^}/{s/:set failed true/:set failed false/;}' \
  "$rsc_root/preflight.rsc" > "$site_seal_root/lifecycle/scheduler-no-fail.rsc"
if scheduler_preflight_gate "$site_seal_root/lifecycle/scheduler-no-fail.rsc"; then
  report "scheduler=no failure injection was not rejected by the preflight contract"
fi
sed 's/|FOXOS_INSTALL_MARKER|MOSDNS_AUTO_INIT|"/|FOXOS_INSTALL_MARKER|MOSDNS_AUTO_INIT|UNEXPECTED_KEY|"/' \
  "$rsc_root/foxos-install-inspect.rsc" > "$site_seal_root/lifecycle/mosdns-unknown-allowed.rsc"
if mosdns_env_contract "$site_seal_root/lifecycle/mosdns-unknown-allowed.rsc"; then
  report "MosDNS unknown-env failure injection was not rejected by the inspector contract"
fi
sed 's/interval] = "0s"/interval] = "1m"/' \
  "$rsc_root/foxos-install-inspect.rsc" > "$site_seal_root/lifecycle/scheduler-interval-1m.rsc"
if scheduler_contract "$site_seal_root/lifecycle/scheduler-interval-1m.rsc"; then
  report "scheduler interval=1m failure injection was not rejected"
fi
sed 's/name="www" && dynamic=no/name="www"/' \
  "$rsc_root/foxos-doctor.rsc" > "$site_seal_root/lifecycle/rest-www-dynamic-connections-counted.rsc"
if cmp -s "$rsc_root/foxos-doctor.rsc" "$site_seal_root/lifecycle/rest-www-dynamic-connections-counted.rsc"; then
  report "REST static-service failure injection did not mutate the doctor"
elif rest_www_static_service_contract "$site_seal_root/lifecycle/rest-www-dynamic-connections-counted.rsc"; then
  report "REST static-service contract accepted a query that counts dynamic www connections"
fi
sed 's/(\$wwwAddress != \$siteNetwork && \$wwwAddress != \$foxosRESTAddress)/(false)/' \
  "$rsc_root/preflight.rsc" > "$site_seal_root/lifecycle/rest-extra-address-allowed.rsc"
if rest_address_contract "$site_seal_root/lifecycle/rest-extra-address-allowed.rsc"; then
  report "REST mixed-address failure injection was not rejected"
fi
sed '/\$serviceAddress & \$netmaskValue/d' \
  "$rsc_root/preflight.rsc" > "$site_seal_root/lifecycle/site-cidr-membership-missing.rsc"
if cmp -s "$rsc_root/preflight.rsc" "$site_seal_root/lifecycle/site-cidr-membership-missing.rsc"; then
  report "site CIDR membership failure injection did not mutate preflight"
elif routeros_site_ipv4_cidr_contract "$site_seal_root/lifecycle/site-cidr-membership-missing.rsc"; then
  report "site CIDR contract accepted a preflight without IPv4 bitmask membership"
fi
sed 's/\[:toip \[:pick \$privateCIDR 0 \$privateCIDRSlash\]\]/[:toip $privateCIDR]/' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/ipv4-cidr-toip-prefix.rsc"
if cmp -s "$rsc_root/load-site-config.rsc" "$site_seal_root/lifecycle/ipv4-cidr-toip-prefix.rsc"; then
  report "IPv4 CIDR conversion failure injection did not mutate the loader"
elif routeros_private_cidr_contract "$site_seal_root/lifecycle/ipv4-cidr-toip-prefix.rsc"; then
  report "IPv4 CIDR contract accepted direct :toip conversion of a prefix"
fi
sed -e 's/:local probeAddress /:local address /' -e 's/\$probeAddress/\$address/g' \
  "$rsc_root/preflight.rsc" > "$site_seal_root/lifecycle/find-filter-variable-collision.rsc"
if cmp -s "$rsc_root/preflight.rsc" "$site_seal_root/lifecycle/find-filter-variable-collision.rsc"; then
  report "find-filter variable collision failure injection did not mutate preflight"
elif routeros_find_filter_variable_collision_free "$site_seal_root/lifecycle/find-filter-variable-collision.rsc" >/dev/null 2>&1; then
  report "RouterOS find-filter variable collision was not rejected"
fi
parenthesized_collision_fixture="$site_seal_root/lifecycle/find-filter-parenthesized-variable-collision.rsc"
printf '%s\n' \
  ':local address "10.0.0.3"' \
  ':local configured [/ip/address find where address~($address . "/")]' \
  > "$parenthesized_collision_fixture"
if routeros_find_filter_variable_collision_free "$parenthesized_collision_fixture" >/dev/null 2>&1; then
  report "parenthesized RouterOS find-filter variable collision was not rejected"
fi
sed 's/ && status!="failed"//' \
  "$rsc_root/preflight.rsc" > "$site_seal_root/lifecycle/failed-arp-counted-as-occupied.rsc"
if cmp -s "$rsc_root/preflight.rsc" "$site_seal_root/lifecycle/failed-arp-counted-as-occupied.rsc"; then
  report "failed-ARP failure injection did not mutate preflight"
elif routeros_arp_occupancy_contract "$site_seal_root/lifecycle/failed-arp-counted-as-occupied.rsc"; then
  report "failed dynamic ARP entries were not excluded from occupancy checks"
fi
mixed_arp_fixture="$site_seal_root/lifecycle/mixed-guarded-and-unguarded-arp.rsc"
printf '%s\n' \
  ':local probeAddress "10.0.0.3"' \
  ':local guarded [/ip/arp find where address=$probeAddress && status!="failed"]; :local unguarded [/ip/arp find where address=$probeAddress]' \
  > "$mixed_arp_fixture"
if routeros_arp_occupancy_contract "$mixed_arp_fixture"; then
  report "same-line unguarded ARP query was hidden by a guarded query"
fi
sed 's/:local privateULA \[:toip6 "fc00::\/7"\]/:local privateULA [:toip "fc00::\/7"]/' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/ula-toip-bypass.rsc"
if routeros_private_cidr_contract "$site_seal_root/lifecycle/ula-toip-bypass.rsc"; then
  report "IPv6 :toip failure injection was not rejected"
fi
sed 's/\$hostnameLabelLength > 63/\$hostnameLabelLength > 64/' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/hostname-label-64-allowed.rsc"
if cmp -s "$rsc_root/load-site-config.rsc" "$site_seal_root/lifecycle/hostname-label-64-allowed.rsc"; then
  report "hostname label-length failure injection did not mutate the loader"
elif routeros_hostname_contract "$site_seal_root/lifecycle/hostname-label-64-allowed.rsc"; then
  report "hostname label-length failure injection was not rejected"
fi
sed '/:local publicHostnameDoubleDot ("\." \. "\.")/d' \
  "$rsc_root/load-site-config.rsc" > "$site_seal_root/lifecycle/hostname-double-dot-marker-missing.rsc"
if routeros_hostname_contract "$site_seal_root/lifecycle/hostname-double-dot-marker-missing.rsc"; then
  report "hostname contract accepted a parser-unsafe or missing double-dot marker"
fi
sed '/publicHostnameLength > 253/s/home/example/' \
  "$rsc_root/preflight.rsc" > "$site_seal_root/lifecycle/hostname-suffix-weakened.rsc"
if cmp -s "$rsc_root/preflight.rsc" "$site_seal_root/lifecycle/hostname-suffix-weakened.rsc"; then
  report "hostname suffix failure injection did not mutate preflight"
elif routeros_hostname_contract "$site_seal_root/lifecycle/hostname-suffix-weakened.rsc"; then
  report "hostname suffix failure injection was not rejected"
fi
sed 's#/system/script get \$startScript policy#/system/script skip $startScript policy#' \
  "$rsc_root/foxos-full-install.rsc" > "$site_seal_root/lifecycle/system-script-policy-missing.rsc"
if cmp -s "$rsc_root/foxos-full-install.rsc" "$site_seal_root/lifecycle/system-script-policy-missing.rsc"; then
  report "system-script policy missing failure injection did not mutate full install"
elif system_script_policy_contract "$site_seal_root/lifecycle/system-script-policy-missing.rsc"; then
  report "missing system-script policy failure injection was not rejected"
fi
sed '/system\/script get/s/"read,write,test"/"read,write,test,sensitive"/' \
  "$rsc_root/foxos-full-install.rsc" > "$site_seal_root/lifecycle/system-script-policy-extra.rsc"
if cmp -s "$rsc_root/foxos-full-install.rsc" "$site_seal_root/lifecycle/system-script-policy-extra.rsc"; then
  report "system-script extra-policy failure injection did not mutate full install"
elif system_script_policy_contract "$site_seal_root/lifecycle/system-script-policy-extra.rsc"; then
  report "extra system-script policy failure injection was not rejected"
fi

sed 's/envlists=foxos-mosdns-env/envlist=foxos-mosdns-env/' \
  "$rsc_root/foxos-full-install.rsc" > "$site_seal_root/lifecycle/container-env-reference-mixed.rsc"
if cmp -s "$rsc_root/foxos-full-install.rsc" "$site_seal_root/lifecycle/container-env-reference-mixed.rsc"; then
  report "container env reference failure injection did not mutate full install"
elif container_envlist_contract "$site_seal_root/lifecycle/container-env-reference-mixed.rsc"; then
  report "singular RouterOS container env reference was not rejected"
fi

sed 's#/container/mounts add list=#/container/mounts add name=#' \
  "$rsc_root/foxos-full-install.rsc" > "$site_seal_root/lifecycle/container-mount-name.rsc"
if cmp -s "$rsc_root/foxos-full-install.rsc" "$site_seal_root/lifecycle/container-mount-name.rsc"; then
  report "container mount list failure injection did not mutate full install"
elif container_mount_list_contract "$site_seal_root/lifecycle/container-mount-name.rsc"; then
  report "unsupported container mount name property failure injection was not rejected"
fi
sed 's/mode=\$FoxOSWritableMountMode/read-only=no/' \
  "$rsc_root/foxos-full-install.rsc" > "$site_seal_root/lifecycle/container-mount-legacy-property.rsc"
if cmp -s "$rsc_root/foxos-full-install.rsc" "$site_seal_root/lifecycle/container-mount-legacy-property.rsc"; then
  report "mount legacy-property failure injection did not mutate full install"
elif full_install_mount_contract "$site_seal_root/lifecycle/container-mount-legacy-property.rsc"; then
  report "legacy read-only=no named mount creation was not rejected"
fi
sed 's/\[\$FoxOSMountMode \$mountID\] = \$FoxOSWritableMountMode/[$FoxOSMountMode $mountID] = "ro"/g' \
  "$rsc_root/foxos-install-inspect.rsc" > "$site_seal_root/lifecycle/container-mount-readback-weakened.rsc"
if cmp -s "$rsc_root/foxos-install-inspect.rsc" "$site_seal_root/lifecycle/container-mount-readback-weakened.rsc"; then
  report "mount readback failure injection did not mutate install inspector"
elif container_mount_readback_contract "$site_seal_root/lifecycle/container-mount-readback-weakened.rsc"; then
  report "weakened mount mode readback was not rejected"
fi
sed 's#\[\$FoxOSMountSource \$mountID\]#[/container/mounts get $mountID src]#g' \
  "$rsc_root/foxos-install-inspect.rsc" > "$site_seal_root/lifecycle/container-mount-source-readback-raw.rsc"
if cmp -s "$rsc_root/foxos-install-inspect.rsc" "$site_seal_root/lifecycle/container-mount-source-readback-raw.rsc"; then
  report "mount source readback failure injection did not mutate install inspector"
elif container_mount_readback_contract "$site_seal_root/lifecycle/container-mount-source-readback-raw.rsc"; then
  report "raw RouterOS mount source readback bypass was not rejected"
fi
for lifecycle_mount_script in foxos-start-all.rsc upgrade-promote.rsc rollback.rsc; do
  mutated_script="$site_seal_root/lifecycle/${lifecycle_mount_script%.rsc}-shared-mount-source-raw.rsc"
  sed 's#\[\$FoxOSMountSource \$mountID\]#[/container/mounts get $mountID src]#g' "$rsc_root/$lifecycle_mount_script" > "$mutated_script"
  if cmp -s "$rsc_root/$lifecycle_mount_script" "$mutated_script"; then
    report "shared mount source failure injection did not mutate $lifecycle_mount_script"
  elif lifecycle_shared_mount_contract "$mutated_script"; then
    report "raw shared mount source readback was not rejected for $lifecycle_mount_script"
  fi
done
for mount_digest_script in foxos-install-inspect.rsc foxos-uninstall-inspect.rsc upgrade-inspect.rsc upgrade-promote-inspect.rsc rollback-inspect.rsc upgrade-cleanup-inspect.rsc; do
  mutated_script="$site_seal_root/lifecycle/${mount_digest_script%.rsc}-mount-evidence-source-raw.rsc"
  sed -E '/:set (material|mountEvidence)/ s#\[\$FoxOSMountSource \$mountID\]#[/container/mounts get $mountID src]#g' "$rsc_root/$mount_digest_script" > "$mutated_script"
  if cmp -s "$rsc_root/$mount_digest_script" "$mutated_script"; then
    report "mount evidence source failure injection did not mutate $mount_digest_script"
  elif mount_digest_source_contract "$mutated_script"; then
    report "raw mount source was not rejected from plan evidence for $mount_digest_script"
  fi
done
for lifecycle_mount_script in foxos-start-all.rsc upgrade-promote.rsc rollback.rsc; do
  mutated_script="$site_seal_root/lifecycle/${lifecycle_mount_script%.rsc}-shared-mount-rw-weakened.rsc"
  sed 's/\[\$FoxOSMountMode \$mountID\] != \$FoxOSWritableMountMode/[$FoxOSMountMode $mountID] != "ro"/g' "$rsc_root/$lifecycle_mount_script" > "$mutated_script"
  if cmp -s "$rsc_root/$lifecycle_mount_script" "$mutated_script"; then
    report "shared mount RW failure injection did not mutate $lifecycle_mount_script"
  elif lifecycle_shared_mount_contract "$mutated_script"; then
    report "weakened shared mount RW readback was not rejected for $lifecycle_mount_script"
  fi
done
for lifecycle_mount_script in foxos-start-all.rsc upgrade-promote.rsc rollback.rsc; do
  mutated_script="$site_seal_root/lifecycle/${lifecycle_mount_script%.rsc}-shared-mount-uniqueness-missing.rsc"
  sed 's/\[:len \$mountID\] != 1 || //' "$rsc_root/$lifecycle_mount_script" > "$mutated_script"
  if cmp -s "$rsc_root/$lifecycle_mount_script" "$mutated_script"; then
    report "shared mount uniqueness failure injection did not mutate $lifecycle_mount_script"
  elif lifecycle_shared_mount_contract "$mutated_script"; then
    report "missing shared mount uniqueness guard was not rejected for $lifecycle_mount_script"
  fi
done
sed 's#;"foxos-backups|foxos-backups|/backups"##' \
  "$rsc_root/foxos-start-all.rsc" > "$site_seal_root/lifecycle/start-all-shared-mount-missing.rsc"
if cmp -s "$rsc_root/foxos-start-all.rsc" "$site_seal_root/lifecycle/start-all-shared-mount-missing.rsc"; then
  report "shared mount membership failure injection did not mutate start-all"
elif lifecycle_shared_mount_contract "$site_seal_root/lifecycle/start-all-shared-mount-missing.rsc"; then
  report "missing shared mount was not rejected for start-all"
fi
awk '
  /:if \(\$verifiedSharedMounts != 3\)/ && !injected {
    print "  /container/set $knownAdminSlots start-on-boot=no"
    injected = 1
  }
  { print }
' "$rsc_root/foxos-start-all.rsc" > "$site_seal_root/lifecycle/start-all-mutation-before-mount-guard.rsc"
if lifecycle_shared_mount_contract "$site_seal_root/lifecycle/start-all-mutation-before-mount-guard.rsc"; then
  report "container mutation before the shared mount completion guard was not rejected"
fi
sed 's/ logging]/ log-state]/g' \
  "$rsc_root/foxos-start-all.rsc" > "$site_seal_root/lifecycle/container-logging-identity-missing.rsc"
if cmp -s "$rsc_root/foxos-start-all.rsc" "$site_seal_root/lifecycle/container-logging-identity-missing.rsc"; then
  report "container identity failure injection did not remove logging readback"
elif container_identity_fields_contract "$site_seal_root/lifecycle/container-logging-identity-missing.rsc"; then
  report "missing container logging identity was not rejected"
fi
sed '/:global FoxOSUninstallContainerCount/d' \
  "$rsc_root/uninstall-apply.rsc" > "$site_seal_root/lifecycle/uninstall-container-count-global-missing.rsc"
if uninstall_snapshot_contract "$site_seal_root/lifecycle/uninstall-container-count-global-missing.rsc"; then
  report "missing uninstall container-count binding was not rejected"
fi
sed '/:local persistedDataRoot /d' \
  "$rsc_root/foxos-install-inspect.rsc" > "$site_seal_root/lifecycle/install-retained-data-check-missing.rsc"
if retained_state_install_contract "$site_seal_root/lifecycle/install-retained-data-check-missing.rsc"; then
  report "missing retained-data reinstall guard was not rejected"
fi
sed '/:local pendingMihomoApplyJournal /d' \
  "$rsc_root/foxos-uninstall-inspect.rsc" > "$site_seal_root/lifecycle/uninstall-journal-plan-check-missing.rsc"
if pending_journal_uninstall_contract "$site_seal_root/lifecycle/uninstall-journal-plan-check-missing.rsc" "$rsc_root/uninstall-apply.rsc"; then
  report "missing pending-journal uninstall plan guard was not rejected"
fi
sed '/:local pendingMihomoApplyJournalAfterStop /d' \
  "$rsc_root/uninstall-apply.rsc" > "$site_seal_root/lifecycle/uninstall-journal-post-stop-check-missing.rsc"
if pending_journal_uninstall_contract "$rsc_root/foxos-uninstall-inspect.rsc" "$site_seal_root/lifecycle/uninstall-journal-post-stop-check-missing.rsc"; then
  report "missing post-stop pending-journal guard was not rejected"
fi
for snapshot_case in \
  'mihomo-runtime-mount|:local mihomoRuntimeMountSnapshot [/container/mounts find where list="foxos-mihomo-runtime"]|:set mountID $mihomoRuntimeMountSnapshot' \
  'mihomo-port|:local mihomoPortSnapshot [/interface/bridge/port find where interface="veth-mihomo"]|:set portID $mihomoPortSnapshot' \
  'mihomo-veth|:local mihomoVethSnapshot [/interface/veth find where name="veth-mihomo"]|:set vethID $mihomoVethSnapshot'; do
  IFS='|' read -r case_name declaration binding <<< "$snapshot_case"
  declaration_mutation="$site_seal_root/lifecycle/uninstall-$case_name-snapshot-declaration-missing.rsc"
  binding_mutation="$site_seal_root/lifecycle/uninstall-$case_name-snapshot-binding-missing.rsc"
  awk -v target="$declaration" 'index($0, target) == 0 { print }' "$rsc_root/uninstall-apply.rsc" > "$declaration_mutation"
  awk -v target="$binding" 'index($0, target) == 0 { print }' "$rsc_root/uninstall-apply.rsc" > "$binding_mutation"
  if cmp -s "$rsc_root/uninstall-apply.rsc" "$declaration_mutation"; then
    report "$case_name snapshot declaration failure injection did not mutate uninstall apply"
  elif uninstall_snapshot_contract "$declaration_mutation" || uninstall_predelete_contract "$declaration_mutation"; then
    report "missing $case_name snapshot declaration was not rejected by both uninstall contracts"
  fi
  if cmp -s "$rsc_root/uninstall-apply.rsc" "$binding_mutation"; then
    report "$case_name snapshot binding failure injection did not mutate uninstall apply"
  elif uninstall_predelete_contract "$binding_mutation"; then
    report "missing $case_name snapshot binding was not rejected"
  fi
done
awk '
  /foxos-uninstall-inspect\.rsc/ {
    inspector_count++
    if (inspector_count == 2) next
  }
  { print }
' "$rsc_root/uninstall-apply.rsc" > "$site_seal_root/lifecycle/uninstall-post-snapshot-inspector-missing.rsc"
if uninstall_predelete_contract "$site_seal_root/lifecycle/uninstall-post-snapshot-inspector-missing.rsc"; then
  report "missing post-snapshot uninstall inspector was not rejected"
fi
sed '/:if (\$FoxOSUninstallCurrentDigest != \$approved) do={/,/^}/d' \
  "$rsc_root/uninstall-apply.rsc" > "$site_seal_root/lifecycle/uninstall-post-snapshot-digest-missing.rsc"
if uninstall_predelete_contract "$site_seal_root/lifecycle/uninstall-post-snapshot-digest-missing.rsc"; then
  report "missing post-snapshot uninstall digest guard was not rejected"
fi
sed 's#:local scheduler \$schedulerSnapshot#:local scheduler [/system/scheduler find where name="foxos-start-sequence"]#' \
  "$rsc_root/uninstall-apply.rsc" > "$site_seal_root/lifecycle/uninstall-fresh-scheduler-find.rsc"
if uninstall_predelete_contract "$site_seal_root/lifecycle/uninstall-fresh-scheduler-find.rsc"; then
  report "fresh find replacing an uninstall snapshot ID was not rejected"
fi
awk '
  { print }
  /:local backupsMountSnapshot / && !injected {
    print "  /system/scheduler set $schedulerSnapshot disabled=yes"
    injected = 1
  }
' "$rsc_root/uninstall-apply.rsc" > "$site_seal_root/lifecycle/uninstall-mutation-before-second-digest.rsc"
if uninstall_predelete_contract "$site_seal_root/lifecycle/uninstall-mutation-before-second-digest.rsc"; then
  report "uninstall mutation before the post-snapshot digest guard was not rejected"
fi
for early_mutation_case in \
  'container-envs-remove|/container/envs/remove $envItems' \
  'container-mounts-remove|/container/mounts/remove $backupsMountSnapshot'; do
  IFS='|' read -r case_name mutation <<< "$early_mutation_case"
  mutated_script="$site_seal_root/lifecycle/uninstall-$case_name-before-second-digest.rsc"
  awk -v mutation="$mutation" '
    { print }
    /:local backupsMountSnapshot / && !injected {
      print mutation
      injected = 1
    }
  ' "$rsc_root/uninstall-apply.rsc" > "$mutated_script"
  if cmp -s "$rsc_root/uninstall-apply.rsc" "$mutated_script"; then
    report "$case_name failure injection did not mutate uninstall apply"
  elif uninstall_predelete_contract "$mutated_script"; then
    report "$case_name before the post-snapshot digest guard was not rejected"
  fi
done

for transition_case in \
  'promote-pending|upgrade-promote.rsc|/container/set $pending comment="foxos:transition:promote"' \
  'promote-old|upgrade-promote.rsc|/container/set $active comment="foxos:rollback"' \
  'promote-active|upgrade-promote.rsc|/container/set $pending comment="foxos:active"' \
  'rollback-target|rollback.rsc|/container/set $rollback comment="foxos:transition:rollback"' \
  'rollback-previous|rollback.rsc|/container/set $active comment="foxos:transition:rollback:previous"' \
  'rollback-active|rollback.rsc|/container/set $rollback comment="foxos:active"' \
  'rollback-complete|rollback.rsc|/container/set $active comment="foxos:rollback-complete"'; do
  IFS='|' read -r case_name source_script transition_line <<< "$transition_case"
  mutated_promote="$rsc_root/upgrade-promote.rsc"
  mutated_rollback="$rsc_root/rollback.rsc"
  mutated_script="$site_seal_root/lifecycle/transition-$case_name.rsc"
  awk -v target="$transition_line" 'index($0, target) == 0 { print }' "$rsc_root/$source_script" > "$mutated_script"
  if [[ "$source_script" == "upgrade-promote.rsc" ]]; then mutated_promote=$mutated_script; else mutated_rollback=$mutated_script; fi
  if transition_contract "$mutated_promote" "$mutated_rollback" "$rsc_root/foxos-start-all.rsc"; then
    report "power-loss transition failure injection was not rejected: $case_name"
  fi
done

sed '/:if (\[:len \$installMarkers\] != 1)/d' "$rsc_root/upgrade-inspect.rsc" > "$site_seal_root/lifecycle/upgrade-marker-count-bypass.rsc"
if upgrade_marker_contract "$site_seal_root/lifecycle/upgrade-marker-count-bypass.rsc"; then
  report "upgrade marker duplicate-value failure injection was not rejected"
fi

sed '/:local transitions \[\/container find where comment~"\^foxos:transition:"\]/d' \
  "$rsc_root/upgrade-cleanup-inspect.rsc" > "$site_seal_root/lifecycle/cleanup-transition-snapshot-missing.rsc"
if cleanup_inspector_contract "$site_seal_root/lifecycle/cleanup-transition-snapshot-missing.rsc"; then
  report "cleanup inspector accepted a missing arbitrary-transition snapshot"
fi
sed '/:if (\$runningAdminCount != 1 || \$runningAdminID != \$active)/d' \
  "$rsc_root/upgrade-cleanup-inspect.rsc" > "$site_seal_root/lifecycle/cleanup-unique-running-active-missing.rsc"
if cmp -s "$rsc_root/upgrade-cleanup-inspect.rsc" "$site_seal_root/lifecycle/cleanup-unique-running-active-missing.rsc"; then
  report "cleanup unique-running-active failure injection did not mutate the inspector"
elif cleanup_inspector_contract "$site_seal_root/lifecycle/cleanup-unique-running-active-missing.rsc"; then
  report "cleanup inspector accepted another running management slot"
fi
sed 's/schedulerByName disabled] != false/schedulerByName disabled] != true/' \
  "$rsc_root/upgrade-cleanup-inspect.rsc" > "$site_seal_root/lifecycle/cleanup-disabled-scheduler-accepted.rsc"
if cmp -s "$rsc_root/upgrade-cleanup-inspect.rsc" "$site_seal_root/lifecycle/cleanup-disabled-scheduler-accepted.rsc"; then
  report "cleanup scheduler enabled-state failure injection did not mutate the inspector"
elif cleanup_inspector_contract "$site_seal_root/lifecycle/cleanup-disabled-scheduler-accepted.rsc"; then
  report "cleanup inspector accepted a disabled cold-start scheduler"
fi
awk '
  /upgrade-cleanup-inspect\.rsc/ {
    inspector_count++
    if (inspector_count == 2) next
  }
  { print }
' "$rsc_root/upgrade-cleanup-apply.rsc" > "$site_seal_root/lifecycle/cleanup-second-inspector-missing.rsc"
if confirmed_lifecycle_contract "$site_seal_root/lifecycle/cleanup-second-inspector-missing.rsc" upgrade-cleanup-inspect.rsc FoxOSUpgradeCleanupCurrentDigest FoxOSUpgradeCleanupApprovedDigest FoxOSUpgradeCleanupConfirmation; then
  report "cleanup apply accepted a missing post-confirmation inspector"
fi
sed 's#/container/set \$rollbackSnapshot comment="foxos:retained"#/container/set $retirement comment="foxos:retained"#' \
  "$rsc_root/upgrade-cleanup-apply.rsc" > "$site_seal_root/lifecycle/cleanup-fresh-retirement-write.rsc"
if cmp -s "$rsc_root/upgrade-cleanup-apply.rsc" "$site_seal_root/lifecycle/cleanup-fresh-retirement-write.rsc"; then
  report "cleanup frozen-ID write failure injection did not mutate apply"
elif cleanup_apply_snapshot_contract "$site_seal_root/lifecycle/cleanup-fresh-retirement-write.rsc"; then
  report "cleanup apply accepted a non-snapshot rollback write"
fi

for lifecycle_inspector in upgrade-inspect.rsc upgrade-promote-inspect.rsc rollback-inspect.rsc upgrade-cleanup-inspect.rsc; do
  mutated_script="$site_seal_root/lifecycle/${lifecycle_inspector%.rsc}-start-script-owner-missing.rsc"
  sed '/:local startScriptByOwner \[\/system\/script find where comment="foxos:start-sequence"\]/d' \
    "$rsc_root/$lifecycle_inspector" > "$mutated_script"
  if cmp -s "$rsc_root/$lifecycle_inspector" "$mutated_script"; then
    report "cold-start owner failure injection did not mutate $lifecycle_inspector"
  elif lifecycle_start_sequence_contract "$mutated_script"; then
    report "$lifecycle_inspector accepted an unbound cold-start system-script owner"
  fi
done

for confirmation_case in \
  'upgrade|upgrade.rsc|upgrade-inspect.rsc|FoxOSUpgradeCurrentDigest|FoxOSUpgradeApprovedDigest|FoxOSUpgradeConfirmation' \
  'promote|upgrade-promote.rsc|upgrade-promote-inspect.rsc|FoxOSUpgradePromoteCurrentDigest|FoxOSUpgradePromoteApprovedDigest|FoxOSUpgradePromoteConfirmation' \
  'rollback|rollback.rsc|rollback-inspect.rsc|FoxOSRollbackCurrentDigest|FoxOSRollbackApprovedDigest|FoxOSRollbackConfirmation' \
  'cleanup|upgrade-cleanup-apply.rsc|upgrade-cleanup-inspect.rsc|FoxOSUpgradeCleanupCurrentDigest|FoxOSUpgradeCleanupApprovedDigest|FoxOSUpgradeCleanupConfirmation'; do
  IFS='|' read -r case_name apply_script inspector_script current_global approved_global confirmation_global <<< "$confirmation_case"
  mutated_script="$site_seal_root/lifecycle/${case_name}-confirmation-clear-missing.rsc"
  sed "/:set $confirmation_global \"\"/d" "$rsc_root/$apply_script" > "$mutated_script"
  if cmp -s "$rsc_root/$apply_script" "$mutated_script"; then
    report "$case_name confirmation-clear failure injection did not mutate $apply_script"
  elif confirmed_lifecycle_contract "$mutated_script" "$inspector_script" "$current_global" "$approved_global" "$confirmation_global"; then
    report "$case_name lifecycle accepted a reusable confirmation"
  fi

  mutated_script="$site_seal_root/lifecycle/${case_name}-early-mutation.rsc"
  awk '
    { print }
    /:local confirmation/ && !injected {
      print "/container/set [/container find where comment=\"foxos:active\"] start-on-boot=no"
      injected = 1
    }
  ' "$rsc_root/$apply_script" > "$mutated_script"
  if cmp -s "$rsc_root/$apply_script" "$mutated_script"; then
    report "$case_name early-mutation failure injection did not mutate $apply_script"
  elif confirmed_lifecycle_contract "$mutated_script" "$inspector_script" "$current_global" "$approved_global" "$confirmation_global"; then
    report "$case_name lifecycle accepted a RouterOS mutation before digest confirmation"
  fi
done

for invariant in \
  '-component foxos' \
  '-component mihomo' \
  '-component mosdns' \
  'must have three distinct SHA-256 identities' \
  'upgrade_dir_name="foxos-upgrade-${release_id}"' \
  '"$upgrade_stage/foxos-amd64.tar"' \
  'allowed-upgrade-upload: this directory only' \
  'upgrade payload SHA256 verification failed' \
  'expected_upgrade_roots=(' \
  'upgrade payload root allowlist mismatch' \
  'upgrade-cleanup-inspect.rsc' \
  "! -path './SHA256SUMS'" \
  'upgrade-only file leaked into mutable bundle root'; do
  if ! rg -Fq -- "$invariant" "$repo_root/scripts/build-routeros-bundle.sh"; then
    report "bundle assembly is missing a component identity invariant: $invariant"
  fi
done
if rg -n '(mihomo-config|mosdns-config|site-config|foxos-data|foxos-backups).*["$]upgrade_stage|upgrade_stage.*(mihomo-config|mosdns-config|site-config|foxos-data|foxos-backups)' "$repo_root/scripts/build-routeros-bundle.sh"; then
  report "versioned upgrade payload contains mutable runtime configuration or data"
fi
for component in foxos mihomo mosdns; do
  if ! rg -Fq "io.foxos.component=\"$component\"" "$repo_root/Dockerfile"; then
    report "Dockerfile is missing component identity label: $component"
  fi
done
for workflow in core-ci.yml release.yml; do
  if ! rg -Fq 'scripts/build-routeros-bundle.sh' "$repo_root/.github/workflows/$workflow"; then
    report "$workflow does not run the shared bundle component contract"
  fi
done
for workflow in "$repo_root"/.github/workflows/*.yml; do
  if ! workflow_actions_pinned_contract "$workflow"; then
    report "workflow contains an external action that is not pinned to a commit SHA: $(basename "$workflow")"
  fi
done
if ! dockerfile_base_images_pinned_contract "$repo_root/Dockerfile"; then
  report "Dockerfile contains a non-scratch base image that is not pinned to a registry digest"
fi
if ! core_ci_go_tools_contract "$repo_root/.github/workflows/core-ci.yml"; then
  report "Core CI does not install ripgrep in the Go job before bundle-backed tests"
fi
if ! release_context_script_contract "$repo_root/scripts/check-release-context.sh"; then
  report "release context script does not fail closed on branch, tag, checks, alerts and release identity"
fi
if ! "$repo_root/scripts/test-release-context.sh"; then
  report "release context behavior tests did not preserve ruleset, environment, check and release-absence gates"
fi
if ! release_publisher_script_contract "$repo_root/scripts/publish-release-assets.sh"; then
  report "release publisher does not preserve create-only assets, digest readback and RC/latest semantics"
fi
if ! release_workflow_contract "$repo_root/.github/workflows/release.yml"; then
  report "release workflow does not preserve immutable first-publish semantics"
fi
if ! rg -Fq '  workflow_call:' "$repo_root/.github/workflows/codeql.yml"; then
  report "CodeQL workflow is not reusable by the release gate"
fi
if ! rg -Fq 'uses: ./.github/workflows/codeql.yml' "$repo_root/.github/workflows/release.yml"; then
  report "release workflow does not run CodeQL for the release commit"
fi
if [[ "$(rg -F 'needs: [quality, codeql]' "$repo_root/.github/workflows/release.yml" | wc -l | tr -d ' ')" != 2 ]] \
  || ! rg -Fq 'needs: [codeql, binaries, routeros-images]' "$repo_root/.github/workflows/release.yml"; then
  report "release artifacts are not fully gated on Core CI and CodeQL"
fi
workflow_negative_root=$(mktemp -d "/tmp/foxos-workflow-negative.XXXXXX")
sed '/apt-get install --yes ripgrep/d' "$repo_root/.github/workflows/core-ci.yml" > "$workflow_negative_root/core-ci.yml"
if core_ci_go_tools_contract "$workflow_negative_root/core-ci.yml"; then
  report "Core CI tool-order contract accepted a workflow without ripgrep installation"
fi
sed '/run: scripts\/publish-release-assets.sh release-assets/d' "$repo_root/.github/workflows/release.yml" > "$workflow_negative_root/release.yml"
if release_workflow_contract "$workflow_negative_root/release.yml"; then
  report "release workflow contract accepted a workflow without the create-only publisher"
fi
printf '%s\n' 'steps:' '  - uses: actions/checkout@v7' > "$workflow_negative_root/unpinned-action.yml"
if workflow_actions_pinned_contract "$workflow_negative_root/unpinned-action.yml"; then
  report "workflow action pin contract accepted a mutable tag"
fi
printf '%s\n' 'FROM alpine:3.22' > "$workflow_negative_root/unpinned.Dockerfile"
if dockerfile_base_images_pinned_contract "$workflow_negative_root/unpinned.Dockerfile"; then
  report "Dockerfile base image pin contract accepted a mutable tag"
fi
cp -- "$repo_root/scripts/publish-release-assets.sh" "$workflow_negative_root/publisher-target-commitish.sh"
printf '%s\n' '# target_commitish' >> "$workflow_negative_root/publisher-target-commitish.sh"
if release_publisher_script_contract "$workflow_negative_root/publisher-target-commitish.sh"; then
  report "release publisher contract accepted target_commitish"
fi
rm -rf -- "$workflow_negative_root"
bundle_negative_root=$(mktemp -d "/tmp/foxos-bundle-negative.XXXXXX")
printf 'duplicate archive fixture\n' > "$bundle_negative_root/duplicate.tar"
if FOXOS_IMAGE="$bundle_negative_root/duplicate.tar" \
  MIHOMO_IMAGE="$bundle_negative_root/duplicate.tar" \
  MOSDNS_IMAGE="$bundle_negative_root/duplicate.tar" \
  "$repo_root/scripts/build-routeros-bundle.sh" "$bundle_negative_root/out" duplicate-test >"$bundle_negative_root/output.log" 2>&1; then
  report "bundle assembly accepted identical component archives"
elif ! rg -Fq 'must have three distinct SHA-256 identities' "$bundle_negative_root/output.log"; then
  report "duplicate archive negative test did not reach the identity guard"
fi
rm -rf -- "$bundle_negative_root"

if ((failed != 0)); then
  exit 1
fi
printf 'RouterOS static checks passed (%s scripts).\n' "$(find "$rsc_root" -maxdepth 1 -type f -name '*.rsc' | wc -l | tr -d ' ')"
