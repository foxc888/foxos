# FoxOS two-phase upgrade, phase 2: checkpoint, verify pending, then promote.
# Run only after phase 1 shows foxos:pending status=stopped.

:global FoxOSSiteManifestVersion
:global FoxOSSiteFoxOSAddress
:global FoxOSSiteStorageRoot
:global FoxOSContainerCompatVersion
:global FoxOSContainerState
:global FoxOSContainerRoot
:global FoxOSContainerMountLists
:global FoxOSMountCompatVersion
:global FoxOSWritableMountMode
:global FoxOSMountSource
:global FoxOSMountMode
:global FoxOSUpgradePromoteInspectVerbose false
:global FoxOSUpgradePromoteCurrentDigest
:global FoxOSUpgradePromoteApprovedDigest
:global FoxOSUpgradePromoteConfirmation
:global FoxOSUpgradePromoteState
:global FoxOSUpgradePromoteActiveID
:global FoxOSUpgradePromotePendingID
:global FoxOSUpgradePromoteRollbackID
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:if ($FoxOSContainerCompatVersion != 2) do={ :error "container compatibility contract is unavailable" }
:if ($FoxOSMountCompatVersion != 2 || $FoxOSWritableMountMode != "rw") do={ :error "mount compatibility contract is unavailable" }
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__\$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || !($releaseID ~ "^[A-Za-z0-9._-]+\$")) do={ :error "upgrade-promote.rsc 未绑定有效 release ID" }
:local payloadRoot ($FoxOSSiteStorageRoot . "/foxos-upgrade-" . $releaseID)
:local pendingName ("foxos-" . $releaseID)
:local pendingRoot ($FoxOSSiteStorageRoot . "/containers/" . $pendingName)
:local foxosURL ("https://" . $FoxOSSiteFoxOSAddress)
:local approvedDigest $FoxOSUpgradePromoteApprovedDigest
:local confirmationDigest $FoxOSUpgradePromoteConfirmation
/import file-name=($payloadRoot . "/upgrade-promote-inspect.rsc")
:if ([:len $approvedDigest] != 128 || $approvedDigest != $FoxOSUpgradePromoteCurrentDigest) do={
  :error "RouterOS promote 前态在计划后变化；重新运行版本化 upgrade-promote-plan.rsc"
}
:if ($confirmationDigest != $approvedDigest) do={
  :error "未确认当前 APPROVED PROMOTE SHA-512；未创建检查点或切换容器"
}
:local plannedState $FoxOSUpgradePromoteState
:local activeSnapshot $FoxOSUpgradePromoteActiveID
:local pendingSnapshot $FoxOSUpgradePromotePendingID
:local rollbackSnapshot $FoxOSUpgradePromoteRollbackID
/import file-name=($payloadRoot . "/upgrade-promote-inspect.rsc")
:if ($FoxOSUpgradePromoteCurrentDigest != $approvedDigest || $FoxOSUpgradePromoteState != $plannedState || $FoxOSUpgradePromoteActiveID != $activeSnapshot || $FoxOSUpgradePromotePendingID != $pendingSnapshot || $FoxOSUpgradePromoteRollbackID != $rollbackSnapshot) do={
  :error "promote 对象 ID snapshot 后变化；重新运行 upgrade-promote-plan.rsc"
}
:local active [/container find where comment="foxos:active"]
:local pending [/container find where comment="foxos:pending"]
:local rollback [/container find where comment="foxos:rollback"]
:if ($active != $activeSnapshot || $pending != $pendingSnapshot || $rollback != $rollbackSnapshot) do={
  :error "promote 写入前槽位 ID 变化"
}
:local promoteTransition [/container find where comment="foxos:transition:promote"]
:local rollbackTransition [/container find where comment="foxos:transition:rollback"]
:local rollbackPrevious [/container find where comment="foxos:transition:rollback:previous"]
:if ([:len $promoteTransition] > 0 || [:len $rollbackTransition] > 0 || [:len $rollbackPrevious] > 0) do={
  :error "promote 二次检查后出现未批准的生命周期过渡标记；未执行容器写入，重新运行 upgrade-promote-plan.rsc"
}
:set FoxOSUpgradePromoteConfirmation ""
:set FoxOSUpgradePromoteApprovedDigest ""
:local promotionAlreadySwitched false
:if ([:len $pending] = 0 && [:len $active] = 1 && [:len $rollback] = 1 && [/container get $active name] = $pendingName && [$FoxOSContainerRoot $active] = $pendingRoot) do={
  :local promotedSlot $active
  :set active $rollback
  :set pending $promotedSlot
  :set promotionAlreadySwitched true
}
:if (($plannedState = "pending" && $promotionAlreadySwitched) || ($plannedState = "switched" && $promotionAlreadySwitched = false)) do={
  :error "promote 模式在确认后变化；重新运行 upgrade-promote-plan.rsc"
}
:local tokenID [/container/envs find where list="foxos-env" key="FOXOS_API_TOKEN"]
:if ([:len $tokenID] != 1) do={ :error "FOXOS_API_TOKEN 缺失或不唯一" }
:local apiToken [/container/envs get $tokenID value]
:if ([:len $apiToken] < 32) do={ :error "FOXOS_API_TOKEN 不符合安全基线" }
:local operationID $releaseID
:local authHeader ("Authorization: Bearer " . $apiToken)
:local jsonHeaders ($authHeader . ",Content-Type: application/json")
:local operationBody ("{\"operationId\":\"" . $operationID . "\"}")
:local sharedMountDefinitions {"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:local verifiedSharedMounts 0
:foreach definition in=$sharedMountDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local mountName [:pick $definition 0 $p1]
  :local expectedSource ($FoxOSSiteStorageRoot . "/" . [:pick $definition ($p1 + 1) $p2])
  :local expectedDestination [:pick $definition ($p2 + 1) [:len $definition]]
  :local mountID [/container/mounts find where list=$mountName]
  :if ([:len $mountID] != 1 || [$FoxOSMountSource $mountID] != $expectedSource || [/container/mounts get $mountID dst] != $expectedDestination || [$FoxOSMountMode $mountID] != $FoxOSWritableMountMode) do={
    :error ("promote 所需共享挂载身份或读写属性不匹配: " . $mountName)
  }
  :set verifiedSharedMounts ($verifiedSharedMounts + 1)
}
:if ($verifiedSharedMounts != 3) do={ :error "三个共享挂载未全部通过身份与可写检查" }

:if ($promotionAlreadySwitched = false) do={
:if ([:len $active] != 1) do={ :error "必须且只能存在一个 foxos:active 容器" }
:if ([:len $pending] != 1) do={ :error "必须且只能存在一个 foxos:pending 容器" }
:if ([:len $rollback] > 0) do={ :error "已有 foxos:rollback，拒绝覆盖" }
:if ($active = $pending) do={ :error "active 与 pending 槽位 ID 冲突" }
:local activeName [/container get $active name]
:if (!($activeName ~ "^foxos-[A-Za-z0-9._-]+\$") || [$FoxOSContainerRoot $active] != ($FoxOSSiteStorageRoot . "/containers/" . $activeName) || [/container get $active interface] != "veth-foxos" || [/container get $active envlists] != "foxos-env" || [$FoxOSContainerMountLists $active] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $active start-on-boot] != false && [/container get $active start-on-boot] != "no") || ([/container get $active logging] != false && [/container get $active logging] != "no") || [$FoxOSContainerState $active] != "running") do={
  :error "active 槽位完整身份契约不匹配"
}
:if ([/container get $pending name] != $pendingName || [$FoxOSContainerRoot $pending] != $pendingRoot || [/container get $pending interface] != "veth-foxos" || [/container get $pending envlists] != "foxos-env" || [$FoxOSContainerMountLists $pending] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $pending start-on-boot] != false && [/container get $pending start-on-boot] != "no") || ([/container get $pending logging] != false && [/container get $pending logging] != "no")) do={
  :error "pending 槽位与当前发布包的 release ID 或挂载契约不匹配"
}
:if ([$FoxOSContainerState $pending] != "stopped") do={
  /container/print
  :error "pending 镜像尚未完成导入；等待 status=stopped 后重试"
}
:put "升级阶段 2：先由当前版本创建 SQLite 兼容回滚点；检查点失败时不会停止 active。"
:local checkpointOK false
:for checkpointAttempt from=1 to=6 do={
  :if ($checkpointAttempt > 1) do={ :delay 5s }
  :onerror checkpointError in={
    :local checkpointResult [/tool/fetch url=($foxosURL . "/api/v1/system/upgrade/checkpoint") check-certificate=yes-without-crl http-method=post http-header-field=$jsonHeaders http-data=$operationBody output=user as-value]
    :if (($checkpointResult->"status") = "finished" && [:typeof [:find ($checkpointResult->"data") ("\"operationId\":\"" . $operationID . "\"")]] != "nil" && [:typeof [:find ($checkpointResult->"data") "\"status\":\"checkpoint_ready\""]] != "nil") do={
      :set checkpointOK true
    }
  } do={ :put ("创建升级检查点第 " . $checkpointAttempt . " 次请求失败: " . $checkpointError) }
  :if ($checkpointOK) do={ :break }
}
:if ($checkpointOK = false) do={
  :put "检查点响应仍无法确认；仅在原 active/pending 身份与状态完全不变时尝试幂等 abort。"
  :local checkpointAbortIdentityOK false
  :if ([:len $active] = 1 && [:len $pending] = 1 && $active = $activeSnapshot && $pending = $pendingSnapshot && [/container get $active comment] = "foxos:active" && [$FoxOSContainerState $active] = "running" && [/container get $active name] = $activeName && [$FoxOSContainerRoot $active] = ($FoxOSSiteStorageRoot . "/containers/" . $activeName) && [/container get $pending comment] = "foxos:pending" && [$FoxOSContainerState $pending] = "stopped" && [/container get $pending name] = $pendingName && [$FoxOSContainerRoot $pending] = $pendingRoot) do={
    :set checkpointAbortIdentityOK true
  }
  :if ($checkpointAbortIdentityOK = false) do={
    :error "检查点状态与槽位身份均无法确认；禁止自动 abort 或启动 pending，必须人工检查"
  }
  :local checkpointLive false
  :for checkpointLiveAttempt from=1 to=6 do={
    :if ($checkpointLiveAttempt > 1) do={ :delay 5s }
    :onerror checkpointLiveError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/live") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ok\""]] != "nil") do={ :set checkpointLive true }
    } do={}
    :if ($checkpointLive) do={ :break }
  }
  :if ($checkpointLive = false) do={ :error "检查点响应无法确认，且原 active 的 live 不可达；未自动 abort" }
  :local checkpointAbortRecorded false
  :for checkpointAbortAttempt from=1 to=6 do={
    :if ($checkpointAbortAttempt > 1) do={ :delay 5s }
    :onerror checkpointAbortError in={
      :local abortResult [/tool/fetch url=($foxosURL . "/api/v1/system/upgrade/aborted") check-certificate=yes-without-crl http-method=post http-header-field=$jsonHeaders http-data=$operationBody output=user as-value]
      :if (($abortResult->"status") = "finished" && [:typeof [:find ($abortResult->"data") ("\"operationId\":\"" . $operationID . "\"")]] != "nil" && ([:typeof [:find ($abortResult->"data") "\"status\":\"aborted\""]] != "nil" || [:typeof [:find ($abortResult->"data") "\"status\":\"restored\""]] != "nil")) do={ :set checkpointAbortRecorded true }
    } do={ :put ("取消不确定检查点第 " . $checkpointAbortAttempt . " 次请求失败: " . $checkpointAbortError) }
    :if ($checkpointAbortRecorded) do={ :break }
  }
  :if ($checkpointAbortRecorded = false) do={ :error "检查点和 abort 响应均无法确认；原 active 可能保持只读，必须人工重试 aborted 状态收敛" }
  :local checkpointReady false
  :for checkpointReadyAttempt from=1 to=18 do={
    :delay 5s
    :onerror checkpointReadyError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set checkpointReady true }
    } do={}
    :if ($checkpointReady) do={ :break }
  }
  :if ($checkpointReady) do={ :error "检查点响应无法确认；已幂等取消操作并恢复原 active 写入，请重新运行 promote plan" }
  :error "检查点已取消，但原 active 未恢复 ready；禁止启动 pending，必须人工检查"
}

:put "停止旧 active；pending 在保持 foxos:pending 所有权标记时启动并接受自动验收。"
:onerror activeStopError in={ /container/stop $active } do={ :put ("active stop 命令失败，继续以状态回读为准: " . $activeStopError) }
:local activeStopped false
:for attempt from=1 to=12 do={
  :delay 5s
  :if ([$FoxOSContainerState $active] = "stopped") do={ :set activeStopped true; :break }
}
:if ($activeStopped = false) do={
  :put "active 60 秒内未停稳；仅在它仍是唯一原槽且 live 可达时取消检查点。"
  :local activeAbortIdentityOK false
  :if ([:len $active] = 1 && [:len $pending] = 1 && $active = $activeSnapshot && $pending = $pendingSnapshot && [/container get $active comment] = "foxos:active" && [$FoxOSContainerState $active] = "running" && [/container get $active name] = $activeName && [$FoxOSContainerRoot $active] = ($FoxOSSiteStorageRoot . "/containers/" . $activeName) && [/container get $pending comment] = "foxos:pending" && [$FoxOSContainerState $pending] = "stopped" && [/container get $pending name] = $pendingName && [$FoxOSContainerRoot $pending] = $pendingRoot) do={
    :set activeAbortIdentityOK true
  }
  :if ($activeAbortIdentityOK = false) do={ :error "active 未停稳且槽位身份或状态不确定；禁止自动 abort 或启动 pending" }
  :local activeLive false
  :for activeLiveAttempt from=1 to=6 do={
    :if ($activeLiveAttempt > 1) do={ :delay 5s }
    :onerror activeLiveError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/live") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ok\""]] != "nil") do={ :set activeLive true }
    } do={}
    :if ($activeLive) do={ :break }
  }
  :if ($activeLive = false) do={ :error "active 未停稳且 live 不可达；未启动 pending，必须人工检查" }
  :local activeAbortRecorded false
  :for activeAbortAttempt from=1 to=6 do={
    :if ($activeAbortAttempt > 1) do={ :delay 5s }
    :onerror activeAbortError in={
      :local abortResult [/tool/fetch url=($foxosURL . "/api/v1/system/upgrade/aborted") check-certificate=yes-without-crl http-method=post http-header-field=$jsonHeaders http-data=$operationBody output=user as-value]
      :if (($abortResult->"status") = "finished" && [:typeof [:find ($abortResult->"data") ("\"operationId\":\"" . $operationID . "\"")]] != "nil" && ([:typeof [:find ($abortResult->"data") "\"status\":\"aborted\""]] != "nil" || [:typeof [:find ($abortResult->"data") "\"status\":\"restored\""]] != "nil")) do={ :set activeAbortRecorded true }
    } do={ :put ("active 未停稳后的 abort 第 " . $activeAbortAttempt . " 次请求失败: " . $activeAbortError) }
    :if ($activeAbortRecorded) do={ :break }
  }
  :if ($activeAbortRecorded = false) do={ :error "active 未停稳且 abort 无法确认；可能保持只读，必须人工收敛" }
  :local activeReady false
  :for activeReadyAttempt from=1 to=18 do={
    :delay 5s
    :onerror activeReadyError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set activeReady true }
    } do={}
    :if ($activeReady) do={ :break }
  }
  :if ($activeReady) do={ :error "active 未停稳；检查点已取消，原 active 已恢复 ready，未启动 pending" }
  :error "active 未停稳；检查点已取消，但原 active 未恢复 ready，必须人工检查"
}
:local pendingStartOK false
:onerror pendingStartError in={
  /container/start $pending
  :set pendingStartOK true
} do={ :put ("pending start 命令失败: " . $pendingStartError) }
:if ($pendingStartOK = false) do={
  :if ([$FoxOSContainerState $pending] != "stopped") do={
    :onerror pendingStopError in={ /container/stop $pending } do={ :put ("pending 补偿 stop 命令失败，继续回读: " . $pendingStopError) }
  }
  :local failedStartPendingStopped false
  :for attempt from=1 to=12 do={
    :if ([$FoxOSContainerState $pending] = "stopped") do={ :set failedStartPendingStopped true; :break }
    :delay 5s
  }
  :if ($failedStartPendingStopped = false) do={ :error "pending start 命令失败且 60 秒内未停稳；为保护共享 veth/SQLite，未恢复旧 active" }
  :local oldStartOK false
  :if ([$FoxOSContainerState $active] = "running") do={ :set oldStartOK true }
  :if ([$FoxOSContainerState $active] = "stopped") do={
    :onerror oldStartError in={
      /container/start $active
      :set oldStartOK true
    } do={ :put ("恢复旧 active 的 start 命令失败: " . $oldStartError) }
  }
  :if ($oldStartOK = false) do={ :error "pending start 命令失败；pending 已停稳，但旧 active start 同步失败，必须人工恢复" }
  :local oldLive false
  :for failedStartLiveAttempt from=1 to=18 do={
    :delay 5s
    :onerror oldLiveError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/live") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ok\""]] != "nil") do={ :set oldLive true }
    } do={}
    :if ($oldLive) do={ :break }
  }
  :if ($oldLive = false) do={ :error "pending start 命令失败；pending 已停稳，但旧 active 未恢复 live，必须人工恢复" }
  :local failedStartAbortIdentityOK false
  :if ($active = $activeSnapshot && $pending = $pendingSnapshot && [/container get $active comment] = "foxos:active" && [$FoxOSContainerState $active] = "running" && [/container get $active name] = $activeName && [$FoxOSContainerRoot $active] = ($FoxOSSiteStorageRoot . "/containers/" . $activeName) && [/container get $pending comment] = "foxos:pending" && [$FoxOSContainerState $pending] = "stopped" && [/container get $pending name] = $pendingName && [$FoxOSContainerRoot $pending] = $pendingRoot) do={
    :set failedStartAbortIdentityOK true
  }
  :if ($failedStartAbortIdentityOK = false) do={ :error "旧 active 恢复 live 后槽位身份变化；禁止自动 abort" }
  :local failedStartAbortRecorded false
  :for failedStartAbortAttempt from=1 to=6 do={
    :if ($failedStartAbortAttempt > 1) do={ :delay 5s }
    :onerror failedStartAbortError in={
      :local abortResult [/tool/fetch url=($foxosURL . "/api/v1/system/upgrade/aborted") check-certificate=yes-without-crl http-method=post http-header-field=$jsonHeaders http-data=$operationBody output=user as-value]
      :if (($abortResult->"status") = "finished" && [:typeof [:find ($abortResult->"data") ("\"operationId\":\"" . $operationID . "\"")]] != "nil" && ([:typeof [:find ($abortResult->"data") "\"status\":\"aborted\""]] != "nil" || [:typeof [:find ($abortResult->"data") "\"status\":\"restored\""]] != "nil")) do={ :set failedStartAbortRecorded true }
    } do={ :put ("pending start 失败后的 abort 第 " . $failedStartAbortAttempt . " 次请求失败: " . $failedStartAbortError) }
    :if ($failedStartAbortRecorded) do={ :break }
  }
  :if ($failedStartAbortRecorded = false) do={ :error "旧 active 已恢复 live，但 aborted/restored 状态无法确认；可能保持只读，必须人工收敛" }
  :local oldRecovered false
  :for failedStartReadyAttempt from=1 to=18 do={
    :delay 5s
    :onerror oldReadyError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set oldRecovered true }
    } do={}
    :if ($oldRecovered) do={ :break }
  }
  :if ($oldRecovered) do={ :error "pending start 命令失败；pending 已停稳，旧 active 已按 live、abort、ready 顺序恢复写入" }
  :error "pending start 命令失败；abort 已确认，但旧 active 未恢复 ready，必须人工检查"
}

:local pendingReady false
:for attempt from=1 to=18 do={
  :delay 5s
  :if ([$FoxOSContainerState $pending] = "running") do={
    :local liveOK false
    :local readyOK false
    :local pageOK false
    :local readOnlyOK false
    :onerror liveError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/live") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ok\""]] != "nil") do={ :set liveOK true }
    } do={}
    :onerror readyError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set readyOK true }
    } do={}
    :onerror pageError in={
      :local result [/tool/fetch url=($foxosURL . "/") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "id=\"root\""]] != "nil") do={ :set pageOK true }
    } do={}
    :onerror readOnlyError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/audit-events?limit=1") check-certificate=yes-without-crl http-header-field=$authHeader output=user as-value]
      :if (($result->"status") = "finished") do={ :set readOnlyOK true }
    } do={}
    :if ($liveOK && $readyOK && $pageOK && $readOnlyOK) do={
      :set pendingReady true
      :break
    }
  }
}

:if ($pendingReady = false) do={
  :put "pending 未通过 running、live、ready、页面和只读 API 联合验收；开始自动恢复旧容器。"
  :onerror rejectedStopError in={ /container/stop $pending } do={ :put ("pending 验收失败后的 stop 命令失败，继续回读: " . $rejectedStopError) }
  :local rejectedPendingStopped false
  :for attempt from=1 to=12 do={
    :delay 5s
    :if ([$FoxOSContainerState $pending] = "stopped") do={ :set rejectedPendingStopped true; :break }
  }
  :if ($rejectedPendingStopped = false) do={ :error "pending 验收失败且 60 秒内未停稳；为保护共享 veth/SQLite，未启动旧 active" }
  /container/set $pending comment="foxos:failed" start-on-boot=no
  /container/set $active comment="foxos:active" start-on-boot=no
  :local oldStartOK false
  :if ([$FoxOSContainerState $active] = "running") do={ :set oldStartOK true }
  :if ([$FoxOSContainerState $active] = "stopped") do={
    :onerror oldStartError in={
      /container/start $active
      :set oldStartOK true
    } do={ :put ("恢复旧 active 的 start 命令失败: " . $oldStartError) }
  }
  :if ($oldStartOK = false) do={ :error "pending 已停稳，但旧 active start 同步失败；保留检查点和两个槽位，必须人工恢复" }
  :local oldLive false
  :for rejectedLiveAttempt from=1 to=18 do={
    :delay 5s
    :onerror rejectedLiveError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/live") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ok\""]] != "nil") do={ :set oldLive true }
    } do={}
    :if ($oldLive) do={ :break }
  }
  :if ($oldLive = false) do={ :error "pending 验收失败；旧 active 未恢复 live，保留检查点和两个槽位，必须人工恢复" }
  :local rejectedAbortIdentityOK false
  :if ($active = $activeSnapshot && $pending = $pendingSnapshot && [/container get $active comment] = "foxos:active" && [$FoxOSContainerState $active] = "running" && [/container get $active name] = $activeName && [$FoxOSContainerRoot $active] = ($FoxOSSiteStorageRoot . "/containers/" . $activeName) && [/container get $pending comment] = "foxos:failed" && [$FoxOSContainerState $pending] = "stopped" && [/container get $pending name] = $pendingName && [$FoxOSContainerRoot $pending] = $pendingRoot) do={
    :set rejectedAbortIdentityOK true
  }
  :if ($rejectedAbortIdentityOK = false) do={ :error "旧 active 恢复 live 后槽位身份变化；禁止自动 abort" }
  :local rejectedAbortRecorded false
  :for rejectedAbortAttempt from=1 to=6 do={
    :if ($rejectedAbortAttempt > 1) do={ :delay 5s }
    :onerror rejectedAbortError in={
      :local abortResult [/tool/fetch url=($foxosURL . "/api/v1/system/upgrade/aborted") check-certificate=yes-without-crl http-method=post http-header-field=$jsonHeaders http-data=$operationBody output=user as-value]
      :if (($abortResult->"status") = "finished" && [:typeof [:find ($abortResult->"data") ("\"operationId\":\"" . $operationID . "\"")]] != "nil" && ([:typeof [:find ($abortResult->"data") "\"status\":\"aborted\""]] != "nil" || [:typeof [:find ($abortResult->"data") "\"status\":\"restored\""]] != "nil")) do={ :set rejectedAbortRecorded true }
    } do={ :put ("pending 验收失败后的 abort 第 " . $rejectedAbortAttempt . " 次请求失败: " . $rejectedAbortError) }
    :if ($rejectedAbortRecorded) do={ :break }
  }
  :if ($rejectedAbortRecorded = false) do={ :error "旧 active 已恢复 live，但 aborted/restored 状态无法确认；可能保持只读，必须人工收敛" }
  :local oldRecovered false
  :for rejectedReadyAttempt from=1 to=18 do={
    :delay 5s
    :onerror rejectedReadyError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set oldRecovered true }
    } do={}
    :if ($oldRecovered) do={ :break }
  }
  :if ($oldRecovered) do={ :error "pending 验收失败；SQLite 已按需恢复，旧 active 已按 live、abort、ready 顺序恢复写入" }
  :error "pending 验收失败；abort 已确认，但旧 active 未恢复 ready，必须人工检查"
}

:if ([/container get $pending comment] != "foxos:pending" || [/container get $active comment] != "foxos:active" || [$FoxOSContainerState $pending] != "running" || [$FoxOSContainerState $active] != "stopped" || [/container get $pending name] != $pendingName || [$FoxOSContainerRoot $pending] != $pendingRoot || [/container get $active name] != $activeName || [$FoxOSContainerRoot $active] != ($FoxOSSiteStorageRoot . "/containers/" . $activeName) || [/container get $pending interface] != "veth-foxos" || [/container get $active interface] != "veth-foxos" || [/container get $pending envlists] != "foxos-env" || [/container get $active envlists] != "foxos-env" || [$FoxOSContainerMountLists $pending] != "foxos-mihomo-config,foxos-data,foxos-backups" || [$FoxOSContainerMountLists $active] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $pending start-on-boot] != false && [/container get $pending start-on-boot] != "no") || ([/container get $active start-on-boot] != false && [/container get $active start-on-boot] != "no") || ([/container get $pending logging] != false && [/container get $pending logging] != "no") || ([/container get $active logging] != false && [/container get $active logging] != "no")) do={
  :error "ownership switch 前槽位身份或状态发生变化"
}
:local ownershipSwitched false
:onerror switchError in={
  /container/set $pending comment="foxos:transition:promote" start-on-boot=no
  /container/set $active comment="foxos:rollback" start-on-boot=no
  /container/set $pending comment="foxos:active" start-on-boot=no
  :set ownershipSwitched true
} do={ :put ("切换容器所有权失败: " . $switchError) }
:if ($ownershipSwitched = false) do={
  /import file-name=($FoxOSSiteStorageRoot . "/foxos-start-all.rsc")
  :local recoveredActive [/container find where comment="foxos:active"]
  :local recoveredRollback [/container find where comment="foxos:rollback"]
  :if ([:len $recoveredActive] != 1 || [:len $recoveredRollback] != 1 || [/container get $recoveredActive name] != $pendingName || [$FoxOSContainerRoot $recoveredActive] != $pendingRoot) do={
    :error "所有权切换未能收敛到已验收 pending；保留持久过渡标记，重跑 foxos-start-all.rsc"
  }
  :set pending $recoveredActive
  :set active $recoveredRollback
}
}

:if ([:len $pending] != 1 || [:len $active] != 1 || $pending = $active || [/container get $pending comment] != "foxos:active" || [/container get $pending name] != $pendingName || [$FoxOSContainerRoot $pending] != $pendingRoot || [/container get $pending interface] != "veth-foxos" || [/container get $pending envlists] != "foxos-env" || [$FoxOSContainerMountLists $pending] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $pending start-on-boot] != false && [/container get $pending start-on-boot] != "no") || ([/container get $pending logging] != false && [/container get $pending logging] != "no")) do={
  :error "promote 后 active 槽位完整身份回读失败"
}
:local rollbackName [/container get $active name]
:if ([/container get $active comment] != "foxos:rollback" || !($rollbackName ~ "^foxos-[A-Za-z0-9._-]+\$") || [$FoxOSContainerRoot $active] != ($FoxOSSiteStorageRoot . "/containers/" . $rollbackName) || [/container get $active interface] != "veth-foxos" || [/container get $active envlists] != "foxos-env" || [$FoxOSContainerMountLists $active] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $active start-on-boot] != false && [/container get $active start-on-boot] != "no") || ([/container get $active logging] != false && [/container get $active logging] != "no")) do={
  :error "promote 后 rollback 槽位完整身份回读失败"
}

:local finalAcceptanceOK false
:for finalAcceptanceAttempt from=1 to=18 do={
  :if ($finalAcceptanceAttempt > 1) do={ :delay 5s }
  :if ([$FoxOSContainerState $pending] = "running") do={
    :local finalLiveOK false
    :local finalReadyOK false
    :local finalPageOK false
    :local finalReadOnlyOK false
    :onerror finalLiveError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/live") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ok\""]] != "nil") do={ :set finalLiveOK true }
    } do={}
    :onerror finalReadyError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set finalReadyOK true }
    } do={}
    :onerror finalPageError in={
      :local result [/tool/fetch url=($foxosURL . "/") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "id=\"root\""]] != "nil") do={ :set finalPageOK true }
    } do={}
    :onerror finalReadOnlyError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/audit-events?limit=1") check-certificate=yes-without-crl http-header-field=$authHeader output=user as-value]
      :if (($result->"status") = "finished") do={ :set finalReadOnlyOK true }
    } do={}
    :if ($finalLiveOK && $finalReadyOK && $finalPageOK && $finalReadOnlyOK) do={
      :set finalAcceptanceOK true
      :break
    }
  }
}
:if ($finalAcceptanceOK = false) do={
  :error "新 active 在 promoted 写入前未通过 running、live、ready、页面和只读 API 最终验收；保留新 active 和 stopped rollback，禁止写入 promoted、自动 abort 或回滚，必须人工检查并重新运行 upgrade-promote-plan.rsc"
}
:local finalActiveOwner [/container find where comment="foxos:active"]
:local finalRollbackOwner [/container find where comment="foxos:rollback"]
:if ([:len $finalActiveOwner] != 1 || [:len $finalRollbackOwner] != 1 || $finalActiveOwner != $pending || $finalRollbackOwner != $active || [$FoxOSContainerState $finalActiveOwner] != "running" || [/container get $finalActiveOwner name] != $pendingName || [$FoxOSContainerRoot $finalActiveOwner] != $pendingRoot || [$FoxOSContainerState $finalRollbackOwner] != "stopped" || [/container get $finalRollbackOwner name] != $rollbackName || [$FoxOSContainerRoot $finalRollbackOwner] != ($FoxOSSiteStorageRoot . "/containers/" . $rollbackName)) do={
  :error "promoted 写入前 active/rollback 槽位身份或状态变化；禁止写入 promoted、自动 abort 或回滚"
}

:local promotedRecorded false
:for promotedAttempt from=1 to=6 do={
  :if ($promotedAttempt > 1) do={ :delay 5s }
  :onerror promotedError in={
    :local promotedResult [/tool/fetch url=($foxosURL . "/api/v1/system/upgrade/promoted") check-certificate=yes-without-crl http-method=post http-header-field=$jsonHeaders http-data=$operationBody output=user as-value]
    :if (($promotedResult->"status") = "finished" && [:typeof [:find ($promotedResult->"data") ("\"operationId\":\"" . $operationID . "\"")]] != "nil" && [:typeof [:find ($promotedResult->"data") "\"status\":\"promoted\""]] != "nil") do={ :set promotedRecorded true }
  } do={ :put ("记录升级完成状态第 " . $promotedAttempt . " 次请求失败: " . $promotedError) }
  :if ($promotedRecorded) do={ :break }
}
:if ($promotedRecorded = false) do={
  :local promotedIdentityOK false
  :if ([:len $pending] = 1 && [:len $active] = 1 && [/container get $pending comment] = "foxos:active" && [$FoxOSContainerState $pending] = "running" && [/container get $pending name] = $pendingName && [$FoxOSContainerRoot $pending] = $pendingRoot && [/container get $active comment] = "foxos:rollback" && [$FoxOSContainerState $active] = "stopped" && [/container get $active name] = $rollbackName && [$FoxOSContainerRoot $active] = ($FoxOSSiteStorageRoot . "/containers/" . $rollbackName)) do={
    :set promotedIdentityOK true
  }
  :if ($promotedIdentityOK = false) do={ :error "promoted 响应无法确认且槽位身份已变化；禁止自动 abort 或回滚，必须人工检查" }
  :error "promoted 响应在 6 次幂等重试后仍无法确认；保留已验收的新 active 和停止的 rollback，禁止自动回滚。重新运行 upgrade-promote-plan.rsc 查询并完成状态"
}

:put ("升级完成：release=" . $releaseID . " 已自动通过 live、ready、页面、只读依赖与审计门禁后切换为 active。")
:put "旧容器保留为 foxos:rollback；完成实体流量验收前不要删除槽位或 SQLite 检查点。"
:put ("回滚或归档前先运行 " . $payloadRoot . "/rollback-plan.rsc 或 upgrade-cleanup-plan.rsc。")
