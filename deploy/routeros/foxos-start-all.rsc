# Start or resume the three precisely owned containers after image extraction.
# Boot autostart remains disabled until foxos-verify.rsc passes every gate.

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSSitePublicHostname
:global FoxOSSiteFoxOSAddress
:global FoxOSContainerCompatVersion
:global FoxOSContainerState
:global FoxOSContainerRoot
:global FoxOSMountCompatVersion
:global FoxOSWritableMountMode
:global FoxOSMountSource
:global FoxOSMountMode
:if ($FoxOSSiteManifestVersion != 2) do={ :error "先导入不可变的 load-site-config.rsc" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:if ($FoxOSContainerCompatVersion != 1) do={ :error "container compatibility contract is unavailable" }
:if ($FoxOSMountCompatVersion != 2 || $FoxOSWritableMountMode != "rw") do={ :error "mount compatibility contract is unavailable" }
:local storageRoot $FoxOSSiteStorageRoot
:local promoteTransition [/container find where comment="foxos:transition:promote"]
:local rollbackTransition [/container find where comment="foxos:transition:rollback"]
:local rollbackPrevious [/container find where comment="foxos:transition:rollback:previous"]
:local rollbackComplete [/container find where comment="foxos:rollback-complete"]
:if ([:len $promoteTransition] > 1 || [:len $rollbackTransition] > 1 || [:len $rollbackPrevious] > 1 || [:len $rollbackComplete] > 1 || ([:len $promoteTransition] > 0 && ([:len $rollbackTransition] > 0 || [:len $rollbackPrevious] > 0))) do={
  :error "FoxOS 容器切换标记不唯一或互相冲突，拒绝启动"
}
:local knownAdminSlots [/container find where comment~"^foxos:(active|pending|rollback|rollback-complete|transition:promote|transition:rollback|transition:rollback:previous|retained|failed)\$"]
:local interfaceAdminSlots [/container find where interface="veth-foxos"]
:if ([:len $knownAdminSlots] != [:len $interfaceAdminSlots]) do={ :error "veth-foxos 上存在未绑定或错绑的管理容器，拒绝启动" }
:foreach adminSlot in=$knownAdminSlots do={
  :local adminName [/container get $adminSlot name]
  :local adminStatus [$FoxOSContainerState $adminSlot]
  :if (!($adminName ~ "^foxos-[A-Za-z0-9._-]+\$") || [/container get $adminSlot interface] != "veth-foxos" || [/container get $adminSlot envlists] != "foxos-env" || [/container get $adminSlot mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || [$FoxOSContainerRoot $adminSlot] != ($storageRoot . "/containers/" . $adminName) || ($adminStatus != "running" && $adminStatus != "stopped") || ([/container get $adminSlot start-on-boot] != false && [/container get $adminSlot start-on-boot] != "no") || ([/container get $adminSlot logging] != false && [/container get $adminSlot logging] != "no")) do={
    :error ("管理容器完整身份契约不匹配: " . $adminName)
  }
}
:local mihomo [/container find where comment="foxos:mihomo"]
:local mosdns [/container find where comment="foxos:mosdns"]
:local mihomoByName [/container find where name="foxos-mihomo"]
:local mosdnsByName [/container find where name="foxos-mosdns"]
:if ([:len $mihomo] != 1 || [:len $mosdns] != 1) do={ :error "Mihomo 与 MosDNS 容器必须各自唯一" }
:if ([:len $mihomoByName] != 1 || $mihomoByName != $mihomo || [/container get $mihomo interface] != "veth-mihomo" || [/container get $mihomo envlists] != "" || [/container get $mihomo mountlists] != "foxos-mihomo-runtime" || [$FoxOSContainerRoot $mihomo] != ($storageRoot . "/containers/mihomo") || ([/container get $mihomo start-on-boot] != false && [/container get $mihomo start-on-boot] != "no") || ([/container get $mihomo logging] != true && [/container get $mihomo logging] != "yes")) do={
  :error "Mihomo 容器完整身份契约不匹配，拒绝启动"
}
:if ([:len $mosdnsByName] != 1 || $mosdnsByName != $mosdns || [/container get $mosdns interface] != "veth-mosdns" || [/container get $mosdns envlists] != "foxos-mosdns-env" || [/container get $mosdns mountlists] != "foxos-mosdns-runtime" || [$FoxOSContainerRoot $mosdns] != ($storageRoot . "/containers/mosdns") || ([/container get $mosdns start-on-boot] != false && [/container get $mosdns start-on-boot] != "no") || ([/container get $mosdns logging] != true && [/container get $mosdns logging] != "yes")) do={
  :error "MosDNS 容器完整身份契约不匹配，拒绝启动"
}
:local runningAdminSlots 0
:foreach adminSlot in=[/container find where interface="veth-foxos"] do={
  :if ([$FoxOSContainerState $adminSlot] = "running") do={ :set runningAdminSlots ($runningAdminSlots + 1) }
}
:if ($runningAdminSlots > 1) do={ :error "共享 veth/SQLite 的管理容器同时运行，拒绝继续切换或启动" }
:local sharedMountDefinitions {"foxos-mihomo-config|mihomo-config|/data/mihomo";"foxos-data|foxos-data|/data";"foxos-backups|foxos-backups|/backups"}
:local verifiedSharedMounts 0
:foreach definition in=$sharedMountDefinitions do={
  :local p1 [:find $definition "|"]
  :local p2 [:find $definition "|" ($p1 + 1)]
  :local mountName [:pick $definition 0 $p1]
  :local expectedSource ($storageRoot . "/" . [:pick $definition ($p1 + 1) $p2])
  :local expectedDestination [:pick $definition ($p2 + 1) [:len $definition]]
  :local mountID [/container/mounts find where list=$mountName]
  :if ([:len $mountID] != 1 || [$FoxOSMountSource $mountID] != $expectedSource || [/container/mounts get $mountID dst] != $expectedDestination || [$FoxOSMountMode $mountID] != $FoxOSWritableMountMode) do={
    :error ("启动所需共享挂载身份或读写属性不匹配: " . $mountName)
  }
  :set verifiedSharedMounts ($verifiedSharedMounts + 1)
}
:if ($verifiedSharedMounts != 3) do={ :error "三个共享挂载未全部通过身份与可写检查" }

# Finish a power-loss interrupted ownership switch before selecting the only
# container allowed to use the shared veth and SQLite mounts.
:if ([:len $promoteTransition] = 1) do={
  :local transitionActive [/container find where comment="foxos:active"]
  :local transitionRollback [/container find where comment="foxos:rollback"]
  :if ([:len $transitionActive] = 1 && [:len $transitionRollback] = 0) do={
    /container/set $transitionActive comment="foxos:rollback" start-on-boot=no
  } else={
    :if ([:len $transitionActive] != 0 || [:len $transitionRollback] != 1) do={ :error "无法收敛 promote 过渡状态" }
  }
  /container/set $promoteTransition comment="foxos:active" start-on-boot=no
  :put "已收敛中断的 promote 所有权切换"
}
:if ([:len $rollbackTransition] = 1) do={
  :local transitionActive [/container find where comment="foxos:active"]
  :set rollbackPrevious [/container find where comment="foxos:transition:rollback:previous"]
  :if ([:len $transitionActive] = 1 && [:len $rollbackPrevious] = 0) do={
    /container/set $transitionActive comment="foxos:transition:rollback:previous" start-on-boot=no
    :set rollbackPrevious $transitionActive
  } else={
    :if ([:len $transitionActive] != 0 || [:len $rollbackPrevious] != 1) do={ :error "无法收敛 rollback 过渡状态" }
  }
  /container/set $rollbackTransition comment="foxos:active" start-on-boot=no
  /container/set $rollbackPrevious comment="foxos:rollback-complete" start-on-boot=no
  :put "已收敛中断的 rollback 所有权切换"
} else={
  :if ([:len $rollbackPrevious] = 1) do={
    :if ([:len [/container find where comment="foxos:active"]] != 1) do={ :error "rollback previous 标记存在但 active 不唯一" }
    /container/set $rollbackPrevious comment="foxos:rollback-complete" start-on-boot=no
    :put "已完成 rollback 前序槽位的持久终态标记"
  }
}
:local active [/container find where comment="foxos:active"]
:if ([:len $mihomo] != 1 || [:len $mosdns] != 1 || [:len $active] != 1) do={ :error "三个 FoxOS 容器必须各自唯一" }
:local activeName [/container get $active name]
:if (!($activeName ~ "^foxos-[A-Za-z0-9._-]+\$") || [:len [/container find where name=$activeName]] != 1 || [/container get $active interface] != "veth-foxos" || [/container get $active envlists] != "foxos-env" || [/container get $active mountlists] != "foxos-mihomo-config,foxos-data,foxos-backups" || [$FoxOSContainerRoot $active] != ($storageRoot . "/containers/" . $activeName) || ([/container get $active start-on-boot] != false && [/container get $active start-on-boot] != "no") || ([/container get $active logging] != false && [/container get $active logging] != "no")) do={
  :error "FoxOS active 容器完整身份契约不匹配，拒绝启动"
}

# A cancelled promote/rollback job can leave its uncommitted candidate running
# without a transition marker. Stop that sole stale runner before starting the
# committed active slot; multiple runners remain a fail-closed condition.
:local committedRunningAdminCount 0
:local committedRunningAdminID ""
:foreach adminSlot in=[/container find where interface="veth-foxos"] do={
  :if ([$FoxOSContainerState $adminSlot] = "running") do={
    :set committedRunningAdminCount ($committedRunningAdminCount + 1)
    :set committedRunningAdminID $adminSlot
  }
}
:if ($committedRunningAdminCount > 1) do={ :error "过渡态收敛后仍有多个共享 veth/SQLite 的管理容器运行，拒绝继续" }
:if ($committedRunningAdminCount = 1 && $committedRunningAdminID != $active) do={
  :put ("停止未提交的运行槽位，再启动 committed active: " . [/container get $committedRunningAdminID name])
  :onerror staleAdminStopError in={
    /container/stop $committedRunningAdminID
  } do={ :put ("未提交运行槽位 stop 命令失败，继续回读: " . $staleAdminStopError) }
  :local staleAdminStopped false
  :for attempt from=1 to=12 do={
    :if ([$FoxOSContainerState $committedRunningAdminID] = "stopped") do={ :set staleAdminStopped true; :break }
    :delay 5s
  }
  :if ($staleAdminStopped = false) do={ :error "未提交运行槽位在 60 秒内未停稳；为保护共享 veth/SQLite，未启动 committed active" }
}

:foreach containerID in={$mihomo;$mosdns;$active} do={
  :local currentStatus [$FoxOSContainerState $containerID]
  :if ($currentStatus != "running" && $currentStatus != "stopped") do={
    /container/print
    :error ("镜像仍在解压或容器状态异常: " . [/container get $containerID name] . " status=" . $currentStatus)
  }
  /container/set $containerID start-on-boot=no
}

:local startedMihomo false
:local startedMosDNS false
:local startedFoxOS false

:if ([$FoxOSContainerState $mihomo] = "stopped") do={
  :put "FoxOS: 启动 Mihomo..."
  :set startedMihomo true
  :local mihomoStartOK false
  :onerror mihomoStartError in={
    /container/start $mihomo
    :set mihomoStartOK true
  } do={ :put ("Mihomo start 命令失败: " . $mihomoStartError) }
  :if ($mihomoStartOK = false) do={
    :local compensationFailed false
    :foreach containerID in={$mihomo} do={
      :if ([$FoxOSContainerState $containerID] != "stopped") do={
        :onerror stopError in={ /container/stop $containerID } do={ :put ("补偿 stop 命令失败，继续回读: " . $stopError) }
      }
      :local stopped false
      :for attempt from=1 to=12 do={
        :if ([$FoxOSContainerState $containerID] = "stopped") do={ :set stopped true; :break }
        :delay 5s
      }
      :if ($stopped = false) do={ :set compensationFailed true }
    }
    :if ($compensationFailed) do={ :error "Mihomo start 命令失败且补偿后 60 秒内未停稳；未启动其他容器" }
    :error "Mihomo start 命令失败；目标已确认 stopped，未启动其他容器"
  }
  :local mihomoRunning false
  :for attempt from=1 to=12 do={
    :delay 5s
    :if ([$FoxOSContainerState $mihomo] = "running") do={ :set mihomoRunning true; :break }
  }
  :if ($mihomoRunning = false) do={
    :local compensationFailed false
    :if ([$FoxOSContainerState $mihomo] != "stopped") do={
      :onerror stopError in={ /container/stop $mihomo } do={ :put ("Mihomo 补偿 stop 命令失败，继续回读: " . $stopError) }
    }
    :local mihomoStopped false
    :for attempt from=1 to=12 do={
      :if ([$FoxOSContainerState $mihomo] = "stopped") do={ :set mihomoStopped true; :break }
      :delay 5s
    }
    :if ($mihomoStopped = false) do={ :set compensationFailed true }
    :if ($compensationFailed) do={ :error "Mihomo 未进入 running 且补偿后 60 秒内未停稳；未启动其他容器" }
    :error "Mihomo 未进入 running；目标已确认 stopped，未启动其他容器"
  }
}

:if ([$FoxOSContainerState $mosdns] = "stopped") do={
  :put "FoxOS: 启动 MosDNS..."
  :set startedMosDNS true
  :local mosdnsStartOK false
  :onerror mosdnsStartError in={
    /container/start $mosdns
    :set mosdnsStartOK true
  } do={ :put ("MosDNS start 命令失败: " . $mosdnsStartError) }
  :if ($mosdnsStartOK = false) do={
    :local compensationFailed false
    :foreach containerID in={$mosdns;$mihomo} do={
      :local shouldStop false
      :if ($containerID = $mosdns && $startedMosDNS) do={ :set shouldStop true }
      :if ($containerID = $mihomo && $startedMihomo) do={ :set shouldStop true }
      :if ($shouldStop) do={
        :if ([$FoxOSContainerState $containerID] != "stopped") do={
          :onerror stopError in={ /container/stop $containerID } do={ :put ("补偿 stop 命令失败，继续回读: " . $stopError) }
        }
        :local stopped false
        :for attempt from=1 to=12 do={
          :if ([$FoxOSContainerState $containerID] = "stopped") do={ :set stopped true; :break }
          :delay 5s
        }
        :if ($stopped = false) do={ :set compensationFailed true }
      }
    }
    :if ($compensationFailed) do={ :error "MosDNS start 命令失败且本次启动容器未在 60 秒内全部停稳" }
    :error "MosDNS start 命令失败；已停止并回读本次启动的前序容器"
  }
  :local mosdnsRunning false
  :for attempt from=1 to=12 do={
    :delay 5s
    :if ([$FoxOSContainerState $mosdns] = "running") do={ :set mosdnsRunning true; :break }
  }
  :if ($mosdnsRunning = false) do={
    :local compensationFailed false
    :foreach containerID in={$mosdns;$mihomo} do={
      :local shouldStop false
      :if ($containerID = $mosdns && $startedMosDNS) do={ :set shouldStop true }
      :if ($containerID = $mihomo && $startedMihomo) do={ :set shouldStop true }
      :if ($shouldStop) do={
        :if ([$FoxOSContainerState $containerID] != "stopped") do={
          :onerror stopError in={ /container/stop $containerID } do={ :put ("补偿 stop 命令失败，继续回读: " . $stopError) }
        }
        :local stopped false
        :for attempt from=1 to=12 do={
          :if ([$FoxOSContainerState $containerID] = "stopped") do={ :set stopped true; :break }
          :delay 5s
        }
        :if ($stopped = false) do={ :set compensationFailed true }
      }
    }
    :if ($compensationFailed) do={ :error "MosDNS 未进入 running 且本次启动容器未在 60 秒内全部停稳" }
    :error "MosDNS 未进入 running；已停止并回读本次启动的前序容器"
  }
}

:if ([$FoxOSContainerState $active] = "stopped") do={
  :put "FoxOS: 启动管理后台..."
  :set startedFoxOS true
  :local foxosStartOK false
  :onerror foxosStartError in={
    /container/start $active
    :set foxosStartOK true
  } do={ :put ("FoxOS start 命令失败: " . $foxosStartError) }
  :if ($foxosStartOK = false) do={
    :local compensationFailed false
    :foreach containerID in={$active;$mosdns;$mihomo} do={
      :local shouldStop false
      :if ($containerID = $active && $startedFoxOS) do={ :set shouldStop true }
      :if ($containerID = $mosdns && $startedMosDNS) do={ :set shouldStop true }
      :if ($containerID = $mihomo && $startedMihomo) do={ :set shouldStop true }
      :if ($shouldStop) do={
        :if ([$FoxOSContainerState $containerID] != "stopped") do={
          :onerror stopError in={ /container/stop $containerID } do={ :put ("补偿 stop 命令失败，继续回读: " . $stopError) }
        }
        :local stopped false
        :for attempt from=1 to=12 do={
          :if ([$FoxOSContainerState $containerID] = "stopped") do={ :set stopped true; :break }
          :delay 5s
        }
        :if ($stopped = false) do={ :set compensationFailed true }
      }
    }
    :if ($compensationFailed) do={ :error "FoxOS start 命令失败且本次启动容器未在 60 秒内全部停稳" }
    :error "FoxOS start 命令失败；已停止并回读本次启动的前序容器"
  }
  :local foxosRunning false
  :for attempt from=1 to=18 do={
    :delay 5s
    :if ([$FoxOSContainerState $active] = "running") do={ :set foxosRunning true; :break }
  }
  :if ($foxosRunning = false) do={
    :local compensationFailed false
    :foreach containerID in={$active;$mosdns;$mihomo} do={
      :local shouldStop false
      :if ($containerID = $active && $startedFoxOS) do={ :set shouldStop true }
      :if ($containerID = $mosdns && $startedMosDNS) do={ :set shouldStop true }
      :if ($containerID = $mihomo && $startedMihomo) do={ :set shouldStop true }
      :if ($shouldStop) do={
        :if ([$FoxOSContainerState $containerID] != "stopped") do={
          :onerror stopError in={ /container/stop $containerID } do={ :put ("补偿 stop 命令失败，继续回读: " . $stopError) }
        }
        :local stopped false
        :for attempt from=1 to=12 do={
          :if ([$FoxOSContainerState $containerID] = "stopped") do={ :set stopped true; :break }
          :delay 5s
        }
        :if ($stopped = false) do={ :set compensationFailed true }
      }
    }
    :if ($compensationFailed) do={ :error "FoxOS 未进入 running 且本次启动容器未在 60 秒内全部停稳" }
    :error "FoxOS 未进入 running；已停止并回读本次启动的前序容器"
  }
}

:foreach containerID in={$mihomo;$mosdns;$active} do={
  :if ([$FoxOSContainerState $containerID] != "running") do={
    :local compensationFailed false
    :foreach startedID in={$active;$mosdns;$mihomo} do={
      :local shouldStop false
      :if ($startedID = $active && $startedFoxOS) do={ :set shouldStop true }
      :if ($startedID = $mosdns && $startedMosDNS) do={ :set shouldStop true }
      :if ($startedID = $mihomo && $startedMihomo) do={ :set shouldStop true }
      :if ($shouldStop) do={
        :if ([$FoxOSContainerState $startedID] != "stopped") do={
          :onerror stopError in={ /container/stop $startedID } do={ :put ("联合回读补偿 stop 命令失败，继续回读: " . $stopError) }
        }
        :local stopped false
        :for attempt from=1 to=12 do={
          :if ([$FoxOSContainerState $startedID] = "stopped") do={ :set stopped true; :break }
          :delay 5s
        }
        :if ($stopped = false) do={ :set compensationFailed true }
      }
    }
    :if ($compensationFailed) do={ :error "联合 running 回读失败且本次启动容器未在 60 秒内全部停稳" }
    :error "联合 running 回读失败；已补偿并回读本次启动的容器"
  }
}

:local finalRunningAdminCount 0
:local finalRunningAdminID ""
:foreach adminSlot in=[/container find where interface="veth-foxos"] do={
  :if ([$FoxOSContainerState $adminSlot] = "running") do={
    :set finalRunningAdminCount ($finalRunningAdminCount + 1)
    :set finalRunningAdminID $adminSlot
  }
}
:if ($finalRunningAdminCount != 1 || $finalRunningAdminID != $active) do={
  :error "启动完成回读要求 committed active 是唯一 running 管理槽位"
}

/container/print
:put ("三个容器均为 running，但 start-on-boot 仍为 no。导入 " . $storageRoot . "/foxos-data/tls/foxos-local-ca.pem，标记 trusted=yes，然后运行 foxos-verify.rsc。")
:put ("验证成功后访问 https://" . $FoxOSSitePublicHostname . "；IP 备用入口为 https://" . $FoxOSSiteFoxOSAddress . "。")
:put "若验证失败，修复后可安全重复运行本脚本；已在运行的容器不会被重复启动。"
