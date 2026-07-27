package routerosimage

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxArchiveEntries          = 100_000
	maxImageMetadataBytes      = 4 << 20
	maxDockerArchiveEntryBytes = 2 << 30
	maxDockerArchiveBytes      = 4 << 30
	maxLayerEntryBytes         = 1 << 30
	maxExpandedLayerBytes      = 2 << 30
)

type Metadata struct {
	Architecture   string
	OS             string
	RepoTags       []string
	OriginalLayers int
}

type dockerManifest struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

type rootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"`
}

type stagedEntry struct {
	header   tar.Header
	dataPath string
}

type whiteout struct {
	target string
	opaque bool
}

func Flatten(inputPath, outputPath, expectedArchitecture string) (Metadata, error) {
	if strings.TrimSpace(inputPath) == "" || strings.TrimSpace(outputPath) == "" {
		return Metadata{}, errors.New("input and output paths are required")
	}
	work, err := os.MkdirTemp("", "foxos-routeros-image-*")
	if err != nil {
		return Metadata{}, err
	}
	defer func() {
		_ = os.RemoveAll(work)
	}()

	archiveRoot, files, err := unpackDockerArchive(inputPath, filepath.Join(work, "archive"))
	if err != nil {
		return Metadata{}, err
	}
	defer archiveRoot.Close()
	if _, ok := files["manifest.json"]; !ok {
		return Metadata{}, errors.New("docker archive does not contain manifest.json")
	}
	manifestBody, err := readRootFileAtMost(archiveRoot, "manifest.json", maxImageMetadataBytes)
	if err != nil {
		return Metadata{}, err
	}
	var manifests []dockerManifest
	if err := json.Unmarshal(manifestBody, &manifests); err != nil {
		return Metadata{}, fmt.Errorf("decode manifest.json: %w", err)
	}
	if len(manifests) != 1 {
		return Metadata{}, fmt.Errorf("docker archive must contain exactly one image, got %d", len(manifests))
	}
	manifest := manifests[0]
	if len(manifest.Layers) == 0 {
		return Metadata{}, errors.New("docker archive image has no layers")
	}
	configName, err := cleanArchivePath(manifest.Config)
	if err != nil {
		return Metadata{}, fmt.Errorf("invalid config path: %w", err)
	}
	if _, ok := files[configName]; !ok {
		return Metadata{}, fmt.Errorf("docker archive is missing config %q", configName)
	}
	configBody, err := readRootFileAtMost(archiveRoot, configName, maxImageMetadataBytes)
	if err != nil {
		return Metadata{}, err
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(configBody, &config); err != nil {
		return Metadata{}, fmt.Errorf("decode image config: %w", err)
	}
	var architecture string
	var operatingSystem string
	if err := json.Unmarshal(config["architecture"], &architecture); err != nil || architecture == "" {
		return Metadata{}, errors.New("image config has no valid architecture")
	}
	if err := json.Unmarshal(config["os"], &operatingSystem); err != nil || operatingSystem == "" {
		return Metadata{}, errors.New("image config has no valid operating system")
	}
	if expectedArchitecture != "" && architecture != expectedArchitecture {
		return Metadata{}, fmt.Errorf("image architecture %q does not match expected %q", architecture, expectedArchitecture)
	}
	if operatingSystem != "linux" {
		return Metadata{}, fmt.Errorf("RouterOS container image must target linux, got %q", operatingSystem)
	}

	entries := make(map[string]stagedEntry)
	dataRoot := filepath.Join(work, "data")
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return Metadata{}, err
	}
	sequence := 0
	for _, layerName := range manifest.Layers {
		cleanLayerName, err := cleanArchivePath(layerName)
		if err != nil {
			return Metadata{}, fmt.Errorf("invalid layer path %q: %w", layerName, err)
		}
		if _, ok := files[cleanLayerName]; !ok {
			return Metadata{}, fmt.Errorf("docker archive is missing layer %q", cleanLayerName)
		}
		layerEntries, whiteouts, nextSequence, err := readLayer(archiveRoot, cleanLayerName, dataRoot, sequence)
		if err != nil {
			return Metadata{}, fmt.Errorf("read layer %q: %w", cleanLayerName, err)
		}
		sequence = nextSequence
		for _, item := range whiteouts {
			if item.opaque {
				removeChildren(entries, item.target)
			} else {
				removePath(entries, item.target)
			}
		}
		for _, entry := range layerEntries {
			if entry.header.Typeflag != tar.TypeDir {
				removeChildren(entries, entry.header.Name)
			}
			entries[entry.header.Name] = entry
		}
	}

	flattenedLayer := filepath.Join(work, "layer.tar")
	if err := writeLayer(flattenedLayer, entries); err != nil {
		return Metadata{}, err
	}
	digest, err := fileSHA256(flattenedLayer)
	if err != nil {
		return Metadata{}, err
	}
	updatedRootFS, err := json.Marshal(rootFS{Type: "layers", DiffIDs: []string{"sha256:" + digest}})
	if err != nil {
		return Metadata{}, err
	}
	config["rootfs"] = updatedRootFS
	history, err := json.Marshal([]map[string]any{{"created_by": "FoxOS RouterOS single-layer flatten"}})
	if err != nil {
		return Metadata{}, err
	}
	config["history"] = history
	flattenedConfig, err := json.Marshal(config)
	if err != nil {
		return Metadata{}, fmt.Errorf("encode flattened image config: %w", err)
	}
	flattenedManifest, err := json.Marshal([]dockerManifest{{Config: "config.json", RepoTags: manifest.RepoTags, Layers: []string{"layer.tar"}}})
	if err != nil {
		return Metadata{}, fmt.Errorf("encode flattened manifest: %w", err)
	}
	if err := writeDockerArchive(outputPath, flattenedConfig, flattenedManifest, flattenedLayer); err != nil {
		return Metadata{}, err
	}
	return Metadata{Architecture: architecture, OS: operatingSystem, RepoTags: append([]string(nil), manifest.RepoTags...), OriginalLayers: len(manifest.Layers)}, nil
}

func unpackDockerArchive(inputPath, outputRoot string) (*os.Root, map[string]struct{}, error) {
	// #nosec G304 -- inputPath is the operator-selected local archive passed to
	// the CLI; archive member paths are validated independently below.
	input, err := os.Open(inputPath)
	if err != nil {
		return nil, nil, err
	}
	defer input.Close()
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(outputRoot)
	if err != nil {
		return nil, nil, err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = root.Close()
		}
	}()
	reader := tar.NewReader(input)
	files := make(map[string]struct{})
	entries := 0
	var extractedBytes int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read docker archive: %w", err)
		}
		entries++
		if entries > maxArchiveEntries {
			return nil, nil, errors.New("docker archive contains too many entries")
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if !isRegularEntry(header.Typeflag) {
			return nil, nil, fmt.Errorf("unsupported docker archive entry type for %q", header.Name)
		}
		if header.Size < 0 || header.Size > maxDockerArchiveEntryBytes {
			return nil, nil, fmt.Errorf("docker archive entry %q exceeds the %d-byte limit", header.Name, maxDockerArchiveEntryBytes)
		}
		if extractedBytes > maxDockerArchiveBytes-header.Size {
			return nil, nil, fmt.Errorf("docker archive expands beyond the %d-byte limit", maxDockerArchiveBytes)
		}
		extractedBytes += header.Size
		name, err := cleanArchivePath(header.Name)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := files[name]; exists {
			return nil, nil, fmt.Errorf("duplicate docker archive entry %q", name)
		}
		relativeName := filepath.FromSlash(name)
		if err := mkdirAllInRoot(root, filepath.Dir(relativeName), 0o700); err != nil {
			return nil, nil, err
		}
		output, err := root.OpenFile(relativeName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, err
		}
		copyErr := copySized(output, reader, header.Size, maxDockerArchiveEntryBytes, "docker archive entry")
		closeErr := output.Close()
		if copyErr != nil {
			return nil, nil, copyErr
		}
		if closeErr != nil {
			return nil, nil, closeErr
		}
		files[name] = struct{}{}
	}
	succeeded = true
	return root, files, nil
}

func mkdirAllInRoot(root *os.Root, name string, mode os.FileMode) error {
	name = filepath.Clean(name)
	if name == "." {
		return nil
	}
	current := "."
	for _, component := range strings.Split(name, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		if err := root.Mkdir(current, mode); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := root.Stat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("archive path component %q is not a directory", current)
		}
	}
	return nil
}

func readLayer(archiveRoot *os.Root, layerName, dataRoot string, sequence int) ([]stagedEntry, []whiteout, int, error) {
	input, err := archiveRoot.Open(filepath.FromSlash(layerName))
	if err != nil {
		return nil, nil, sequence, err
	}
	defer input.Close()
	buffered := bufio.NewReader(input)
	var source io.Reader = buffered
	if signature, _ := buffered.Peek(2); len(signature) == 2 && signature[0] == 0x1f && signature[1] == 0x8b {
		compressed, err := gzip.NewReader(buffered)
		if err != nil {
			return nil, nil, sequence, err
		}
		defer compressed.Close()
		source = compressed
	}
	reader := tar.NewReader(source)
	entries := make([]stagedEntry, 0)
	whiteouts := make([]whiteout, 0)
	count := 0
	var expandedBytes int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, sequence, err
		}
		count++
		if count > maxArchiveEntries {
			return nil, nil, sequence, errors.New("image layer contains too many entries")
		}
		name, err := cleanArchivePath(header.Name)
		if err != nil {
			return nil, nil, sequence, err
		}
		base := path.Base(name)
		if strings.HasPrefix(base, ".wh.") {
			directory := path.Dir(name)
			if base == ".wh..wh..opq" {
				whiteouts = append(whiteouts, whiteout{target: directory, opaque: true})
			} else {
				target := path.Join(directory, strings.TrimPrefix(base, ".wh."))
				whiteouts = append(whiteouts, whiteout{target: target})
			}
			continue
		}
		copied := cloneHeader(header)
		copied.Name = name
		if copied.Typeflag == 0 {
			copied.Typeflag = tar.TypeReg
		}
		if copied.Typeflag == tar.TypeLink {
			linkName, err := cleanArchivePath(copied.Linkname)
			if err != nil {
				return nil, nil, sequence, fmt.Errorf("invalid hardlink target for %q: %w", name, err)
			}
			copied.Linkname = linkName
		}
		entry := stagedEntry{header: copied}
		if isRegularEntry(copied.Typeflag) {
			if copied.Size < 0 || copied.Size > maxLayerEntryBytes {
				return nil, nil, sequence, fmt.Errorf("layer entry %q exceeds the %d-byte limit", name, maxLayerEntryBytes)
			}
			if expandedBytes > maxExpandedLayerBytes-copied.Size {
				return nil, nil, sequence, fmt.Errorf("image layer expands beyond the %d-byte limit", maxExpandedLayerBytes)
			}
			expandedBytes += copied.Size
			dataPath := filepath.Join(dataRoot, fmt.Sprintf("%012d", sequence))
			sequence++
			// #nosec G304 -- dataPath is a generated numeric filename under the
			// mode-0700 temporary data directory.
			output, err := os.OpenFile(dataPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, nil, sequence, err
			}
			copyErr := copySized(output, reader, copied.Size, maxLayerEntryBytes, "layer entry")
			closeErr := output.Close()
			if copyErr != nil {
				return nil, nil, sequence, copyErr
			}
			if closeErr != nil {
				return nil, nil, sequence, closeErr
			}
			entry.dataPath = dataPath
		}
		entries = append(entries, entry)
	}
	return entries, whiteouts, sequence, nil
}

func writeLayer(destination string, entries map[string]stagedEntry) error {
	// #nosec G304 -- destination is the fixed layer.tar path in FoxOS's private
	// temporary work directory.
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writer := tar.NewWriter(output)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		left := entries[names[i]].header
		right := entries[names[j]].header
		leftGroup := entrySortGroup(left)
		rightGroup := entrySortGroup(right)
		if leftGroup != rightGroup {
			return leftGroup < rightGroup
		}
		if leftGroup == 0 {
			leftDepth := strings.Count(names[i], "/")
			rightDepth := strings.Count(names[j], "/")
			if leftDepth != rightDepth {
				return leftDepth < rightDepth
			}
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		entry := entries[name]
		header := cloneHeader(&entry.header)
		header.Format = tar.FormatPAX
		if err := writer.WriteHeader(&header); err != nil {
			_ = writer.Close()
			_ = output.Close()
			return fmt.Errorf("write flattened header %q: %w", name, err)
		}
		if entry.dataPath == "" {
			continue
		}
		data, err := os.Open(entry.dataPath)
		if err != nil {
			_ = writer.Close()
			_ = output.Close()
			return err
		}
		_, copyErr := io.Copy(writer, data)
		closeErr := data.Close()
		if copyErr != nil {
			_ = writer.Close()
			_ = output.Close()
			return copyErr
		}
		if closeErr != nil {
			_ = writer.Close()
			_ = output.Close()
			return closeErr
		}
	}
	if err := writer.Close(); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func writeDockerArchive(destination string, config, manifest []byte, layerPath string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".routeros-image-*.tar")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	writer := tar.NewWriter(temporary)
	if err := writeBytesEntry(writer, "config.json", config); err != nil {
		_ = writer.Close()
		_ = temporary.Close()
		return err
	}
	if err := writeBytesEntry(writer, "manifest.json", manifest); err != nil {
		_ = writer.Close()
		_ = temporary.Close()
		return err
	}
	// #nosec G304 -- layerPath is the fixed flattened layer generated in the
	// private work directory, not an archive-controlled path.
	layer, err := os.Open(layerPath)
	if err != nil {
		_ = writer.Close()
		_ = temporary.Close()
		return err
	}
	layerInfo, err := layer.Stat()
	if err != nil {
		_ = layer.Close()
		_ = writer.Close()
		_ = temporary.Close()
		return err
	}
	if err := writer.WriteHeader(&tar.Header{Name: "layer.tar", Mode: 0o644, Size: layerInfo.Size(), Typeflag: tar.TypeReg, Uid: 0, Gid: 0, Format: tar.FormatPAX}); err != nil {
		_ = layer.Close()
		_ = writer.Close()
		_ = temporary.Close()
		return err
	}
	if _, err := io.Copy(writer, layer); err != nil {
		_ = layer.Close()
		_ = writer.Close()
		_ = temporary.Close()
		return err
	}
	if err := layer.Close(); err != nil {
		_ = writer.Close()
		_ = temporary.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func writeBytesEntry(writer *tar.Writer, name string, body []byte) error {
	header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg, Uid: 0, Gid: 0, Format: tar.FormatPAX}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	_, err := writer.Write(body)
	return err
}

func cleanArchivePath(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := path.Clean(strings.TrimPrefix(name, "./"))
	if clean == "." || clean == ".." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return clean, nil
}

func cloneHeader(header *tar.Header) tar.Header {
	copyHeader := *header
	if header.PAXRecords != nil {
		copyHeader.PAXRecords = make(map[string]string, len(header.PAXRecords))
		for key, value := range header.PAXRecords {
			copyHeader.PAXRecords[key] = value
		}
	}
	return copyHeader
}

func isRegularEntry(typeflag byte) bool {
	return typeflag == tar.TypeReg || typeflag == 0
}

func copySized(destination io.Writer, source io.Reader, size, limit int64, kind string) error {
	if size < 0 || size > limit {
		return fmt.Errorf("%s exceeds the %d-byte limit", kind, limit)
	}
	written, err := io.CopyN(destination, source, size)
	if err != nil {
		return fmt.Errorf("copy %s: %w", kind, err)
	}
	if written != size {
		return fmt.Errorf("copy %s: wrote %d bytes, expected %d", kind, written, size)
	}
	return nil
}

func readRootFileAtMost(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("file %q exceeds the %d-byte metadata limit or is not regular", name, limit)
	}
	return io.ReadAll(io.LimitReader(file, limit+1))
}

func removeChildren(entries map[string]stagedEntry, directory string) {
	prefix := ""
	if directory != "." {
		prefix = strings.TrimSuffix(directory, "/") + "/"
	}
	for name := range entries {
		if prefix == "" || strings.HasPrefix(name, prefix) {
			delete(entries, name)
		}
	}
}

func removePath(entries map[string]stagedEntry, target string) {
	delete(entries, target)
	removeChildren(entries, target)
}

func entrySortGroup(header tar.Header) int {
	if header.Typeflag == tar.TypeDir {
		return 0
	}
	if header.Typeflag == tar.TypeLink {
		return 2
	}
	return 1
}

func fileSHA256(filePath string) (string, error) {
	// #nosec G304 -- filePath is the fixed flattened layer generated in FoxOS's
	// private temporary work directory.
	input, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
