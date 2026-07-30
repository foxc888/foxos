# FoxOS RouterOS 7.21+ container env and mount compatibility smoke test.
# DESTRUCTIVE: run only on a disposable CHR. Never run on a production router.
# This script creates and removes an isolated env list, named RW mount, VETH,
# container, and container root directory. It never imports site configuration
# and never uses FoxOS production object names or ownership comments.
# This standalone compatibility gate intentionally does not load site topology.

:global FoxOSCHREnvlistsSmokeConfirm
:global FoxOSCHREnvlistsSmokeImagePath
:global FoxOSCHREnvlistsSmokeStorageRoot

:local containerState do={
  :local container $1
  :local running [/container get $container running]
  :local stopped [/container get $container stopped]
  :local isRunning ($running = true || $running = "yes")
  :local isStopped ($stopped = true || $stopped = "yes")
  :if ($isRunning && $isStopped) do={ :return "invalid" }
  :if ($isRunning) do={ :return "running" }
  :if ($isStopped) do={ :return "stopped" }
  :return "transitional"
}
:local containerRoot do={
  :local container $1
  :local rootDirectory [/container get $container root-dir]
  :if ([:typeof $rootDirectory] != "str") do={ :return "" }
  :if ([:len $rootDirectory] > 0 && [:pick $rootDirectory 0 1] = "/") do={
    :return [:pick $rootDirectory 1 [:len $rootDirectory]]
  }
  :return $rootDirectory
}
:local writableMountMode "rw"
:local mountSourcePath do={
  :local sourceMount $1
  :local currentSource [/container/mounts get $sourceMount src]
  :if ([:typeof $currentSource] != "str") do={ :return "" }
  :if ([:len $currentSource] > 0 && [:pick $currentSource 0 1] = "/") do={
    :return [:pick $currentSource 1 [:len $currentSource]]
  }
  :return $currentSource
}
:local mountMode do={
  :local mount $1
  :local currentMode [/container/mounts get $mount mode]
  :if ([:typeof $currentMode] != "str") do={ :return "invalid" }
  :if ($currentMode = "ro" || $currentMode = "ro,noexec" || $currentMode = "rw" || $currentMode = "rw,noexec") do={ :return $currentMode }
  :return "invalid"
}

:put "=== FoxOS CHR container contract destructive smoke test ==="
:put "WARNING disposable CHR only; abort on every production RouterOS device."
:put "=== RouterOS evidence: system resource ==="
/system/resource/print
:put "=== RouterOS evidence: container package ==="
/system/package/print detail where name="container"
:put "=== RouterOS evidence: device-mode ==="
/system/device-mode/print

:local routerVersion [/system/resource get version]
:local architecture [/system/resource get architecture-name]
:local imageArchitecture ""
:if ($architecture = "x86" || $architecture = "x86_64") do={ :set imageArchitecture "amd64" }
:local boardName [/system/resource get board-name]
:local versionBase $routerVersion
:local versionSpace [:find $versionBase " "]
:if ([:typeof $versionSpace] != "nil") do={ :set versionBase [:pick $versionBase 0 $versionSpace] }
:put ("EVIDENCE routeros-version-full=" . $routerVersion)
:put ("EVIDENCE architecture-name=" . $architecture)
:put ("EVIDENCE board-name=" . $boardName)

:if ($boardName != "CHR") do={ :error "REFUSED: board-name is not CHR; production hardware is forbidden" }
:if ($imageArchitecture != "amd64") do={ :error ("REFUSED: this FoxOS amd64 gate requires CHR architecture-name=x86 or x86_64, got " . $architecture) }
:if ($architecture = "x86_64") do={ :put "WARNING architecture-name=x86_64 is non-standard for CHR; retain this as target-specific compatibility evidence only" }

:local firstVersionDot [:find $versionBase "."]
:if ([:typeof $firstVersionDot] = "nil") do={ :error ("cannot parse RouterOS version: " . $routerVersion) }
:local versionMajor [:tonum [:pick $versionBase 0 $firstVersionDot]]
:local versionTail [:pick $versionBase ($firstVersionDot + 1) [:len $versionBase]]
:local versionMinorEnd [:find ($versionTail . ".") "."]
:local versionMinor [:tonum [:pick $versionTail 0 $versionMinorEnd]]
:if ($versionMajor < 7 || ($versionMajor = 7 && $versionMinor < 21)) do={
  :error ("RouterOS 7.21 or newer is required for envlists, got " . $routerVersion)
}

:local containerPackages [/system/package find where name="container"]
:if ([:len $containerPackages] != 1) do={ :error "container package must be installed exactly once" }
:local containerPackageVersion [/system/package get $containerPackages version]
:local containerPackageDisabled [/system/package get $containerPackages disabled]
:put ("EVIDENCE container-package-version=" . $containerPackageVersion . " disabled=" . $containerPackageDisabled)
:if ($containerPackageDisabled = true || $containerPackageDisabled = "yes") do={ :error "container package is disabled" }
:if ($containerPackageVersion != $versionBase) do={
  :error ("container package version must exactly match RouterOS: package=" . $containerPackageVersion . " routeros=" . $versionBase)
}

:local deviceModeContainer [/system/device-mode get container]
:local deviceModeScheduler [/system/device-mode get scheduler]
:put ("EVIDENCE device-mode-container=" . $deviceModeContainer . " scheduler=" . $deviceModeScheduler)
:if ($deviceModeContainer != true && $deviceModeContainer != "yes") do={
  :error "device-mode container=yes is required; this script will not change device-mode"
}

:if ($FoxOSCHREnvlistsSmokeConfirm != "RUN-ON-DISPOSABLE-CHR") do={
  :error "confirmation missing; set FoxOSCHREnvlistsSmokeConfirm=RUN-ON-DISPOSABLE-CHR"
}
:local imagePath $FoxOSCHREnvlistsSmokeImagePath
:local storageRoot $FoxOSCHREnvlistsSmokeStorageRoot
:local parentPathMarker ("." . ".")
:if ([:typeof $imagePath] != "str" || [:len $imagePath] < 3 || [:len $imagePath] > 220 || !($imagePath ~ "^[A-Za-z0-9][A-Za-z0-9._/-]*\$") || [:typeof [:find $imagePath $parentPathMarker]] != "nil") do={
  :error "FoxOSCHREnvlistsSmokeImagePath must be a bounded relative RouterOS file path"
}
:if ([:typeof $storageRoot] != "str" || [:len $storageRoot] < 1 || [:len $storageRoot] > 63 || !($storageRoot ~ "^[A-Za-z0-9][A-Za-z0-9_-]*\$")) do={
  :error "FoxOSCHREnvlistsSmokeStorageRoot must be one mounted slot name"
}
:if (!($imagePath ~ ("^" . $storageRoot . "/"))) do={
  :error "smoke image must be stored below FoxOSCHREnvlistsSmokeStorageRoot"
}
:local imageFile [/file find where name=$imagePath]
:if ([:len $imageFile] != 1) do={ :error ("smoke image is missing or ambiguous: " . $imagePath) }
:local imageSize [/file get $imageFile size]
:if ($imageSize < 1) do={ :error "smoke image is empty" }
:local storageDisk [/disk find where slot=$storageRoot]
:if ([:len $storageDisk] != 1) do={ :error ("scratch storage slot is missing or ambiguous: " . $storageRoot) }
:local storageFree [/disk get $storageDisk free]
:local requiredFree (($imageSize * 2) + 67108864)
:put ("EVIDENCE smoke-image=" . $imagePath . " bytes=" . $imageSize)
:put ("EVIDENCE scratch-slot=" . $storageRoot . " free-bytes=" . $storageFree . " required-free-bytes=" . $requiredFree)
:if ($storageFree < $requiredFree) do={ :error "scratch storage lacks image-size-times-two plus 64 MiB free space" }

:local runID [:rndstr from="abcdef0123456789" length=16]
:local owner ("chr-envlists-smoke:" . $runID)
:local envListName ("chr-envlists-smoke-env-" . $runID)
:local envKey "CHR_ENVLISTS_SMOKE_RUN_ID"
:local mountListName ("chr-envlists-smoke-mount-" . $runID)
:local mountSource $storageRoot
:local mountDestination ("/chr-envlists-smoke-" . $runID)
:local vethName ("veth-chr-smoke-" . $runID)
:local containerName ("chr-envlists-smoke-" . $runID)
:local rootDirectory ($storageRoot . "/chr-envlists-smoke-" . $runID)
:local vethAddress "192.0.2.2/30"
:local vethGateway "192.0.2.1"
:local rootPattern ("^" . $rootDirectory . "(\$|/)")
:if ($owner ~ "^foxos:" || $envListName ~ "^foxos-" || $vethName ~ "^veth-(foxos|mihomo|mosdns)\$" || $containerName ~ "^foxos-") do={
  :error "internal isolation guard rejected a FoxOS production namespace"
}
:if ([:len [/container/mounts find where list=$mountListName]] > 0 || [:len [/container/mounts find where comment=$owner]] > 0) do={
  :error ("random smoke mount namespace collision before write: " . $runID)
}
:if ([:len [/container find where name=$containerName]] > 0 || [:len [/container find where comment=$owner]] > 0 || [:len [/container/envs find where list=$envListName]] > 0 || [:len [/interface/veth find where name=$vethName]] > 0 || [:len [/file find where name~$rootPattern]] > 0) do={
  :error ("random smoke namespace collision before write: " . $runID)
}
:if ([:len [/interface/veth find where address=$vethAddress]] > 0) do={
  :error ("reserved smoke VETH address is already in use: " . $vethAddress)
}
:if ($imagePath ~ $rootPattern) do={ :error "smoke image cannot be inside the generated root directory" }

:put ("RUN destructive-temporary-namespace=" . $runID)
:put ("RUN owner=" . $owner . " envlist=" . $envListName . " mountlist=" . $mountListName . " veth=" . $vethName . " container=" . $containerName)
:set FoxOSCHREnvlistsSmokeConfirm ""
:set FoxOSCHREnvlistsSmokeImagePath ""
:set FoxOSCHREnvlistsSmokeStorageRoot ""

:local operationComplete false
:local primaryFailure ""
:onerror smokeError in={
  /container/envs/add list=$envListName key=$envKey value=$runID
  :local envItems [/container/envs find where list=$envListName]
  :local envItem [/container/envs find where list=$envListName key=$envKey]
  :if ([:len $envItems] != 1 || [:len $envItem] != 1 || [/container/envs get $envItem value] != $runID) do={
    :error "env add/get readback did not return the exact temporary item"
  }
  :put ("READBACK env-list=" . [/container/envs get $envItem list] . " key=" . [/container/envs get $envItem key] . " value=" . [/container/envs get $envItem value])

  /container/mounts/add list=$mountListName src=$mountSource dst=$mountDestination mode=$writableMountMode comment=$owner
  :local mountByList [/container/mounts find where list=$mountListName]
  :local mountByOwner [/container/mounts find where comment=$owner]
  :if ([:len $mountByList] != 1 || [:len $mountByOwner] != 1 || $mountByList != $mountByOwner || [$mountSourcePath $mountByList] != $mountSource || [/container/mounts get $mountByList dst] != $mountDestination || [$mountMode $mountByList] != $writableMountMode || [/container/mounts get $mountByList comment] != $owner) do={
    :error "mount add/get did not return the exact temporary RW object"
  }
  :put ("READBACK mount-list=" . [/container/mounts get $mountByList list] . " src-raw=" . [/container/mounts get $mountByList src] . " src-normalized=" . [$mountSourcePath $mountByList] . " dst=" . [/container/mounts get $mountByList dst] . " mode=" . [$mountMode $mountByList])

  /interface/veth/add name=$vethName address=$vethAddress gateway=$vethGateway comment=$owner
  :local veth [/interface/veth find where name=$vethName]
  :if ([:len $veth] != 1 || [/interface/veth get $veth comment] != $owner || [/interface/veth get $veth address] != $vethAddress || [/interface/veth get $veth gateway] != $vethGateway) do={
    :error "VETH add/get readback did not return the exact temporary object"
  }
  :put ("READBACK veth=" . [/interface/veth get $veth name] . " address=" . [/interface/veth get $veth address] . " gateway=" . [/interface/veth get $veth gateway])

  /container/add name=$containerName file=$imagePath interface=$vethName root-dir=$rootDirectory envlists=$envListName mountlists=$mountListName logging=no start-on-boot=no comment=$owner
  :local container
  :local containerFound false
  :for discoverAttempt from=1 to=10 do={
    :local byName [/container find where name=$containerName]
    :local byOwner [/container find where comment=$owner]
    :if ([:len $byName] = 1 && [:len $byOwner] = 1 && $byName = $byOwner) do={
      :set container $byName
      :set containerFound true
      :break
    }
    :delay 1s
  }
  :if ($containerFound = false) do={ :error "container add did not create one identity-bound object" }
  :local containerEnvLists [/container get $container envlists]
  :local containerMountLists [/container get $container mountlists]
  :local containerInterface [/container get $container interface]
  :local containerRootReadback [$containerRoot $container]
  :local containerStartOnBoot [/container get $container start-on-boot]
  :local containerLogging [/container get $container logging]
  :put ("READBACK container-id=" . [:pick $container 0] . " name=" . [/container get $container name] . " envlists=" . $containerEnvLists . " mountlists=" . $containerMountLists . " interface=" . $containerInterface . " root-dir=" . $containerRootReadback . " start-on-boot=" . $containerStartOnBoot . " logging=" . $containerLogging)
  :if ($containerEnvLists != $envListName || $containerMountLists != $mountListName || $containerInterface != $vethName || $containerRootReadback != $rootDirectory || ($containerStartOnBoot != false && $containerStartOnBoot != "no") || ($containerLogging != false && $containerLogging != "no")) do={
    :error "container add/get identity, envlists, or mountlists readback mismatch"
  }

  :local importedStopped false
  :for importAttempt from=1 to=120 do={
    :local currentStatus [$containerState $container]
    :if ($currentStatus = "stopped") do={ :set importedStopped true; :break }
    :delay 2s
  }
  :if ($importedStopped = false) do={ :error ("container image did not reach stopped=true; last-state=" . [$containerState $container]) }

  :local stopReturned true
  :onerror stopError in={ /container/stop $container } do={
    :set stopReturned false
    :put ("INFO stop command returned an error for an already-stopped container; status readback decides: " . $stopError)
  }
  :local stopReadback false
  :for stopAttempt from=1 to=12 do={
    :if ([$containerState $container] = "stopped") do={ :set stopReadback true; :break }
    :delay 5s
  }
  :if ($stopReadback = false) do={ :error "container stop did not reach stopped=true within 60 seconds" }
  :put ("READBACK stop-command-returned=" . $stopReturned . " state=" . [$containerState $container])
  :set operationComplete true
} do={
  :set primaryFailure ("operation failed: " . $smokeError)
  :put ("FAIL primary " . $smokeError)
}

:local cleanupFailed false
:local containerGone false
:onerror containerCleanupError in={
  :local cleanupByName [/container find where name=$containerName]
  :local cleanupByOwner [/container find where comment=$owner]
  :if ([:len $cleanupByName] = 0 && [:len $cleanupByOwner] = 0) do={
    :set containerGone true
  } else={
    :if ([:len $cleanupByName] != 1 || [:len $cleanupByOwner] != 1 || $cleanupByName != $cleanupByOwner || [/container get $cleanupByName interface] != $vethName || [/container get $cleanupByName envlists] != $envListName || [/container get $cleanupByName mountlists] != $mountListName || [$containerRoot $cleanupByName] != $rootDirectory || ([/container get $cleanupByName start-on-boot] != false && [/container get $cleanupByName start-on-boot] != "no")) do={
      :error "temporary container identity changed; refusing unbound cleanup"
    }
    :local cleanupContainer $cleanupByName
    :if ([$containerState $cleanupContainer] != "stopped") do={
      :onerror cleanupStopError in={ /container/stop $cleanupContainer } do={ :put ("WARN cleanup stop command failed; status readback decides: " . $cleanupStopError) }
      :local cleanupStopped false
      :for cleanupStopAttempt from=1 to=12 do={
        :if ([$containerState $cleanupContainer] = "stopped") do={ :set cleanupStopped true; :break }
        :delay 5s
      }
      :if ($cleanupStopped = false) do={ :error "temporary container did not stop within 60 seconds during cleanup" }
    }
    /container/remove $cleanupContainer
    :delay 1s
    :if ([:len [/container find where name=$containerName]] > 0 || [:len [/container find where comment=$owner]] > 0) do={
      :error "temporary container remains after remove"
    }
    :set containerGone true
  }
} do={
  :set cleanupFailed true
  :put ("FAIL container cleanup: " . $containerCleanupError)
}

:if ($containerGone) do={
  :onerror mountCleanupError in={
    :local cleanupMountByList [/container/mounts find where list=$mountListName]
    :local cleanupMountByOwner [/container/mounts find where comment=$owner]
    :if ([:len $cleanupMountByList] > 0 || [:len $cleanupMountByOwner] > 0) do={
      :if ([:len $cleanupMountByList] != 1 || [:len $cleanupMountByOwner] != 1 || $cleanupMountByList != $cleanupMountByOwner || [$mountSourcePath $cleanupMountByList] != $mountSource || [/container/mounts get $cleanupMountByList dst] != $mountDestination || [$mountMode $cleanupMountByList] != $writableMountMode || [/container/mounts get $cleanupMountByList comment] != $owner) do={
        :error "temporary mount identity changed; refusing unbound cleanup"
      }
      /container/mounts/remove $cleanupMountByList
    }
    :if ([:len [/container/mounts find where list=$mountListName]] > 0 || [:len [/container/mounts find where comment=$owner]] > 0) do={ :error "temporary mount remains after remove" }
  } do={
    :set cleanupFailed true
    :put ("FAIL mount cleanup: " . $mountCleanupError)
  }

  :onerror vethCleanupError in={
    :local cleanupVeth [/interface/veth find where name=$vethName]
    :if ([:len $cleanupVeth] > 0) do={
      :if ([:len $cleanupVeth] != 1 || [/interface/veth get $cleanupVeth comment] != $owner || [/interface/veth get $cleanupVeth address] != $vethAddress || [/interface/veth get $cleanupVeth gateway] != $vethGateway) do={
        :error "temporary VETH identity changed; refusing unbound cleanup"
      }
      /interface/veth/remove $cleanupVeth
    }
    :if ([:len [/interface/veth find where name=$vethName]] > 0) do={ :error "temporary VETH remains after remove" }
  } do={
    :set cleanupFailed true
    :put ("FAIL VETH cleanup: " . $vethCleanupError)
  }

  :onerror envCleanupError in={
    :local cleanupEnvItems [/container/envs find where list=$envListName]
    :if ([:len $cleanupEnvItems] > 0) do={
      :if ([:len $cleanupEnvItems] != 1 || [/container/envs get $cleanupEnvItems key] != $envKey || [/container/envs get $cleanupEnvItems value] != $runID) do={
        :error "temporary env list identity changed; refusing unbound cleanup"
      }
      /container/envs/remove $cleanupEnvItems
    }
    :if ([:len [/container/envs find where list=$envListName]] > 0) do={ :error "temporary env list remains after remove" }
  } do={
    :set cleanupFailed true
    :put ("FAIL env cleanup: " . $envCleanupError)
  }

  :onerror rootCleanupError in={
    :for rootCleanupPass from=1 to=20 do={
      :local rootEntries [/file find where name~$rootPattern]
      :if ([:len $rootEntries] = 0) do={ :break }
      :foreach rootEntry in=$rootEntries do={
        :onerror rootEntryError in={ /file/remove $rootEntry } do={ :put ("INFO root entry cleanup retry required: " . $rootEntryError) }
      }
    }
    :local remainingRootEntries [/file find where name~$rootPattern]
    :if ([:len $remainingRootEntries] > 0) do={ :error ("temporary root directory still has " . [:len $remainingRootEntries] . " entries") }
  } do={
    :set cleanupFailed true
    :put ("FAIL root-dir cleanup: " . $rootCleanupError)
  }
} else={
  :set cleanupFailed true
  :put "FAIL dependent cleanup skipped because the temporary container remains"
}

:local residualContainers ([:len [/container find where name=$containerName]] + [:len [/container find where comment=$owner]])
:local residualMounts ([:len [/container/mounts find where list=$mountListName]] + [:len [/container/mounts find where comment=$owner]])
:local residualVeths [:len [/interface/veth find where name=$vethName]]
:local residualEnvs [:len [/container/envs find where list=$envListName]]
:local residualRoots [:len [/file find where name~$rootPattern]]
:put ("CLEANUP residual-containers=" . $residualContainers . " residual-mounts=" . $residualMounts . " residual-veths=" . $residualVeths . " residual-envs=" . $residualEnvs . " residual-root-entries=" . $residualRoots)
:if ($operationComplete = false || [:len $primaryFailure] > 0 || $cleanupFailed || $residualContainers > 0 || $residualMounts > 0 || $residualVeths > 0 || $residualEnvs > 0 || $residualRoots > 0) do={
  :error ("CHR_ENVLISTS_SMOKE FAIL run=" . $runID . " primary=" . $primaryFailure . "; discard this CHR after collecting evidence")
}
:put ("CHR_ENVLISTS_SMOKE PASS run=" . $runID . " routeros=" . $routerVersion . " architecture=" . $architecture . " package=" . $containerPackageVersion . " envlists-readback=exact mount-source-readback=normalized mount-mode=rw mountlists-readback=exact cleanup=clean")
