package routerosimage

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestBuildRouterOSBundleWithVersionedUpgradePayload(t *testing.T) {
	packageRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Clean(filepath.Join(packageRoot, "..", ".."))
	for _, command := range []string{"bash", "go", "rg", "tar"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("required bundle test command %q is unavailable: %v", command, err)
		}
	}

	fixtureRoot := t.TempDir()
	foxosImage := filepath.Join(fixtureRoot, "foxos-input.tar")
	mihomoImage := filepath.Join(fixtureRoot, "mihomo-input.tar")
	mosdnsImage := filepath.Join(fixtureRoot, "mosdns-input.tar")
	writeComponentDockerFixture(t, foxosImage,
		[]string{"/app/foxos"}, []string{"-static", "/app/web", "-database", "/data/foxos.db"}, nil,
		map[string]string{"io.foxos.component": "foxos", "org.opencontainers.image.title": "foxos"},
		[]fixtureEntry{
			{name: "app/foxos", body: "foxos fixture", mode: 0o755},
			{name: "usr/local/bin/mihomo", body: "validator fixture", mode: 0o755},
			{name: "run/secrets/foxos", mode: 0o700, typeflag: tar.TypeDir},
		})
	writeComponentDockerFixture(t, mihomoImage,
		[]string{"/mihomo"}, []string{"-d", "/root/.config/mihomo", "-f", "/root/.config/mihomo/config.yaml"}, nil,
		map[string]string{"io.foxos.component": "mihomo", "org.opencontainers.image.title": "mihomo"},
		[]fixtureEntry{{name: "mihomo", body: "mihomo fixture", mode: 0o755}})
	writeComponentDockerFixture(t, mosdnsImage,
		[]string{"/usr/bin/mosdns", "start", "-d", "/cus/mosdns", "-c", "/cus/mosdns/config_custom.yaml"},
		nil, []string{"MOSDNS_AUTO_INIT=0"},
		map[string]string{"io.foxos.component": "mosdns", "org.opencontainers.image.title": "mosdns"},
		[]fixtureEntry{{name: "usr/bin/mosdns", body: "mosdns fixture", mode: 0o755}})

	const releaseID = "fixture-rc.1"
	outputRoot := filepath.Join(fixtureRoot, "dist")
	command := exec.CommandContext(t.Context(), filepath.Join(repoRoot, "scripts", "build-routeros-bundle.sh"), outputRoot, releaseID)
	command.Dir = repoRoot
	command.Env = append(os.Environ(),
		"FOXOS_IMAGE="+foxosImage,
		"MIHOMO_IMAGE="+mihomoImage,
		"MOSDNS_IMAGE="+mosdnsImage,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build-routeros-bundle.sh failed: %v\n%s", err, output)
	}

	bundleName := "foxos-full-amd64-" + releaseID
	bundleRoot := filepath.Join(outputRoot, bundleName)
	upgradeName := "foxos-upgrade-" + releaseID
	upgradeRoot := filepath.Join(bundleRoot, upgradeName)
	archivePath := filepath.Join(outputRoot, bundleName+".tar.gz")
	verifySHA256Reference(t, outputRoot, archivePath+".sha256")
	verifySHA256Manifest(t, bundleRoot)
	verifySHA256Manifest(t, upgradeRoot)
	rootManifest, err := os.ReadFile(filepath.Join(bundleRoot, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rootManifest), "./"+upgradeName+"/SHA256SUMS") {
		t.Fatal("root SHA256SUMS does not bind the versioned upgrade payload manifest")
	}
	quickInstall, err := os.ReadFile(filepath.Join(bundleRoot, "QUICK-INSTALL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"disk1/QUICK-INSTALL.md",
		"disk1/chr-envlists-smoke.rsc",
		"disk1/foxos-doctor.rsc",
		"disk1/foxos-trust-ca.rsc",
		"disk1/mihomo-config/config.yaml",
		"disk1/mihomo-config/base.yaml",
		"disk1/mosdns-config/config_custom.yaml",
		"/system/backup/save name=before-foxos-YYYYMMDD-HHMM",
		"UPLOAD-COLLISIONS total=0",
		`/console/inspect request=completion input="/container/add "`,
		"CHR_ENVLISTS_SMOKE PASS",
		"PASS|summary|needs-action-count=0|conflict-count=0|first-install=ready-for-plan",
		"APPROVED UPGRADE SHA-512",
		"APPROVED PROMOTE SHA-512",
		"APPROVED ROLLBACK SHA-512",
		"APPROVED CLEANUP SHA-512",
		"upgrade-cleanup-apply.rsc",
		"foxos-secrets/api-token",
		"FoxOSCATrustConfirmation",
		"23/24",
	} {
		if !strings.Contains(string(quickInstall), required) {
			t.Errorf("packaged QUICK-INSTALL is missing %q", required)
		}
	}
	if strings.Contains(string(quickInstall), "../../docs/") {
		t.Error("packaged QUICK-INSTALL links outside the standalone release bundle")
	}
	for _, forbidden := range []string{
		`:put [/container/envs get [find where list="foxos-env" key="FOXOS_API_TOKEN"] value]`,
		"27 键 FoxOS env",
		"28 键",
	} {
		if strings.Contains(string(quickInstall), forbidden) {
			t.Errorf("packaged QUICK-INSTALL contains obsolete secret handling %q", forbidden)
		}
	}
	releaseManifest, err := os.ReadFile(filepath.Join(bundleRoot, "RELEASE-MANIFEST.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"mounted read-only at /run/secrets/foxos",
		"production-secret-env: forbidden",
		"device-generated secret mount",
		"imports only a verified disposable copy",
	} {
		if !strings.Contains(string(releaseManifest), required) {
			t.Errorf("release manifest is missing secret contract %q", required)
		}
	}

	assertDirectoryEntries(t, bundleRoot, []string{
		"QUICK-INSTALL.md",
		"RELEASE-MANIFEST.txt",
		"SHA256SUMS",
		"chr-envlists-smoke.md",
		"chr-envlists-smoke.rsc",
		"foxos-dns-apply.rsc",
		"foxos-dns-plan.rsc",
		"foxos-doctor.rsc",
		"foxos-full-install.rsc",
		"foxos-install-inspect.rsc",
		"foxos-plan.rsc",
		"foxos-start-all.rsc",
		"foxos-trust-ca.rsc",
		"foxos-uninstall-inspect.rsc",
		"foxos-verify.rsc",
		"load-site-config.rsc",
		"mihomo-config",
		"mihomo_amd64.tar",
		"mosdns-amd64.tar",
		"mosdns-config",
		"preflight.rsc",
		"provenance",
		"seal-site-config.sh",
		"site-config.example.rsc",
		"uninstall-apply.rsc",
		"uninstall-plan.rsc",
		upgradeName,
	})
	assertDirectoryEntries(t, upgradeRoot, []string{
		"SHA256SUMS",
		"UPGRADE-MANIFEST.txt",
		"foxos-amd64.tar",
		"rollback-inspect.rsc",
		"rollback-plan.rsc",
		"rollback.rsc",
		"upgrade-cleanup-apply.rsc",
		"upgrade-cleanup-inspect.rsc",
		"upgrade-cleanup-plan.rsc",
		"upgrade-inspect.rsc",
		"upgrade-plan.rsc",
		"upgrade-promote-inspect.rsc",
		"upgrade-promote-plan.rsc",
		"upgrade-promote.rsc",
		"upgrade.rsc",
	})
	for _, relative := range []string{
		"mihomo-config/config.yaml",
		"mihomo-config/base.yaml",
		"mosdns-config/config_custom.yaml",
	} {
		info, err := os.Stat(filepath.Join(bundleRoot, relative))
		if err != nil {
			t.Errorf("required runtime config %q is missing: %v", relative, err)
		} else if !info.Mode().IsRegular() || info.Size() == 0 {
			t.Errorf("required runtime config %q is not a non-empty regular file", relative)
		}
	}

	err = filepath.WalkDir(bundleRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".tar") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), "__FOXOS_RELEASE_ID__") {
			return fmt.Errorf("unresolved release placeholder in %s", path)
		}
		switch entry.Name() {
		case "api-token", "confirmation-key", "routeros-password", "mihomo-secret":
			return fmt.Errorf("bundle contains forbidden production secret file name %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	archiveEntries := readGzipTarEntries(t, archivePath)
	for _, required := range []string{
		bundleName + "/SHA256SUMS",
		bundleName + "/" + upgradeName + "/SHA256SUMS",
		bundleName + "/" + upgradeName + "/foxos-amd64.tar",
	} {
		if _, ok := archiveEntries[required]; !ok {
			t.Errorf("bundle archive is missing %q", required)
		}
	}
}

func verifySHA256Reference(t *testing.T, root, manifestPath string) {
	t.Helper()
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(body))
	if len(fields) != 2 {
		t.Fatalf("%s has %d fields, want 2", manifestPath, len(fields))
	}
	verifySHA256File(t, filepath.Join(root, strings.TrimPrefix(fields[1], "*")), fields[0])
}

func verifySHA256Manifest(t *testing.T, root string) {
	t.Helper()
	manifestPath := filepath.Join(root, "SHA256SUMS")
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]struct{})
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("%s line %d has %d fields, want 2", manifestPath, lineNumber+1, len(fields))
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./")
		clean := filepath.Clean(relative)
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			t.Fatalf("%s line %d contains unsafe path %q", manifestPath, lineNumber+1, fields[1])
		}
		if _, duplicate := seen[clean]; duplicate {
			t.Fatalf("%s lists %q more than once", manifestPath, clean)
		}
		seen[clean] = struct{}{}
		verifySHA256File(t, filepath.Join(root, clean), fields[0])
	}
	if len(seen) == 0 {
		t.Fatalf("%s is empty", manifestPath)
	}
}

func verifySHA256File(t *testing.T, path, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	if digest != want {
		t.Fatalf("SHA-256 for %s = %s, want %s", path, digest, want)
	}
}

func assertDirectoryEntries(t *testing.T, root string, want []string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("directory entries for %s:\n got: %q\nwant: %q", root, got, want)
	}
}

func readGzipTarEntries(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	entries := make(map[string]struct{})
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries[strings.TrimSuffix(header.Name, "/")] = struct{}{}
	}
	return entries
}
