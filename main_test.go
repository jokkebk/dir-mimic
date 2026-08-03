package main

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComputeSampleHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{name: "small file", data: []byte("small file contents")},
		{name: "large file", data: repeatedBytes(140_000)},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(path, tt.data, 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := computeSampleHash(path, int64(len(tt.data)))
			if err != nil {
				t.Fatal(err)
			}

			sampled := tt.data
			if len(tt.data) > 65_536 {
				sampled = append(append([]byte{}, tt.data[:65_536]...), tt.data[len(tt.data)-65_536:]...)
			}
			wantHash := sha1.Sum(sampled)
			want := hex.EncodeToString(wantHash[:])
			if got != want {
				t.Fatalf("computeSampleHash() = %q, want %q", got, want)
			}
		})
	}
}

func TestScanDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.txt"), []byte("root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "child.txt"), []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := scanDirectory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("scanDirectory() returned %d entries, want 2", len(entries))
	}

	byPath := make(map[string]FileEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	for path, size := range map[string]int64{"root.txt": 4, filepath.Join("nested", "child.txt"): 5} {
		entry, ok := byPath[path]
		if !ok {
			t.Errorf("scanDirectory() did not return %q", path)
			continue
		}
		if entry.Size != size {
			t.Errorf("entry %q size = %d, want %d", path, entry.Size, size)
		}
		if entry.MTime == 0 {
			t.Errorf("entry %q has no modification time", path)
		}
		if entry.Hash == "" {
			t.Errorf("entry %q has no sample hash", path)
		}
	}
}

func TestExecuteCopyPreservesContentModeAndModificationTime(t *testing.T) {
	root := t.TempDir()
	targetDir = root

	sourcePath := filepath.Join(root, "source.txt")
	contents := []byte("copy me")
	if err := os.WriteFile(sourcePath, contents, 0o640); err != nil {
		t.Fatal(err)
	}
	wantModTime := time.Date(2020, time.May, 6, 7, 8, 9, 0, time.UTC)
	if err := os.Chtimes(sourcePath, wantModTime, wantModTime); err != nil {
		t.Fatal(err)
	}

	if err := executeCopy("source.txt", filepath.Join("nested", "copy.txt")); err != nil {
		t.Fatal(err)
	}

	destinationPath := filepath.Join(root, "nested", "copy.txt")
	gotContents, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotContents) != string(contents) {
		t.Errorf("copied contents = %q, want %q", gotContents, contents)
	}

	info, err := os.Stat(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("copied permissions = %o, want 640", got)
	}
	if !info.ModTime().Equal(wantModTime) {
		t.Errorf("copied modification time = %v, want %v", info.ModTime(), wantModTime)
	}
}

func TestExecuteMoveAndDelete(t *testing.T) {
	root := t.TempDir()
	targetDir = root

	sourcePath := filepath.Join(root, "source.txt")
	if err := os.WriteFile(sourcePath, []byte("move me"), 0o600); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join("nested", "moved.txt")
	if err := executeMove("source.txt", destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("move left source in place; stat error = %v", err)
	}
	destinationPath := filepath.Join(root, destination)
	if contents, err := os.ReadFile(destinationPath); err != nil {
		t.Fatal(err)
	} else if string(contents) != "move me" {
		t.Errorf("moved contents = %q, want %q", contents, "move me")
	}

	if err := executeDelete(destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destinationPath); !os.IsNotExist(err) {
		t.Fatalf("delete left file in place; stat error = %v", err)
	}
}

func repeatedBytes(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}
