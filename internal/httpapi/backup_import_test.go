package httpapi

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeTestBackupArchive(t *testing.T, name string, typeflag byte) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "backup.tar.gz")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gz)
	header := &tar.Header{Name: name, Mode: 0o600, Size: 4, Typeflag: typeflag}
	if typeflag == tar.TypeSymlink {
		header.Size = 0
		header.Linkname = "target"
	}
	if err = tarWriter.WriteHeader(header); err == nil && header.Size > 0 {
		_, err = tarWriter.Write([]byte("data"))
	}
	if closeErr := tarWriter.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gz.Close(); err == nil {
		err = closeErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestValidateBackupArchive(t *testing.T) {
	if err := validateBackupArchive(writeTestBackupArchive(t, "data/config.json", tar.TypeReg)); err != nil {
		t.Fatalf("valid archive rejected: %v", err)
	}
	if err := validateBackupArchive(writeTestBackupArchive(t, "../escape", tar.TypeReg)); err == nil {
		t.Fatal("path traversal archive was accepted")
	}
	if err := validateBackupArchive(writeTestBackupArchive(t, "link", tar.TypeSymlink)); err == nil {
		t.Fatal("symlink archive was accepted")
	}
}
