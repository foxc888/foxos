# FoxOS container rollback with automatic SQLite compatibility restoration.
# It switches only precisely owned FoxOS container slots.

:global FoxOSSiteManifestVersion
:global FoxOSSiteFoxOSAddress
:global FoxOSSiteStorageRoot
:global FoxOSRollbackInspectVerbose false
:global FoxOSRollbackCurrentDigest
:global FoxOSRollbackApprovedDigest
:global FoxOSRollbackConfirmation
:global FoxOSRollbackActiveID
:global FoxOSRollbackSlotID
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:local releaseID "__FOXOS_RELEASE_ID__"
:if ($releaseID ~ "^__.*__$" || [:len $releaseID] < 1 || [:len $releaseID] > 40 || $releaseID !~ "^[A-Za-z0-9._-]+$") do={ :error "rollback.rsc 未绑定有效 release ID" }
:local payloadRoot ($FoxOSSiteStorageRoot . "/foxos-upgrade-" . $releaseID)
:local foxosURL ("https://" . $FoxOSSiteFoxOSAddress)
:local approvedDigest $FoxOSRollbackApprovedDigest
:local confirmationDigest $FoxOSRollbackConfirmation
/import file-name=($payloadRoot . "/rollback-inspect.rsc")
:if ([:len $approvedDigest] != 128 || $approvedDigest != $FoxOSRollbackCurrentDigest) do={
  :error "RouterOS rollback 前态在计划后变化；重新运行版本化 rollback-plan.rsc"
}
:if ($confirmationDigest != $approvedDigest) do={
  :error "未确认当前 APPROVED ROLLBACK SHA-512；未停止或切换容器"
}
:local activeSnapshot $FoxOSRollbackActiveID
:local rollbackSnapshot $FoxOSRollbackSlotID
/import file-name=($payloadRoot . "/rollback-inspect.rsc")
:if ($FoxOSRollbackCurrentDigest != $approvedDigest || $FoxOSRollbackActiveID != $activeSnapshot || $FoxOSRollbackSlotID != $rollbackSnapshot) do={
  :error "rollback 对象 ID snapshot 后变化；重新运行 rollback-plan.rsc"
}
:local active [/container find where comment="foxos:active"]
:local rollback [/container find where comment="foxos:rollback"]
:if ($active != $activeSnapshot || $rollback != $rollbackSnapshot) do={ :error "rollback 写入前槽位 ID 变化" }
:local promoteTransition [/container find where comment="foxos:transition:promote"]
:local rollbackTransition [/container find where comment="foxos:transition:rollback"]
:local rollbackPrevious [/container find where comment="foxos:transition:rollback:previous"]
:if ([:len $promoteTransition] > 0 || [:len $rollbackTransition] > 0 || [:len $rollbackPrevious] > 0) do={
  :error "rollback 二次检查后出现未批准的生命周期过渡标记；未执行容器写入，重新运行 rollback-plan.rsc"
}
:set FoxOSRollbackConfirmation ""
:set FoxOSRollbackApprovedDigest ""
:local sharedMountDefinitions {"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:local verifiedSharedMounts 0
:foreach definition in=$sharedMountDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local mountName [:pick $definition 0 $p1]
  :local expectedSource ($FoxOSSiteStorageRoot . "/" . [:pick $definition ($p1 + 1) $p2])
  :local expectedDestination [:pick $definition ($p2 + 1) [:len $definition]]
  :local mountID [/container/mounts find where list=$mountName]
  :if ([:len $mountID] != 1 || [/container/mounts get $mountID src] != $expectedSource || [/container/mounts get $mountID dst] != $expectedDestination || ([/container/mounts get $mountID read-only] != false && [/container/mounts get $mountID read-only] != "no")) do={
    :error ("rollback 所需共享挂载身份或读写属性不匹配: " . $mountName)
  }
  :set verifiedSharedMounts ($verifiedSharedMounts + 1)
}
:if ($verifiedSharedMounts != 3) do={ :error "三个共享挂载未全部通过身份与可写检查" }
:local rollbackComplete [/container find where comment="foxos:rollback-complete"]
:if ([:len $active] = 1 && [:len $rollback] = 0 && [:len $rollbackComplete] = 1) do={
  :local completedActiveName [/container get $active name]
  :local completedPreviousName [/container get $rollbackComplete name]
  :if ($completedActiveName !~ "^foxos-[A-Za-z0-9._-]+$" || $completedPreviousName !~ "^foxos-[A-Za-z0-9._-]+$" || [/container get $active root-dir] != ($FoxOSSiteStorageRoot . "/containers/" . $completedActiveName) || [/container get $rollbackComplete root-dir] != ($FoxOSSiteStorageRoot . "/containers/" . $completedPreviousName) || [/container get $active interface] != "veth-foxos" || [/container get $rollbackComplete interface] != "veth-foxos" || [/container get $active envlists] != "foxos-env" || [/container get $rollbackComplete envlists] != "foxos-env" || [/container get $active mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || [/container get $rollbackComplete mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $active start-on-boot] != false && [/container get $active start-on-boot] != "no") || ([/container get $rollbackComplete start-on-boot] != false && [/container get $rollbackComplete start-on-boot] != "no") || ([/container get $active logging] != true && [/container get $active logging] != "yes") || ([/container get $rollbackComplete logging] != true && [/container get $rollbackComplete logging] != "yes")) do={
    :error "已完成回滚的槽位身份契约不匹配"
  }
  :put "回滚已完成：唯一 active 与 rollback-complete 证据均存在。"
  :return
}
:if ([:len $active] != 1) do={ :error "找不到唯一 foxos:active 容器" }
:if ([:len $rollback] != 1) do={ :error "找不到唯一 foxos:rollback 容器" }
:if ([/container get $active .id] = [/container get $rollback .id]) do={ :error "active 与 rollback 槽位 ID 冲突" }
:local activeName [/container get $active name]
:local rollbackName [/container get $rollback name]
:if ($activeName !~ "^foxos-[A-Za-z0-9._-]+$" || $rollbackName !~ "^foxos-[A-Za-z0-9._-]+$" || [/container get $active root-dir] != ($FoxOSSiteStorageRoot . "/containers/" . $activeName) || [/container get $rollback root-dir] != ($FoxOSSiteStorageRoot . "/containers/" . $rollbackName) || [/container get $active interface] != "veth-foxos" || [/container get $rollback interface] != "veth-foxos" || [/container get $active envlists] != "foxos-env" || [/container get $rollback envlists] != "foxos-env" || [/container get $active mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || [/container get $rollback mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $active start-on-boot] != false && [/container get $active start-on-boot] != "no") || ([/container get $rollback start-on-boot] != false && [/container get $rollback start-on-boot] != "no") || ([/container get $active logging] != true && [/container get $active logging] != "yes") || ([/container get $rollback logging] != true && [/container get $rollback logging] != "yes")) do={
  :error "active 或 rollback 槽位完整身份契约不匹配"
}
:local activeStatus [/container get $active status]
:if ($activeStatus != "running" && $activeStatus != "stopped") do={ :error "active 槽位状态异常，拒绝回滚" }
:if ([/container get $rollback status] != "stopped") do={ :error "rollback 槽位必须处于 stopped，拒绝切换" }
:local tokenID [/container/envs find where list="foxos-env" key="FOXOS_API_TOKEN"]
:if ([:len $tokenID] != 1) do={ :error "FOXOS_API_TOKEN 缺失或不唯一" }
:local apiToken [/container/envs get $tokenID value]
:local authHeader ("Authorization: Bearer " . $apiToken)

:put "回滚影响：停止当前 active，在保留 rollback 所有权标记时启动旧槽位。"
:put "旧二进制若发现数据库 schema 更新，会在打开 SQLite 前校验并原子恢复升级检查点。"
:onerror activeStopError in={ /container/stop $active } do={ :put ("active stop 命令失败，继续以状态回读为准: " . $activeStopError) }
:local activeStopped false
:for attempt from=1 to=12 do={
  :delay 5s
  :if ([/container get $active status] = "stopped") do={ :set activeStopped true; :break }
}
:if ($activeStopped = false) do={
  :error "active 容器在 60 秒内未停止；未启动共享 veth/SQLite 的 rollback"
}
:local rollbackStartOK false
:onerror rollbackStartError in={
  /container/start $rollback
  :set rollbackStartOK true
} do={ :put ("rollback start 命令失败: " . $rollbackStartError) }
:if ($rollbackStartOK = false) do={
  :if ([/container get $rollback status] != "stopped") do={
    :onerror rollbackStopError in={ /container/stop $rollback } do={ :put ("rollback 补偿 stop 命令失败，继续回读: " . $rollbackStopError) }
  }
  :local failedStartRollbackStopped false
  :for attempt from=1 to=12 do={
    :if ([/container get $rollback status] = "stopped") do={ :set failedStartRollbackStopped true; :break }
    :delay 5s
  }
  :if ($failedStartRollbackStopped = false) do={ :error "rollback start 命令失败且 60 秒内未停稳；为保护共享 veth/SQLite，未恢复原 active" }
  :local activeStartOK false
  :onerror activeStartError in={
    /container/start $active
    :set activeStartOK true
  } do={ :put ("恢复原 active 的 start 命令失败: " . $activeStartError) }
  :if ($activeStartOK = false) do={ :error "rollback start 命令失败；rollback 已停稳，但原 active start 同步失败，必须人工恢复" }
  :local activeRecovered false
  :for attempt from=1 to=18 do={
    :delay 5s
    :onerror activeHealthError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set activeRecovered true }
    } do={}
    :if ($activeRecovered) do={ :break }
  }
  :if ($activeRecovered) do={ :error "rollback start 命令失败；rollback 已停稳，原 active 已重新启动并通过 ready" }
  :error "rollback start 命令失败；rollback 已停稳，但原 active 未恢复 ready，必须人工恢复"
}

:local rollbackReady false
:for attempt from=1 to=18 do={
  :delay 5s
  :if ([/container get $rollback status] = "running") do={
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
      :set rollbackReady true
      :break
    }
  }
}

:if ($rollbackReady = false) do={
  :onerror rejectedStopError in={ /container/stop $rollback } do={ :put ("rollback 验收失败后的 stop 命令失败，继续回读: " . $rejectedStopError) }
  :local rejectedRollbackStopped false
  :for attempt from=1 to=12 do={
    :delay 5s
    :if ([/container get $rollback status] = "stopped") do={ :set rejectedRollbackStopped true; :break }
  }
  :if ($rejectedRollbackStopped = false) do={ :error "rollback 验收失败且 60 秒内未停稳；为保护共享 veth/SQLite，未启动原 active" }
  /container/set $rollback comment="foxos:rollback" start-on-boot=no
  /container/set $active comment="foxos:active" start-on-boot=no
  :local activeStartOK false
  :onerror activeStartError in={
    /container/start $active
    :set activeStartOK true
  } do={ :put ("恢复原 active 的 start 命令失败: " . $activeStartError) }
  :if ($activeStartOK = false) do={ :error "rollback 已停稳，但原 active start 同步失败；保留两个槽位与检查点，必须人工恢复" }
  :local activeRecovered false
  :for attempt from=1 to=18 do={
    :delay 5s
    :onerror activeHealthError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") check-certificate=yes-without-crl output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set activeRecovered true }
    } do={}
    :if ($activeRecovered) do={ :break }
  }
  :if ($activeRecovered) do={ :error "rollback 槽位验收失败；原 active 已重新启动并通过 ready" }
  :error "rollback 与原 active 均未通过 ready；保留两个槽位与检查点，必须人工恢复"
}

:if ([/container get $rollback comment] != "foxos:rollback" || [/container get $active comment] != "foxos:active" || [/container get $rollback status] != "running" || [/container get $active status] != "stopped" || [/container get $rollback name] != $rollbackName || [/container get $active name] != $activeName || [/container get $rollback root-dir] != ($FoxOSSiteStorageRoot . "/containers/" . $rollbackName) || [/container get $active root-dir] != ($FoxOSSiteStorageRoot . "/containers/" . $activeName) || [/container get $rollback interface] != "veth-foxos" || [/container get $active interface] != "veth-foxos" || [/container get $rollback envlists] != "foxos-env" || [/container get $active envlists] != "foxos-env" || [/container get $rollback mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || [/container get $active mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $rollback start-on-boot] != false && [/container get $rollback start-on-boot] != "no") || ([/container get $active start-on-boot] != false && [/container get $active start-on-boot] != "no") || ([/container get $rollback logging] != true && [/container get $rollback logging] != "yes") || ([/container get $active logging] != true && [/container get $active logging] != "yes")) do={
  :error "rollback ownership switch 前槽位身份或状态发生变化"
}
:local ownershipSwitched false
:onerror switchError in={
  /container/set $rollback comment="foxos:transition:rollback" start-on-boot=no
  /container/set $active comment="foxos:transition:rollback:previous" start-on-boot=no
  /container/set $rollback comment="foxos:active" start-on-boot=no
  /container/set $active comment="foxos:rollback-complete" start-on-boot=no
  :set ownershipSwitched true
} do={ :put ("持久回滚所有权切换中断: " . $switchError) }
:if ($ownershipSwitched = false) do={
  /import file-name=($FoxOSSiteStorageRoot . "/foxos-start-all.rsc")
  :local recoveredActive [/container find where comment="foxos:active"]
  :local recoveredPrevious [/container find where comment="foxos:rollback-complete"]
  :if ([:len $recoveredActive] != 1 || [:len $recoveredPrevious] != 1 || $recoveredActive != $rollback || $recoveredPrevious != $active) do={
    :error "回滚所有权切换未能收敛；保留持久过渡标记，禁止并发启动共享槽位"
  }
}
:if ([/container get $rollback comment] != "foxos:active" || [/container get $active comment] != "foxos:rollback-complete" || [/container get $rollback name] != $rollbackName || [/container get $active name] != $activeName || [/container get $rollback root-dir] != ($FoxOSSiteStorageRoot . "/containers/" . $rollbackName) || [/container get $active root-dir] != ($FoxOSSiteStorageRoot . "/containers/" . $activeName) || [/container get $rollback interface] != "veth-foxos" || [/container get $active interface] != "veth-foxos" || [/container get $rollback envlists] != "foxos-env" || [/container get $active envlists] != "foxos-env" || [/container get $rollback mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || [/container get $active mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || ([/container get $rollback start-on-boot] != false && [/container get $rollback start-on-boot] != "no") || ([/container get $active start-on-boot] != false && [/container get $active start-on-boot] != "no") || ([/container get $rollback logging] != true && [/container get $rollback logging] != "yes") || ([/container get $active logging] != true && [/container get $active logging] != "yes")) do={
  :error "回滚切换后的完整槽位身份回读失败"
}
:put "回滚完成：旧槽位已通过 live、ready、页面和只读 API 后切换为 active。"
:put "若发生 schema 降级，upgrade.database_restored 审计事件记录了检查点恢复。"
:put ("继续前先运行 " . $payloadRoot . "/upgrade-cleanup-plan.rsc 归档 stopped rollback-complete，并为下一次升级使用新的 release ID。")
