# Import and trust the persistent FoxOS CA through a disposable root-level copy.
# FoxOSCATrustConfirmation must be "TRUST <lowercase SHA-256 fingerprint>".

:global FoxOSSiteManifestVersion
:global FoxOSSiteStorageRoot
:global FoxOSCATrustConfirmation
:if ($FoxOSSiteManifestVersion != 2) do={ :error "load the immutable load-site-config.rsc before trusting the FoxOS CA" }
/import file-name=($FoxOSSiteStorageRoot . "/load-site-config.rsc")
:if ($FoxOSSiteManifestVersion != 2) do={ :error "site manifest compatibility contract is unavailable" }

:local confirmation $FoxOSCATrustConfirmation
:set FoxOSCATrustConfirmation ""
:if ([:typeof $confirmation] != "str" || [:len $confirmation] != 70 || [:pick $confirmation 0 6] != "TRUST ") do={
  :error "set FoxOSCATrustConfirmation to TRUST followed by the verified 64-character lowercase SHA-256 fingerprint"
}

:local sourcePath ($FoxOSSiteStorageRoot . "/foxos-data/tls/foxos-local-ca.pem")
:local temporaryName "foxos-local-ca-import.pem"
:local importedNamePrefix "^foxos-local-ca-import.pem_"
:local canonicalName "foxos-local-ca"
:local source [/file find where name=$sourcePath]
:if ([:len $source] != 1 || [/file get $source type] != "file") do={
  :error ("persistent FoxOS CA is missing, ambiguous, or not a regular file: " . $sourcePath)
}
:local sourceSize [/file get $source size]
:if ($sourceSize < 256 || $sourceSize > 16384) do={ :error "persistent FoxOS CA size is outside the accepted range" }
:local sourceContents [/file get $source contents]
:local beginMarker "-----BEGIN CERTIFICATE-----"
:local endMarker "-----END CERTIFICATE-----"
:local firstBegin [:find $sourceContents $beginMarker]
:local firstEnd [:find $sourceContents $endMarker]
:if ([:typeof $firstBegin] = "nil" || [:typeof $firstEnd] = "nil" || [:typeof [:find $sourceContents $beginMarker ($firstBegin + [:len $beginMarker])]] != "nil" || [:typeof [:find $sourceContents $endMarker ($firstEnd + [:len $endMarker])]] != "nil" || [:typeof [:find $sourceContents "PRIVATE KEY"]] != "nil") do={
  :error "persistent FoxOS CA is not a public certificate-only PEM"
}
:local sourceDigest [:convert $sourceContents transform=sha512 to=hex]

:local certificateBefore [/certificate find]
:local caBefore [/certificate find where common-name="FoxOS Local CA"]
:if ([:len $caBefore] > 1) do={ :error "multiple FoxOS Local CA certificates already exist" }
:local canonicalBefore [/certificate find where name=$canonicalName]
:if ([:len $canonicalBefore] > 1 || ([:len $canonicalBefore] = 1 && $canonicalBefore != $caBefore)) do={
  :error "canonical FoxOS CA certificate name is occupied by another certificate"
}
:local reservedBefore [/certificate find where name~$importedNamePrefix]
:if ([:len $reservedBefore] > 1 || ([:len $reservedBefore] = 1 && $reservedBefore != $caBefore)) do={
  :error "reserved FoxOS CA import certificate name is occupied"
}
:if ([:len $reservedBefore] = 1) do={
  :local reservedTrusted [/certificate get $reservedBefore trusted]
  :local reservedPrivateKey [/certificate get $reservedBefore private-key]
  :if ($reservedTrusted = true || $reservedTrusted = "yes" || $reservedPrivateKey = true || $reservedPrivateKey = "yes") do={
    :error "interrupted FoxOS CA import evidence is not an untrusted public certificate"
  }
}

:local temporary [/file find where name=$temporaryName]
:if ([:len $temporary] > 1) do={ :error "temporary FoxOS CA import file is ambiguous" }
:if ([:len $temporary] = 1) do={
  :if ([/file get $temporary type] != "file" || [/file get $temporary size] != $sourceSize || [:convert [/file get $temporary contents] transform=sha512 to=hex] != $sourceDigest) do={
    :error "temporary FoxOS CA import file collides with different content"
  }
} else={
  /file copy number=$source name=$temporaryName
  :set temporary [/file find where name=$temporaryName]
  :if ([:len $temporary] != 1 || [/file get $temporary type] != "file" || [/file get $temporary size] != $sourceSize || [:convert [/file get $temporary contents] transform=sha512 to=hex] != $sourceDigest) do={
    :error "temporary FoxOS CA copy creation or readback failed"
  }
}

:local importErrorMessage ""
:onerror importError in={
  /certificate/import file-name=$temporaryName passphrase="" trusted=no
} do={ :set importErrorMessage $importError }

:local validationError $importErrorMessage
:local sourceAfter [/file find where name=$sourcePath]
:if ($validationError = "" && ([:len $sourceAfter] != 1 || $sourceAfter != $source || [/file get $sourceAfter type] != "file" || [/file get $sourceAfter size] != $sourceSize || [:convert [/file get $sourceAfter contents] transform=sha512 to=hex] != $sourceDigest)) do={
  :set validationError "persistent FoxOS CA changed or was consumed during import"
}
:local temporaryAfter [/file find where name=$temporaryName]
:if ($validationError = "" && [:len $temporaryAfter] != 0) do={ :set validationError "temporary FoxOS CA import file was not consumed" }

:local certificateAfter [/certificate find]
:local reservedAfter [/certificate find where name~$importedNamePrefix]
:local newCertificateCount 0
:local newCertificate ""
:foreach candidate in=$certificateAfter do={
  :local existed false
  :foreach previous in=$certificateBefore do={ :if ($candidate = $previous) do={ :set existed true } }
  :if ($existed = false) do={
    :set newCertificateCount ($newCertificateCount + 1)
    :set newCertificate $candidate
  }
}
:local expectedNewCertificateCount 1
:if ([:len $caBefore] = 1) do={ :set expectedNewCertificateCount 0 }
:if ($validationError = "" && $newCertificateCount != $expectedNewCertificateCount) do={ :set validationError "CA import certificate delta does not match the frozen pre-import state" }

:local caAfter [/certificate find where common-name="FoxOS Local CA"]
:local trustedCA ""
:if ($validationError = "" && [:len $caAfter] != 1) do={ :set validationError "import did not converge to exactly one FoxOS Local CA" }
:if ($validationError = "") do={
  :set trustedCA [:pick $caAfter 0]
  :if ($expectedNewCertificateCount = 1 && $newCertificate != $trustedCA) do={ :set validationError "newly imported certificate is not the unique FoxOS Local CA" }
  :local fingerprint [/certificate get $trustedCA fingerprint]
  :local privateKey [/certificate get $trustedCA private-key]
  :local expired [/certificate get $trustedCA expired]
  :local revoked [/certificate get $trustedCA revoked]
  :local keyUsage [:tostr [/certificate get $trustedCA key-usage]]
  :if ([:typeof $fingerprint] != "str" || [:len $fingerprint] != 64) do={ :set validationError "FoxOS CA SHA-256 fingerprint is invalid" }
  :if ($validationError = "" && $confirmation != ("TRUST " . $fingerprint)) do={ :set validationError "FoxOS CA fingerprint confirmation does not match" }
  :if ($validationError = "" && ([/certificate get $trustedCA key-type] != "ec" || [/certificate get $trustedCA key-size] != "prime256v1" || [/certificate get $trustedCA organization] != "FoxOS local network")) do={ :set validationError "FoxOS CA certificate identity is invalid" }
  :if ($validationError = "" && ([:typeof [:find $keyUsage "key-cert-sign"]] = "nil" || [:typeof [:find $keyUsage "crl-sign"]] = "nil")) do={ :set validationError "FoxOS CA key usage is invalid" }
  :if ($validationError = "" && ($privateKey = true || $privateKey = "yes" || $expired = true || $expired = "yes" || $revoked = true || $revoked = "yes")) do={ :set validationError "FoxOS CA is private-key-bearing, expired, or revoked" }
}

:if ($validationError != "") do={
  :local cleanupFailed false
  :foreach candidate in=$certificateAfter do={
    :local existed false
    :foreach previous in=$certificateBefore do={ :if ($candidate = $previous) do={ :set existed true } }
    :if ($existed = false) do={
      :local candidateIsReserved false
      :foreach reserved in=$reservedAfter do={ :if ($candidate = $reserved) do={ :set candidateIsReserved true } }
      :if ($candidateIsReserved = false) do={
        :set cleanupFailed true
      } else={
        :local candidateTrusted [/certificate get $candidate trusted]
        :local candidatePrivateKey [/certificate get $candidate private-key]
        :if ($candidateTrusted = true || $candidateTrusted = "yes" || $candidatePrivateKey = true || $candidatePrivateKey = "yes") do={
          :set cleanupFailed true
        } else={
          :onerror cleanupCertificateError in={ /certificate remove $candidate } do={ :set cleanupFailed true }
        }
      }
    }
  }
  :set temporaryAfter [/file find where name=$temporaryName]
  :if ([:len $temporaryAfter] = 1 && [/file get $temporaryAfter type] = "file" && [/file get $temporaryAfter size] = $sourceSize && [:convert [/file get $temporaryAfter contents] transform=sha512 to=hex] = $sourceDigest) do={
    :onerror cleanupFileError in={ /file remove $temporaryAfter } do={ :set cleanupFailed true }
  }
  :if ([:len [/file find where name=$temporaryName]] != 0) do={ :set cleanupFailed true }
  :if ($cleanupFailed) do={ :error ("FoxOS CA trust failed and compensation is incomplete: " . $validationError) }
  :error ("FoxOS CA trust failed without changing trusted state: " . $validationError)
}

:local fingerprint [/certificate get $trustedCA fingerprint]
:put ("FOXOS CA APPROVED SHA-256 FINGERPRINT " . $fingerprint)
:local trustErrorMessage ""
:onerror trustError in={
  :if ([/certificate get $trustedCA name] != $canonicalName) do={ /certificate set $trustedCA name=$canonicalName }
  :local alreadyTrusted [/certificate get $trustedCA trusted]
  :if ($alreadyTrusted != true && $alreadyTrusted != "yes") do={ /certificate set $trustedCA trusted=yes }
} do={ :set trustErrorMessage $trustError }
:if ($trustErrorMessage != "") do={ :error ("FoxOS CA confirmation matched, but canonical trust update failed: " . $trustErrorMessage) }

:local finalCA [/certificate find where name=$canonicalName common-name="FoxOS Local CA"]
:local finalSource [/file find where name=$sourcePath]
:if ([:len $finalCA] != 1 || $finalCA != $caAfter || ([/certificate get $finalCA trusted] != true && [/certificate get $finalCA trusted] != "yes") || [/certificate get $finalCA fingerprint] != $fingerprint) do={
  :error "FoxOS CA trusted certificate readback failed"
}
:if ([:len $finalSource] != 1 || $finalSource != $source || [/file get $finalSource size] != $sourceSize || [:convert [/file get $finalSource contents] transform=sha512 to=hex] != $sourceDigest || [:len [/file find where name=$temporaryName]] != 0) do={
  :error "FoxOS CA persistent source or temporary-file final readback failed"
}
:put ("FOXOS CA TRUST PASS source=" . $sourcePath . " fingerprint=" . $fingerprint . " temporary-residual=0")
