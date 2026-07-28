# FoxOS RouterOS 7.21+ envlists compatibility smoke test.
# DESTRUCTIVE: run only on a disposable CHR. Never run on a production router.
# This script creates and removes an isolated env list, VETH, container, and
# container root directory. It never imports site configuration and never uses
# FoxOS production object names or ownership comments.
# This standalone compatibility gate intentionally does not load site topology.

:global FoxOSCHREnvlistsSmokeConfirm
:global FoxOSCHREnvlistsSmokeImagePath
:global FoxOSCHREnvlistsSmokeStorageRoot

:put "=== FoxOS CHR envlists destructive smoke test ==="
:put "WARNING disposable CHR only; abort on every production RouterOS device."
:put "=== RouterOS evidence: system resource ==="
/system/resource/print
:put "=== RouterOS evidence: container package ==="
/system/package/print detail where name="container"
:put "=== RouterOS evidence: device-mode ==="
/system/device-mode/print

:local routerVersion [/system/resource get version]
:local architecture [/system/resource get architecture-name]
:local boardName [/system/resource get board-name]
:local versionBase $routerVersion
:local versionSpace [:find $versionBase " "]
:if ([:typeof $versionSpace] != "nil") do={ :set versionBase [:pick $versionBase 0 $versionSpace] }
:put ("EVIDENCE routeros-version-full=" . $routerVersion)
:put ("EVIDENCE architecture-name=" . $architecture)
:put ("EVIDENCE board-name=" . $boardName)

:if ($boardName != "CHR") do={ :error "REFUSED: board-name is not CHR; production hardware is forbidden" }
:if ($architecture != "x86") do={ :error ("REFUSED: this FoxOS amd64 gate requires CHR architecture-name=x86, got " . $architecture) }

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
:if ([:typeof $imagePath] != "str" || [:len $imagePath] < 3 || [:len $imagePath] > 220 || $imagePath !~ "^[A-Za-z0-9][A-Za-z0-9._/-]*$" || [:typeof [:find $imagePath ".."]] != "nil") do={
  :error "FoxOSCHREnvlistsSmokeImagePath must be a bounded relative RouterOS file path"
}
:if ([:typeof $storageRoot] != "str" || [:len $storageRoot] < 1 || [:len $storageRoot] > 63 || $storageRoot !~ "^[A-Za-z0-9][A-Za-z0-9_-]*$") do={
  :error "FoxOSCHREnvlistsSmokeStorageRoot must be one mounted slot name"
}
:if ($imagePath !~ ("^" . $storageRoot . "/")) do={
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
:local vethName ("veth-chr-smoke-" . $runID)
:local containerName ("chr-envlists-smoke-" . $runID)
:local rootDirectory ($storageRoot . "/chr-envlists-smoke-" . $runID)
:local vethAddress "192.0.2.2/30"
:local vethGateway "192.0.2.1"
:local rootPattern ("^" . $rootDirectory . "($|/)")
:if ($owner ~ "^foxos:" || $envListName ~ "^foxos-" || $vethName ~ "^veth-(foxos|mihomo|mosdns)$" || $containerName ~ "^foxos-") do={
  :error "internal isolation guard rejected a FoxOS production namespace"
}
:if ([:len [/container find where name=$containerName]] > 0 || [:len [/container find where comment=$owner]] > 0 || [:len [/container/envs find where list=$envListName]] > 0 || [:len [/interface/veth find where name=$vethName]] > 0 || [:len [/file find where name~$rootPattern]] > 0) do={
  :error ("random smoke namespace collision before write: " . $runID)
}
:if ([:len [/interface/veth find where address=$vethAddress]] > 0) do={
  :error ("reserved smoke VETH address is already in use: " . $vethAddress)
}
:if ($imagePath ~ $rootPattern) do={ :error "smoke image cannot be inside the generated root directory" }

:put ("RUN destructive-temporary-namespace=" . $runID)
:put ("RUN owner=" . $owner . " envlist=" . $envListName . " veth=" . $vethName . " container=" . $containerName)
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

  /interface/veth/add name=$vethName address=$vethAddress gateway=$vethGateway comment=$owner
  :local veth [/interface/veth find where name=$vethName]
  :if ([:len $veth] != 1 || [/interface/veth get $veth comment] != $owner || [/interface/veth get $veth address] != $vethAddress || [/interface/veth get $veth gateway] != $vethGateway) do={
    :error "VETH add/get readback did not return the exact temporary object"
  }
  :put ("READBACK veth=" . [/interface/veth get $veth name] . " address=" . [/interface/veth get $veth address] . " gateway=" . [/interface/veth get $veth gateway])

  /container/add name=$containerName file=$imagePath interface=$vethName root-dir=$rootDirectory envlists=$envListName logging=no start-on-boot=no comment=$owner
  :local container
  :local containerFound false
  :for discoverAttempt from=1 to=10 do={
    :local byName [/container find where name=$containerName]
    :local byOwner [/container find where comment=$owner]
    :if ([:len $byName] = 1 && [:len $byOwner] = 1 && [/container get $byName .id] = [/container get $byOwner .id]) do={
      :set container $byName
      :set containerFound true
      :break
    }
    :delay 1s
  }
  :if ($containerFound = false) do={ :error "container add did not create one identity-bound object" }
  :local containerEnvLists [/container get $container envlists]
  :local containerInterface [/container get $container interface]
  :local containerRoot [/container get $container root-dir]
  :local containerStartOnBoot [/container get $container start-on-boot]
  :local containerLogging [/container get $container logging]
  :put ("READBACK container-id=" . [/container get $container .id] . " name=" . [/container get $container name] . " envlists=" . $containerEnvLists . " interface=" . $containerInterface . " root-dir=" . $containerRoot . " start-on-boot=" . $containerStartOnBoot . " logging=" . $containerLogging)
  :if ($containerEnvLists != $envListName || $containerInterface != $vethName || $containerRoot != $rootDirectory || ($containerStartOnBoot != false && $containerStartOnBoot != "no") || ($containerLogging != false && $containerLogging != "no")) do={
    :error "container add/get identity or envlists readback mismatch"
  }

  :local importedStopped false
  :for importAttempt from=1 to=120 do={
    :local currentStatus [/container get $container status]
    :if ($currentStatus = "stopped") do={ :set importedStopped true; :break }
    :delay 2s
  }
  :if ($importedStopped = false) do={ :error ("container image did not reach status=stopped; last-status=" . [/container get $container status]) }

  :local stopReturned true
  :onerror stopError in={ /container/stop $container } do={
    :set stopReturned false
    :put ("INFO stop command returned an error for an already-stopped container; status readback decides: " . $stopError)
  }
  :local stopReadback false
  :for stopAttempt from=1 to=12 do={
    :if ([/container get $container status] = "stopped") do={ :set stopReadback true; :break }
    :delay 5s
  }
  :if ($stopReadback = false) do={ :error "container stop did not reach status=stopped within 60 seconds" }
  :put ("READBACK stop-command-returned=" . $stopReturned . " status=" . [/container get $container status])
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
    :if ([:len $cleanupByName] != 1 || [:len $cleanupByOwner] != 1 || [/container get $cleanupByName .id] != [/container get $cleanupByOwner .id] || [/container get $cleanupByName interface] != $vethName || [/container get $cleanupByName root-dir] != $rootDirectory || ([/container get $cleanupByName start-on-boot] != false && [/container get $cleanupByName start-on-boot] != "no")) do={
      :error "temporary container identity changed; refusing unbound cleanup"
    }
    :local cleanupContainer $cleanupByName
    :if ([/container get $cleanupContainer status] != "stopped") do={
      :onerror cleanupStopError in={ /container/stop $cleanupContainer } do={ :put ("WARN cleanup stop command failed; status readback decides: " . $cleanupStopError) }
      :local cleanupStopped false
      :for cleanupStopAttempt from=1 to=12 do={
        :if ([/container get $cleanupContainer status] = "stopped") do={ :set cleanupStopped true; :break }
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
:local residualVeths [:len [/interface/veth find where name=$vethName]]
:local residualEnvs [:len [/container/envs find where list=$envListName]]
:local residualRoots [:len [/file find where name~$rootPattern]]
:put ("CLEANUP residual-containers=" . $residualContainers . " residual-veths=" . $residualVeths . " residual-envs=" . $residualEnvs . " residual-root-entries=" . $residualRoots)
:if ($operationComplete = false || [:len $primaryFailure] > 0 || $cleanupFailed || $residualContainers > 0 || $residualVeths > 0 || $residualEnvs > 0 || $residualRoots > 0) do={
  :error ("CHR_ENVLISTS_SMOKE FAIL run=" . $runID . " primary=" . $primaryFailure . "; discard this CHR after collecting evidence")
}
:put ("CHR_ENVLISTS_SMOKE PASS run=" . $runID . " routeros=" . $routerVersion . " architecture=" . $architecture . " package=" . $containerPackageVersion . " envlists-readback=exact cleanup=clean")
