package routerosimage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureEntry struct {
	name     string
	body     string
	typeflag byte
	mode     int64
	uid      int
	linkname string
}

func TestFlatten(t *testing.T) {
	t.Parallel()
	layerOne := layerFixture(t, []fixtureEntry{
		{name: "etc", typeflag: tar.TypeDir, mode: 0o755},
		{name: "etc/keep", body: "old", mode: 0o640, uid: 123},
		{name: "etc/remove", body: "remove me", mode: 0o600},
		{name: "opaque", typeflag: tar.TypeDir, mode: 0o755},
		{name: "opaque/lower", body: "lower", mode: 0o644},
	})
	layerTwo := layerFixture(t, []fixtureEntry{
		{name: "etc/.wh.remove", mode: 0o600},
		{name: "etc/keep", body: "new", mode: 0o600, uid: 234},
		{name: "opaque/.wh..wh..opq", mode: 0o600},
		{name: "opaque/new", body: "upper", mode: 0o644},
		{name: "usr/bin/tool", body: "binary", mode: 0o755},
		{name: "usr/bin/tool-link", typeflag: tar.TypeLink, linkname: "usr/bin/tool", mode: 0o755},
	})
	input := filepath.Join(t.TempDir(), "input.tar")
	writeDockerFixture(t, input, "amd64", [][]byte{layerOne, gzipFixture(t, layerTwo)}, 1)
	output := filepath.Join(t.TempDir(), "output.tar")

	metadata, err := Flatten(input, output, "amd64")
	if err != nil {
		t.Fatalf("Flatten() error = %v", err)
	}
	if metadata.Architecture != "amd64" || metadata.OS != "linux" || metadata.OriginalLayers != 2 {
		t.Fatalf("Flatten() metadata = %#v", metadata)
	}
	outer := readTarFixture(t, mustReadFile(t, output))
	if len(outer) != 3 {
		t.Fatalf("flattened archive entries = %d, want 3", len(outer))
	}
	var manifests []dockerManifest
	if err := json.Unmarshal(outer["manifest.json"].body, &manifests); err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 || len(manifests[0].Layers) != 1 || manifests[0].Layers[0] != "layer.tar" || manifests[0].Config != "config.json" {
		t.Fatalf("flattened manifest = %#v", manifests)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(outer["config.json"].body, &config); err != nil {
		t.Fatal(err)
	}
	var flattenedRoot rootFS
	if err := json.Unmarshal(config["rootfs"], &flattenedRoot); err != nil {
		t.Fatal(err)
	}
	layerDigest := sha256.Sum256(outer["layer.tar"].body)
	wantDigest := "sha256:" + hex.EncodeToString(layerDigest[:])
	if flattenedRoot.Type != "layers" || len(flattenedRoot.DiffIDs) != 1 || flattenedRoot.DiffIDs[0] != wantDigest {
		t.Fatalf("flattened rootfs = %#v, want digest %s", flattenedRoot, wantDigest)
	}
	var runtimeConfig struct {
		Entrypoint []string `json:"Entrypoint"`
		Cmd        []string `json:"Cmd"`
		Env        []string `json:"Env"`
	}
	if err := json.Unmarshal(config["config"], &runtimeConfig); err != nil {
		t.Fatal(err)
	}
	if len(runtimeConfig.Entrypoint) != 1 || runtimeConfig.Entrypoint[0] != "/app" || runtimeConfig.Cmd != nil || len(runtimeConfig.Env) != 1 {
		t.Fatalf("runtime config was not preserved: %#v", runtimeConfig)
	}

	files := readTarFixture(t, outer["layer.tar"].body)
	assertFixtureBody(t, files, "etc/keep", "new")
	assertFixtureBody(t, files, "opaque/new", "upper")
	assertFixtureBody(t, files, "usr/bin/tool", "binary")
	if _, ok := files["etc/remove"]; ok {
		t.Fatal("whiteout target etc/remove remains in flattened layer")
	}
	if _, ok := files["opaque/lower"]; ok {
		t.Fatal("opaque whiteout did not remove lower entry")
	}
	for name := range files {
		if strings.Contains(name, ".wh.") {
			t.Fatalf("whiteout %q leaked into flattened layer", name)
		}
	}
	keep := files["etc/keep"].header
	if keep.Mode != 0o600 || keep.Uid != 234 {
		t.Fatalf("replacement metadata = mode %o uid %d", keep.Mode, keep.Uid)
	}
	link := files["usr/bin/tool-link"].header
	if link.Typeflag != tar.TypeLink || link.Linkname != "usr/bin/tool" {
		t.Fatalf("hardlink metadata = %#v", link)
	}
}

func TestFlattenRejectsUnsafeOrAmbiguousArchives(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		build       func(*testing.T, string)
		wantMessage string
	}{
		{
			name: "architecture mismatch",
			build: func(t *testing.T, input string) {
				writeDockerFixture(t, input, "arm64", [][]byte{layerFixture(t, []fixtureEntry{{name: "app", body: "ok", mode: 0o755}})}, 1)
			},
			wantMessage: "does not match expected",
		},
		{
			name: "multiple images",
			build: func(t *testing.T, input string) {
				writeDockerFixture(t, input, "amd64", [][]byte{layerFixture(t, []fixtureEntry{{name: "app", body: "ok", mode: 0o755}})}, 2)
			},
			wantMessage: "exactly one image",
		},
		{
			name: "layer path traversal",
			build: func(t *testing.T, input string) {
				writeDockerFixture(t, input, "amd64", [][]byte{layerFixture(t, []fixtureEntry{{name: "../escape", body: "bad", mode: 0o600}})}, 1)
			},
			wantMessage: "unsafe archive path",
		},
		{
			name: "outer path traversal",
			build: func(t *testing.T, input string) {
				writeTarFile(t, input, []fixtureEntry{{name: "../manifest.json", body: "[]", mode: 0o600}})
			},
			wantMessage: "unsafe archive path",
		},
		{
			name: "oversized expanded layer entry",
			build: func(t *testing.T, input string) {
				var layer bytes.Buffer
				writer := tar.NewWriter(&layer)
				if err := writer.WriteHeader(&tar.Header{Name: "huge", Mode: 0o600, Size: maxLayerEntryBytes + 1, Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				writeDockerFixture(t, input, "amd64", [][]byte{layer.Bytes()}, 1)
			},
			wantMessage: "exceeds the",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := filepath.Join(t.TempDir(), "input.tar")
			tt.build(t, input)
			_, err := Flatten(input, filepath.Join(t.TempDir(), "output.tar"), "amd64")
			if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("Flatten() error = %v, want message containing %q", err, tt.wantMessage)
			}
		})
	}
}

func TestFlattenComponentContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		expectedComponent string
		entrypoint        []string
		cmd               []string
		env               []string
		labels            map[string]string
		files             []fixtureEntry
		wantMessage       string
	}{
		{
			name:              "valid foxos",
			expectedComponent: "foxos",
			entrypoint:        []string{"/app/foxos"},
			cmd:               []string{"-static", "/app/web", "-database", "/data/foxos.db"},
			labels:            map[string]string{"io.foxos.component": "foxos", "org.opencontainers.image.title": "foxos"},
			files: []fixtureEntry{
				{name: "app/foxos", body: "foxos", mode: 0o755},
				{name: "usr/local/bin/mihomo", body: "mihomo", mode: 0o755},
				{name: "run/secrets/foxos", mode: 0o700, typeflag: tar.TypeDir},
			},
		},
		{
			name:              "valid mihomo",
			expectedComponent: "mihomo",
			entrypoint:        []string{"/mihomo"},
			cmd:               []string{"-d", "/root/.config/mihomo", "-f", "/root/.config/mihomo/config.yaml"},
			labels:            map[string]string{"io.foxos.component": "mihomo", "org.opencontainers.image.title": "mihomo"},
			files:             []fixtureEntry{{name: "mihomo", body: "mihomo", mode: 0o755}},
		},
		{
			name:              "mihomo requires production command",
			expectedComponent: "mihomo",
			entrypoint:        []string{"/mihomo"},
			labels:            map[string]string{"io.foxos.component": "mihomo", "org.opencontainers.image.title": "mihomo"},
			files:             []fixtureEntry{{name: "mihomo", body: "mihomo", mode: 0o755}},
			wantMessage:       "command",
		},
		{
			name:              "foxos image forbids secret environment",
			expectedComponent: "foxos",
			entrypoint:        []string{"/app/foxos"},
			cmd:               []string{"-static", "/app/web", "-database", "/data/foxos.db"},
			env:               []string{"FOXOS_API_TOKEN=must-not-ship"},
			labels:            map[string]string{"io.foxos.component": "foxos", "org.opencontainers.image.title": "foxos"},
			files: []fixtureEntry{
				{name: "app/foxos", body: "foxos", mode: 0o755},
				{name: "usr/local/bin/mihomo", body: "mihomo", mode: 0o755},
				{name: "run/secrets/foxos", mode: 0o700, typeflag: tar.TypeDir},
			},
			wantMessage: "forbidden environment key",
		},
		{
			name:              "foxos image requires an empty secret directory",
			expectedComponent: "foxos",
			entrypoint:        []string{"/app/foxos"},
			cmd:               []string{"-static", "/app/web", "-database", "/data/foxos.db"},
			labels:            map[string]string{"io.foxos.component": "foxos", "org.opencontainers.image.title": "foxos"},
			files: []fixtureEntry{
				{name: "app/foxos", body: "foxos", mode: 0o755},
				{name: "usr/local/bin/mihomo", body: "mihomo", mode: 0o755},
				{name: "run/secrets/foxos", mode: 0o700, typeflag: tar.TypeDir},
				{name: "run/secrets/foxos/api-token", body: strings.Repeat("S", 32), mode: 0o600},
			},
			wantMessage: "built-in production secret",
		},
		{
			name:              "mihomo image forbids built-in production secret",
			expectedComponent: "mihomo",
			entrypoint:        []string{"/mihomo"},
			cmd:               []string{"-d", "/root/.config/mihomo", "-f", "/root/.config/mihomo/config.yaml"},
			labels:            map[string]string{"io.foxos.component": "mihomo", "org.opencontainers.image.title": "mihomo"},
			files: []fixtureEntry{
				{name: "mihomo", body: "mihomo", mode: 0o755},
				{name: "run/secrets/foxos/api-token", body: strings.Repeat("S", 32), mode: 0o600},
			},
			wantMessage: "built-in production secret",
		},
		{
			name:              "mosdns image forbids built-in production secret",
			expectedComponent: "mosdns",
			entrypoint:        []string{"/usr/bin/mosdns", "start", "-d", "/cus/mosdns", "-c", "/cus/mosdns/config_custom.yaml"},
			env:               []string{"MOSDNS_AUTO_INIT=0"},
			labels:            map[string]string{"io.foxos.component": "mosdns", "org.opencontainers.image.title": "mosdns"},
			files: []fixtureEntry{
				{name: "usr/bin/mosdns", body: "mosdns", mode: 0o755},
				{name: "run/secrets/foxos/mihomo-secret", body: strings.Repeat("S", 32), mode: 0o600},
			},
			wantMessage: "built-in production secret",
		},
		{
			name:              "mihomo cannot masquerade as foxos",
			expectedComponent: "foxos",
			entrypoint:        []string{"/mihomo"},
			cmd:               []string{"-d", "/root/.config/mihomo", "-f", "/root/.config/mihomo/config.yaml"},
			labels:            map[string]string{"io.foxos.component": "mihomo", "org.opencontainers.image.title": "mihomo"},
			files:             []fixtureEntry{{name: "mihomo", body: "mihomo", mode: 0o755}},
			wantMessage:       "component \"foxos\" entrypoint",
		},
		{
			name:              "old mosdns external initializer is forbidden",
			expectedComponent: "mosdns",
			entrypoint:        []string{"/usr/bin/mosdns", "start", "-d", "/cus/mosdns", "-c", "/cus/mosdns/config_custom.yaml"},
			env:               []string{"MOSDNS_AUTO_INIT=0", "MOSDNS_CONFIG_INIT_URL=https://example.invalid/config.yaml"},
			labels:            map[string]string{"io.foxos.component": "mosdns", "org.opencontainers.image.title": "mosdns"},
			files:             []fixtureEntry{{name: "usr/bin/mosdns", body: "mosdns", mode: 0o755}},
			wantMessage:       "forbidden environment key",
		},
		{
			name:              "mosdns auto init must be disabled",
			expectedComponent: "mosdns",
			entrypoint:        []string{"/usr/bin/mosdns", "start", "-d", "/cus/mosdns", "-c", "/cus/mosdns/config_custom.yaml"},
			env:               []string{"MOSDNS_AUTO_INIT=1"},
			labels:            map[string]string{"io.foxos.component": "mosdns", "org.opencontainers.image.title": "mosdns"},
			files:             []fixtureEntry{{name: "usr/bin/mosdns", body: "mosdns", mode: 0o755}},
			wantMessage:       "MOSDNS_AUTO_INIT=0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := filepath.Join(t.TempDir(), "input.tar")
			writeComponentDockerFixture(t, input, tt.entrypoint, tt.cmd, tt.env, tt.labels, tt.files)
			_, err := FlattenComponent(input, filepath.Join(t.TempDir(), "output.tar"), "amd64", tt.expectedComponent)
			if tt.wantMessage == "" {
				if err != nil {
					t.Fatalf("FlattenComponent() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("FlattenComponent() error = %v, want message containing %q", err, tt.wantMessage)
			}
		})
	}
}

func TestUnpackDockerArchiveConfinesWritesToRoot(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	input := filepath.Join(parent, "input.tar")
	writeTarFile(t, input, []fixtureEntry{{name: "../escaped", body: "bad", mode: 0o600}})

	root, _, err := unpackDockerArchive(input, filepath.Join(parent, "archive"))
	if root != nil {
		_ = root.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("unpackDockerArchive() error = %v, want unsafe archive path", err)
	}
	if _, statErr := os.Stat(filepath.Join(parent, "escaped")); !os.IsNotExist(statErr) {
		t.Fatalf("archive wrote outside extraction root: %v", statErr)
	}
}

func TestCopySized(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		body        string
		size        int64
		limit       int64
		want        string
		wantMessage string
	}{
		{name: "exact", body: "fox", size: 3, limit: 3, want: "fox"},
		{name: "over limit", body: "foxos", size: 5, limit: 4, wantMessage: "exceeds the 4-byte limit"},
		{name: "truncated", body: "fo", size: 3, limit: 3, wantMessage: "EOF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var destination bytes.Buffer
			err := copySized(&destination, strings.NewReader(tt.body), tt.size, tt.limit, "fixture")
			if tt.wantMessage != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
					t.Fatalf("copySized() error = %v, want message containing %q", err, tt.wantMessage)
				}
				return
			}
			if err != nil {
				t.Fatalf("copySized() error = %v", err)
			}
			if destination.String() != tt.want {
				t.Fatalf("copySized() output = %q, want %q", destination.String(), tt.want)
			}
		})
	}
}

type readFixtureEntry struct {
	header tar.Header
	body   []byte
}

func writeDockerFixture(t *testing.T, destination, architecture string, layers [][]byte, manifestCount int) {
	t.Helper()
	config, err := json.Marshal(map[string]any{
		"architecture": architecture,
		"os":           "linux",
		"config": map[string]any{
			"Entrypoint": []string{"/app"},
			"Env":        []string{"MODE=test"},
		},
		"rootfs":  rootFS{Type: "layers", DiffIDs: []string{"sha256:old"}},
		"history": []map[string]any{{"created_by": "fixture"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := make([]dockerManifest, manifestCount)
	for index := range manifest {
		manifest[index] = dockerManifest{Config: "config.json", RepoTags: []string{"foxos/fixture:test"}}
		for layerIndex := range layers {
			manifest[index].Layers = append(manifest[index].Layers, "layers/"+string(rune('a'+layerIndex))+".tar")
		}
	}
	manifestBody, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	entries := []fixtureEntry{
		{name: "config.json", body: string(config), mode: 0o600},
		{name: "manifest.json", body: string(manifestBody), mode: 0o600},
	}
	for index, layer := range layers {
		entries = append(entries, fixtureEntry{name: "layers/" + string(rune('a'+index)) + ".tar", body: string(layer), mode: 0o600})
	}
	writeTarFile(t, destination, entries)
}

func writeComponentDockerFixture(t *testing.T, destination string, entrypoint, cmd, env []string, labels map[string]string, files []fixtureEntry) {
	t.Helper()
	config, err := json.Marshal(map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"config": map[string]any{
			"Entrypoint": entrypoint,
			"Cmd":        cmd,
			"Env":        env,
			"Labels":     labels,
		},
		"rootfs":  rootFS{Type: "layers", DiffIDs: []string{"sha256:old"}},
		"history": []map[string]any{{"created_by": "component fixture"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestBody, err := json.Marshal([]dockerManifest{{Config: "config.json", RepoTags: []string{"foxos/component:test"}, Layers: []string{"layers/component.tar"}}})
	if err != nil {
		t.Fatal(err)
	}
	writeTarFile(t, destination, []fixtureEntry{
		{name: "config.json", body: string(config), mode: 0o600},
		{name: "manifest.json", body: string(manifestBody), mode: 0o600},
		{name: "layers/component.tar", body: string(layerFixture(t, files)), mode: 0o600},
	})
}

func layerFixture(t *testing.T, entries []fixtureEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		size := int64(len(entry.body))
		if !isRegularEntry(typeflag) {
			size = 0
		}
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Uid: entry.uid, Size: size, Typeflag: typeflag, Linkname: entry.linkname}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := writer.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func gzipFixture(t *testing.T, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeTarFile(t *testing.T, destination string, entries []fixtureEntry) {
	t.Helper()
	output, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(output)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func readTarFixture(t *testing.T, body []byte) map[string]readFixtureEntry {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(body))
	entries := make(map[string]readFixtureEntry)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entryBody, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		entries[header.Name] = readFixtureEntry{header: cloneHeader(header), body: entryBody}
	}
	return entries
}

func assertFixtureBody(t *testing.T, entries map[string]readFixtureEntry, name, want string) {
	t.Helper()
	entry, ok := entries[name]
	if !ok {
		t.Fatalf("flattened layer does not contain %q", name)
	}
	if string(entry.body) != want {
		t.Fatalf("%s body = %q, want %q", name, entry.body, want)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
