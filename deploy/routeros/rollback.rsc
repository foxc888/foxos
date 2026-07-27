# FoxOS container rollback with automatic SQLite compatibility restoration.
# It switches only precisely owned FoxOS container slots.

:local foxosURL "http://10.0.0.4:8090"
:local active [/container find where comment="foxos:active"]
:local rollback [/container find where comment="foxos:rollback"]
:if ([:len $active] != 1) do={ :error "找不到唯一 foxos:active 容器" }
:if ([:len $rollback] != 1) do={ :error "找不到唯一 foxos:rollback 容器" }
:if ([/container get $rollback status] != "stopped") do={ :error "rollback 槽位必须处于 stopped，拒绝切换" }
:local tokenID [/container/envs find where list="foxos-env" key="FOXOS_API_TOKEN"]
:if ([:len $tokenID] != 1) do={ :error "FOXOS_API_TOKEN 缺失或不唯一" }
:local apiToken [/container/envs get $tokenID value]
:local authHeader ("Authorization: Bearer " . $apiToken)

:put "回滚影响：停止当前 active，在保留 rollback 所有权标记时启动旧槽位。"
:put "旧二进制若发现数据库 schema 更新，会在打开 SQLite 前校验并原子恢复升级检查点。"
/container/stop $active
:delay 3s
:if ([/container get $active status] != "stopped") do={
  /container/start $active
  :error "active 容器未正常停止，已请求恢复启动；未启动 rollback"
}
/container/start $rollback

:local rollbackReady false
:for attempt from=1 to=18 do={
  :delay 5s
  :if ([/container get $rollback status] = "running") do={
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
      :set rollbackReady true
      :break
    }
  }
}

:if ($rollbackReady = false) do={
  /container/stop $rollback
  :delay 3s
  /container/set $rollback comment="foxos:rollback" start-on-boot=no
  /container/set $active comment="foxos:active" start-on-boot=yes
  /container/start $active
  :local activeRecovered false
  :for attempt from=1 to=18 do={
    :delay 5s
    :onerror activeHealthError in={
      :local result [/tool/fetch url=($foxosURL . "/api/v1/health/ready") output=user as-value]
      :if (($result->"status") = "finished" && [:typeof [:find ($result->"data") "\"status\":\"ready\""]] != "nil") do={ :set activeRecovered true }
    } do={}
    :if ($activeRecovered) do={ :break }
  }
  :if ($activeRecovered) do={ :error "rollback 槽位验收失败；原 active 已重新启动并通过 ready" }
  :error "rollback 与原 active 均未通过 ready；保留两个槽位与检查点，必须人工恢复"
}

/container/set $active comment="foxos:failed" start-on-boot=no
/container/set $rollback comment="foxos:active" start-on-boot=yes
:put "回滚完成：旧槽位已通过 live、ready、页面和只读 API 后切换为 active。"
:put "若发生 schema 降级，upgrade.database_restored 审计事件记录了检查点恢复。"
