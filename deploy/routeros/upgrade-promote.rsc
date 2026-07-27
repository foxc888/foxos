# FoxOS two-phase upgrade, phase 2: checkpoint, verify pending, then promote.
# Run only after phase 1 shows foxos:pending status=stopped.

:local foxosURL "http://10.0.0.4:8090"
:local active [/container find where comment="foxos:active"]
:local pending [/container find where comment="foxos:pending"]
:local rollback [/container find where comment="foxos:rollback"]
:if ([:len $active] != 1) do={ :error "必须且只能存在一个 foxos:active 容器" }
:if ([:len $pending] != 1) do={ :error "必须且只能存在一个 foxos:pending 容器" }
:if ([:len $rollback] > 0) do={ :error "已有 foxos:rollback，拒绝覆盖" }
:if ([/container get $pending status] != "stopped") do={
  /container/print
  :error "pending 镜像尚未完成导入；等待 status=stopped 后重试"
}
:local tokenID [/container/envs find where list="foxos-env" key="FOXOS_API_TOKEN"]
:if ([:len $tokenID] != 1) do={ :error "FOXOS_API_TOKEN 缺失或不唯一" }
:local apiToken [/container/envs get $tokenID value]
:if ([:len $apiToken] < 32) do={ :error "FOXOS_API_TOKEN 不符合安全基线" }
:local operationID [/container get $pending .id]
:local authHeader ("Authorization: Bearer " . $apiToken)
:local jsonHeaders ($authHeader . ",Content-Type: application/json")
:local operationBody ("{\"operationId\":\"" . $operationID . "\"}")

:put "升级阶段 2：先由当前版本创建 SQLite 兼容回滚点；检查点失败时不会停止 active。"
:local checkpointOK false
:onerror checkpointError in={
  :local checkpointResult [/tool/fetch url=($foxosURL . "/api/v1/system/upgrade/checkpoint") http-method=post http-header-field=$jsonHeaders http-data=$operationBody output=user as-value]
  :if (($checkpointResult->"status") = "finished" && [:typeof [:find ($checkpointResult->"data") ("\"operationId\":\"" . $operationID . "\"")]] != "nil" && [:typeof [:find ($checkpointResult->"data") "\"status\":\"checkpoint_ready\""]] != "nil") do={
    :set checkpointOK true
  }
} do={ :put ("创建升级检查点失败: " . $checkpointError) }
:if ($checkpointOK = false) do={
  :error "旧版本未提供可验证的升级检查点；active 保持运行，禁止继续共享数据库升级"
}

:put "停止旧 active；pending 在保持 foxos:pending 所有权标记时启动并接受自动验收。"
/container/stop $active
:delay 3s
:if ([/container get $active status] != "stopped") do={
  /container/start $active
  :error "active 容器未正常停止，已请求恢复启动；未启动 pending"
}
/container/start $pending

:local pendingReady false
:for attempt from=1 to=18 do={
  :delay 5s
  :if ([/container get $pending status] = "running") do={
    :local liveOK false
    :local readyOK false
    :local pageOK false
    :local readOnlyOK false
    :onerror liveError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/live") output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ok\""]] != "nil") do={ :set liveOK true }
    } do={}
    :onerror readyError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set readyOK true }
    } do={}
    :onerror pageError in={
      :local result [/tool/fetch url=($foxosURL . "/") output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "id=\"root\""]] != "nil") do={ :set pageOK true }
    } do={}
    :onerror readOnlyError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/audit-events?limit=1") http-header-field=$authHeader output=user as-value]
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
  /container/stop $pending
  :delay 3s
  /container/set $pending comment="foxos:failed" start-on-boot=no
  /container/set $active comment="foxos:active" start-on-boot=yes
  /container/start $active
  :local oldRecovered false
  :for attempt from=1 to=18 do={
    :delay 5s
    :onerror oldHealthError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set oldRecovered true }
    } do={}
    :if ($oldRecovered) do={ :break }
  }
  :if ($oldRecovered) do={ :error "pending 验收失败；SQLite 已按需恢复，旧 active 已自动恢复并通过 ready" }
  :error "pending 验收失败，且旧 active 未能恢复 ready；保留检查点和两个槽位，必须人工恢复"
}

:local ownershipSwitched false
:onerror switchError in={
  /container/set $active comment="foxos:rollback" start-on-boot=no
  /container/set $pending comment="foxos:active" start-on-boot=yes
  :set ownershipSwitched true
} do={ :put ("切换容器所有权失败: " . $switchError) }
:if ($ownershipSwitched = false) do={
  /container/stop $pending
  :delay 3s
  /container/set $pending comment="foxos:failed" start-on-boot=no
  /container/set $active comment="foxos:active" start-on-boot=yes
  /container/start $active
  :error "pending 已通过健康检查，但所有权切换失败；已请求恢复旧 active"
}

:local promotedRecorded false
:onerror promotedError in={
  :local promotedResult [/tool/fetch url=($foxosURL . "/api/v1/system/upgrade/promoted") http-method=post http-header-field=$jsonHeaders http-data=$operationBody output=user as-value]
  :if (($promotedResult->"status") = "finished" && [:typeof [:find ($promotedResult->"data") "\"status\":\"promoted\""]] != "nil") do={ :set promotedRecorded true }
} do={ :put ("记录升级完成状态失败: " . $promotedError) }
:if ($promotedRecorded = false) do={
  /container/stop $pending
  :delay 3s
  /container/set $pending comment="foxos:failed" start-on-boot=no
  /container/set $active comment="foxos:active" start-on-boot=yes
  /container/start $active
  :error "升级完成状态或审计写入失败；已请求恢复旧 active"
}

:put "升级完成：pending 已自动通过 live、ready、页面、只读依赖与审计门禁后切换为 active。"
:put "旧容器保留为 foxos:rollback；完成实体流量验收前不要删除槽位或 SQLite 检查点。"
