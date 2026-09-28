package launcher

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupDigestAndManifestTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.sql")
	os.WriteFile(path, []byte("fixture database"), 0600)
	if err := backupMetadata(path, time.Now().UTC(), "8.4.0"); err != nil {
		t.Fatal(err)
	}
	f, meta, err := openBackup(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if len(meta.SHA256) != 64 || meta.Bytes != 16 {
		t.Fatal(meta)
	}
	info, _ := os.Stat(path + ".meta.json")
	if info.Mode().Perm() != 0600 {
		t.Fatal("manifest permission")
	}
	os.WriteFile(path, []byte("tampered"), 0600)
	if f, _, err := openBackup(path); err == nil {
		f.Close()
		t.Fatal("accepted corruption")
	}
	calls := 0
	m := manager{executeInput: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error { calls++; return nil }}
	if _, err = m.verifyBackup(context.Background(), path, false); err == nil || calls != 0 {
		t.Fatal("created schema before checksum validation")
	}
}
func TestMySQLCompatibilityAndArguments(t *testing.T) {
	for _, v := range []struct {
		old, current string
		ok           bool
	}{{"8.0.36", "8.4.8", true}, {"8.4.8", "8.4.9", true}, {"8.4.8", "8.0.36", false}, {"9.0.0", "8.4.8", false}, {"", "8.4.8", false}} {
		if mysqlCompatible(v.old, v.current) != v.ok {
			t.Fatal(v)
		}
	}
	for _, args := range [][]string{{"restore"}, {"verify-backup"}, {"retention", "--days", "0"}, {"retention", "--days", "9999"}} {
		if Run(context.Background(), args, io.Discard, io.Discard) == nil {
			t.Fatal("accepted invalid", strings.Join(args, " "))
		}
	}
}
