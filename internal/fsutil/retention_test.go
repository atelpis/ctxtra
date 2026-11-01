package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestWriteAtomicRetention(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("0"), 0o640); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 6; n++ {
		if _, changed, err := WriteAtomic(path, []byte(fmt.Sprint(n)), backups, false); err != nil || !changed {
			t.Fatalf("write %d: changed=%v, err=%v", n, changed, err)
		}
		var want []string
		for old := max(0, n-3); old < n; old++ {
			want = append(want, fmt.Sprint(old))
		}
		if got := backupContents(t, backups); !reflect.DeepEqual(got, want) {
			t.Fatalf("write %d: backups %v, want %v", n, got, want)
		}
	}
	for _, dryRun := range []bool{true, false} {
		data := "6"
		if dryRun {
			data = "dry run"
		}
		if _, _, err := WriteAtomic(path, []byte(data), backups, dryRun); err != nil {
			t.Fatal(err)
		}
		if got := backupContents(t, backups); !reflect.DeepEqual(got, []string{"3", "4", "5"}) {
			t.Fatalf("unchanged/dry run: backups %v", got)
		}
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("config permissions: %v, %v", st, err)
	}
}

func TestPruneBackupsIsolationAndOrder(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "a", ".agent", "settings.json")
	other := filepath.Join(dir, "b", ".agent", "settings.json")
	for _, p := range []string{path, other} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for n := range 5 {
		if err := Backup(other, []byte(fmt.Sprint(n)), backups); err != nil {
			t.Fatal(err)
		}
	}
	prefix, err := backupPrefix(path)
	if err != nil {
		t.Fatal(err)
	}
	// All timestamps fall within one second; two are exactly equal.
	names := []string{
		"20261101-010101.000000001.9.bak",
		"20261101-010101.000000002.9.bak",
		"20261101-010101.000000003.1.bak",
		"20261101-010101.000000003.2.bak",
		"20261101-010101.000000004.1.bak",
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(backups, prefix+name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	untouched := []string{
		"agent-settings.json.20261101-010101.123.bak",
		"unrelated.txt",
		prefix + "not-a-timestamp.1.bak",
		prefix + "20261101-010101.000000000..bak",
	}
	for _, name := range untouched {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("untouched"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	importDir := filepath.Join(backups, "import-20261101-010101-123", "claude")
	if err := os.MkdirAll(importDir, 0o700); err != nil {
		t.Fatal(err)
	}
	importFile := filepath.Join(importDir, "settings.json")
	if err := os.WriteFile(importFile, []byte("import"), 0o600); err != nil {
		t.Fatal(err)
	}
	matchingDir := filepath.Join(backups, prefix+"20261101-010101.000000000.1.bak")
	if err := os.Mkdir(matchingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(backups, prefix+"20261101-010101.000000000.2.bak")
	if err := os.Symlink(importFile, link); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := PruneBackups(path, backups); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)-2 {
		t.Fatalf("pruned %d files, want 2", len(before)-len(after))
	}
	for n, name := range names {
		_, err := os.Stat(filepath.Join(backups, prefix+name))
		if n < 2 && !os.IsNotExist(err) || n >= 2 && err != nil {
			t.Errorf("backup %s: %v", name, err)
		}
	}
	for _, entry := range before {
		if entry.Name() == prefix+names[0] || entry.Name() == prefix+names[1] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(backups, entry.Name())); err != nil {
			t.Errorf("unrelated entry removed: %s: %v", entry.Name(), err)
		}
	}
	if got, err := os.ReadFile(importFile); err != nil || string(got) != "import" {
		t.Fatalf("import backup changed: %q, %v", got, err)
	}
}

func TestWriteAtomicFailedReplacementKeepsBackups(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "settings.json")
	backups := filepath.Join(dir, "backups")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	for n := range 3 {
		if err := Backup(path, []byte(fmt.Sprintf("recovery%d", n)), backups); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(configDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(configDir, 0o700) })
	if f, err := os.CreateTemp(configDir, "probe"); err == nil {
		f.Close()
		os.Remove(f.Name())
		t.Skip("directory permissions do not prevent writes for this user")
	}
	for range 3 {
		if _, _, err := WriteAtomic(path, []byte("replacement"), backups, false); err == nil {
			t.Fatal("replacement unexpectedly succeeded")
		}
	}
	if got := backupContents(t, backups); !reflect.DeepEqual(got, []string{"original", "recovery0", "recovery1", "recovery2"}) {
		t.Fatalf("failed write pruned backups: %v", got)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "original" {
		t.Fatalf("failed write changed config: %q, %v", got, err)
	}
	if err := os.Chmod(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteAtomic(path, []byte("replacement"), backups, false); err != nil {
		t.Fatal(err)
	}
	if got := backupContents(t, backups); !reflect.DeepEqual(got, []string{"original", "recovery1", "recovery2"}) {
		t.Fatalf("retries displaced history: %v", got)
	}
}

func TestWriteAtomicNoChangeKeepsExcessBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	backups := filepath.Join(dir, "backups")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	for n := range 4 {
		if err := Backup(path, []byte(fmt.Sprintf("recovery%d", n)), backups); err != nil {
			t.Fatal(err)
		}
	}
	for _, dryRun := range []bool{false, true} {
		data := "original"
		if dryRun {
			data = "replacement"
		}
		if _, changed, err := WriteAtomic(path, []byte(data), backups, dryRun); err != nil || changed != dryRun {
			t.Fatalf("dryRun=%v: changed=%v, err=%v", dryRun, changed, err)
		}
		if got := backupContents(t, backups); len(got) != 4 {
			t.Fatalf("no write pruned backups: %v", got)
		}
	}
}

func TestWriteAtomicSymlinkRetention(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	link := filepath.Join(dir, "link.json")
	backups := filepath.Join(dir, "backups")
	if err := os.WriteFile(target, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 6; n++ {
		path := target
		if n%2 == 0 {
			path = link
		}
		if _, _, err := WriteAtomic(path, []byte(fmt.Sprint(n)), backups, false); err != nil {
			t.Fatal(err)
		}
	}
	if got := backupContents(t, backups); !reflect.DeepEqual(got, []string{"3", "4", "5"}) {
		t.Fatalf("symlink and target do not share retention: %v", got)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v, %v", st, err)
	}
}

func TestWriteAtomicFailedBackupLeavesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	backups := filepath.Join(dir, "not-a-directory")
	for _, p := range []string{path, backups} {
		if err := os.WriteFile(p, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := WriteAtomic(path, []byte("replacement"), backups, false); err == nil {
		t.Fatal("backup unexpectedly succeeded")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "original" {
		t.Fatalf("failed backup changed config: %q, %v", got, err)
	}
}

func backupContents(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var contents []string
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("backup permissions: %v, %v", info, err)
		}
		contents = append(contents, string(data))
	}
	slices.Sort(contents)
	return contents
}
