# FoxOS two-phase upgrade, phase 1 apply. It creates one release-bound pending
# container only after upgrade-plan.rsc binds an exact read-only state digest.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSContainerCompatVersion
:global FoxOSContainerState
:global FoxOSContainerRoot
:global FoxOSContainerMountLists
:global FoxOSSecretContractVersion
:global FoxOSSecretHostDirectory
:global FoxOSSecretContainerDirectory
:global FoxOSSecretMountName
:global FoxOSReadonlyMountMode
:global FoxOSAdminMountLists
:global FoxOSUpgradeInspectVerbose false
:global FoxOSUpgradeCurrentDigest
:global FoxOSUpgradeApprovedDigest
:global FoxOSUpgradeConfirmation
:global FoxOSUpgradeActiveID
:global FoxOSUpgradeImageID
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__\$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || !($releaseID ~ "^[A-Za-z0-9._-]+\$")) do={ :error "upgrade.rsc 未绑定有效 release ID；只能使用版本化升级包内脚本" }
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入根目录不可变 load-site-config.rsc" }
:local storageRoot $FoxOSSiteStorageRoot
:local payloadRoot ($storageRoot . "/foxos-upgrade-" . $releaseID)
:local approved $FoxOSUpgradeApprovedDigest
:local confirmation $FoxOSUpgradeConfirmation
/import file-name=($storageRoot . "/load-site-config.rsc")
:if ($FoxOSContainerCompatVersion != 2) do={ :error "container compatibility contract is unavailable" }
:if ($FoxOSSecretContractVersion != 1 || $FoxOSSecretHostDirectory != ($storageRoot . "/foxos-secrets") || $FoxOSSecretContainerDirectory != "/run/secrets/foxos" || $FoxOSSecretMountName != "foxos-secrets" || $FoxOSReadonlyMountMode != "ro") do={ :error "secret-file compatibility contract is unavailable" }
/import file-name=($payloadRoot . "/upgrade-inspect.rsc")
:if ([:len $approved] != 128 || $approved != $FoxOSUpgradeCurrentDigest || $confirmation != $approved) do={
  :error "升级阶段 1 前态在计划后变化或摘要未确认；重新运行 upgrade-plan.rsc"
}
:local activeSnapshot $FoxOSUpgradeActiveID
:local imageSnapshot $FoxOSUpgradeImageID
/import file-name=($payloadRoot . "/upgrade-inspect.rsc")
:if ($FoxOSUpgradeCurrentDigest != $approved || $FoxOSUpgradeActiveID != $activeSnapshot || $FoxOSUpgradeImageID != $imageSnapshot) do={
  :error "升级阶段 1 对象 ID snapshot 后变化；重新运行 upgrade-plan.rsc"
}
:local activeNow [/container find where comment="foxos:active"]
:local imagePath ($payloadRoot . "/foxos-amd64.tar")
:local imageNow [/file find where name=$imagePath]
:if ([:len $activeNow] != 1 || $activeNow != $activeSnapshot || [$FoxOSContainerState $activeNow] != "running" || [:len $imageNow] != 1 || $imageNow != $imageSnapshot) do={
  :error "升级阶段 1 写入前 active 或镜像 ID 变化"
}
:set FoxOSUpgradeConfirmation ""
:set FoxOSUpgradeApprovedDigest ""

:local pendingName ("foxos-" . $releaseID)
:local pendingRoot ($storageRoot . "/containers/" . $pendingName)
:put ("升级阶段 1：active 保持运行，导入 release=" . $releaseID . " 到版本化 pending 槽位 " . $pendingName . "。")
:put ("pending root-dir=" . $pendingRoot . "；现有配置、数据、镜像、备份和 active root-dir 不变。")
/container/add name=$pendingName file=$imagePath interface=veth-foxos root-dir=$pendingRoot envlists=foxos-env mountlists=$FoxOSAdminMountLists logging=no start-on-boot=no comment="foxos:pending"
:local pending [/container find where comment="foxos:pending"]
:local pendingByName [/container find where name=$pendingName]
:if ([:len $pending] != 1 || [:len $pendingByName] != 1 || $pending != $pendingByName || [/container get $pending interface] != "veth-foxos" || [/container get $pending envlists] != "foxos-env" || [$FoxOSContainerMountLists $pending] != $FoxOSAdminMountLists || [$FoxOSContainerRoot $pending] != $pendingRoot || ([/container get $pending start-on-boot] != false && [/container get $pending start-on-boot] != "no") || ([/container get $pending logging] != false && [/container get $pending logging] != "no") || [$FoxOSContainerState $pending] = "running") do={
  :error "pending 容器创建后的完整身份回读失败"
}
:put ("镜像导入已排队。等待 " . $pendingName . " status=stopped 后执行 " . $payloadRoot . "/upgrade-promote-plan.rsc。")
