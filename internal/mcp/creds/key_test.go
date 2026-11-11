package creds

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/zalando/go-keyring"

	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

var errNoKeychain = errors.New("no Secret Service")

var local = upstream.Server{ID: "pg", Name: "pg", Command: []string{"/usr/bin/pg-mcp"}, SecretEnv: []string{"PGPASSWORD"}}

func setSecret(t *testing.T, s *Store, srv upstream.Server, v string) {
	t.Helper()
	if err := s.SetSecrets(srv, map[string]string{"PGPASSWORD": v}, nil); err != nil {
		t.Fatal(err)
	}
}

func secret(t *testing.T, s *Store, srv upstream.Server) (string, error) {
	t.Helper()
	env, _, err := s.Secrets(srv)
	return env["PGPASSWORD"], err
}

func keyLocation(t *testing.T, dir string) string {
	t.Helper()
	info, err := (&Store{Dir: dir}).readKeyInfo()
	if err != nil {
		t.Fatal(err)
	}
	return info.Location
}

func TestTwoFirstSignInsAtOnce(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	var wg sync.WaitGroup
	for _, name := range []string{"one", "two"} {
		wg.Go(func() {
			s := &Store{Dir: dir}
			if _, err := s.EnsureKey(); err != nil {
				t.Error(err)
				return
			}
			srv := local
			srv.ID, srv.Name = name, name
			setSecret(t, s, srv, "v-"+name)
		})
	}
	wg.Wait()
	s := &Store{Dir: dir}
	for _, name := range []string{"one", "two"} {
		srv := local
		srv.ID, srv.Name = name, name
		if v, err := secret(t, s, srv); err != nil || v != "v-"+name {
			t.Errorf("%s: %q %v", name, v, err)
		}
	}
	if keyLocation(t, dir) != inKeychain {
		t.Errorf("key in %s", keyLocation(t, dir))
	}
}

func TestKeychainUnavailableThenAvailable(t *testing.T) {
	keyring.MockInitWithError(errNoKeychain)
	dir := t.TempDir()
	s := &Store{Dir: dir}
	fileKey, err := s.EnsureKey()
	if err != nil || !fileKey {
		t.Fatalf("EnsureKey: %v %v", fileKey, err)
	}
	setSecret(t, s, local, "pw")
	for _, p := range []string{"", "key", "key.json", "credentials", "credentials/pg.v1.age", "state", "locks"} {
		st, err := os.Stat(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if st.IsDir() {
			want = 0o700
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", p, st.Mode().Perm(), want)
		}
	}

	// A keychain that works later doesn't move the key.
	keyring.MockInit()
	s = &Store{Dir: dir}
	if fileKey, err := s.EnsureKey(); err != nil || !fileKey {
		t.Fatalf("EnsureKey with a keychain: %v %v", fileKey, err)
	}
	if v, err := secret(t, s, local); err != nil || v != "pw" {
		t.Errorf("secret: %q %v", v, err)
	}
	if _, err := keyring.Get(keyService, keyAccount); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("a key was put in the keychain: %v", err)
	}

	keyring.MockInitWithError(errNoKeychain)
	if v, err := secret(t, &Store{Dir: dir}, local); err != nil || v != "pw" {
		t.Errorf("secret without a keychain again: %q %v", v, err)
	}
}

func TestRecordedKeychainUnreachable(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	s := &Store{Dir: dir}
	if fileKey, err := s.EnsureKey(); err != nil || fileKey {
		t.Fatalf("EnsureKey: %v %v", fileKey, err)
	}
	setSecret(t, s, local, "pw")

	keyring.MockInitWithError(errNoKeychain)
	proxy := &Store{Dir: dir, KeyTimeout: 3 * time.Second}
	_, err := secret(t, proxy, local)
	var kerr *KeychainError
	if !errors.As(err, &kerr) || !strings.Contains(err.Error(), "keychain") {
		t.Errorf("secret: %v", err)
	}
	// Not even add or login fall back to a file.
	if _, err := (&Store{Dir: dir}).EnsureKey(); !errors.As(err, &kerr) {
		t.Errorf("EnsureKey: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "key")); !os.IsNotExist(err) {
		t.Errorf("a key file was made: %v", err)
	}
	if keyLocation(t, dir) != inKeychain {
		t.Error("key.json changed")
	}
}

func TestKeychainTimeout(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	s := &Store{Dir: dir}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	setSecret(t, s, local, "pw")
	old := getKey
	t.Cleanup(func() { getKey = old })
	getKey = func() (string, error) {
		time.Sleep(time.Second)
		return "", errors.New("late")
	}
	start := time.Now()
	_, err := secret(t, &Store{Dir: dir, KeyTimeout: 100 * time.Millisecond}, local)
	var kerr *KeychainError
	if !errors.As(err, &kerr) || time.Since(start) > 900*time.Millisecond {
		t.Errorf("after %v: %v", time.Since(start), err)
	}
}

func TestStopBeforeKeyJSON(t *testing.T) {
	for _, keychain := range []bool{true, false} {
		if keychain {
			keyring.MockInit()
		} else {
			keyring.MockInitWithError(errNoKeychain)
		}
		dir := t.TempDir()
		s := &Store{Dir: dir}
		if _, err := s.EnsureKey(); err != nil {
			t.Fatal(err)
		}
		setSecret(t, s, local, "pw")
		// As if the run had stopped right after creating the key.
		os.Remove(filepath.Join(dir, "key.json"))
		s = &Store{Dir: dir}
		if _, err := s.EnsureKey(); err != nil {
			t.Fatal(err)
		}
		if v, err := secret(t, s, local); err != nil || v != "pw" {
			t.Errorf("keychain %v: the key wasn't adopted: %q %v", keychain, v, err)
		}
		if want := map[bool]string{true: inKeychain, false: inFile}[keychain]; keyLocation(t, dir) != want {
			t.Errorf("keychain %v: key in %s", keychain, keyLocation(t, dir))
		}
	}
}

func TestAdoptTheKeyThatReadsRecords(t *testing.T) {
	keyring.MockInitWithError(errNoKeychain)
	dir := t.TempDir()
	s := &Store{Dir: dir}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	setSecret(t, s, local, "pw")
	os.Remove(filepath.Join(dir, "key.json"))
	// A stale key in the keychain must not replace the file key that
	// reads the records.
	keyring.MockInit()
	stale, _ := age.GenerateX25519Identity()
	keyring.Set(keyService, keyAccount, stale.String())
	s = &Store{Dir: dir}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	if v, err := secret(t, s, local); err != nil || v != "pw" {
		t.Errorf("secret: %q %v", v, err)
	}
	if keyLocation(t, dir) != inFile {
		t.Errorf("key in %s", keyLocation(t, dir))
	}
}

func TestDeletedKeychainItem(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	s := &Store{Dir: dir}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	setSecret(t, s, local, "pw")
	keyring.Delete(keyService, keyAccount)

	_, err := secret(t, &Store{Dir: dir}, local)
	var lerr *LoginError
	if !errors.As(err, &lerr) || !strings.Contains(err.Error(), "can't be read") {
		t.Errorf("secret: %v", err)
	}
	s = &Store{Dir: dir}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	moved, _ := filepath.Glob(filepath.Join(dir, "credentials", "unreadable", "pg.v1.age.*"))
	if len(moved) != 1 {
		t.Errorf("unreadable records: %v", moved)
	}
	if st, err := s.State(local); err != nil || !st.NeedsLogin {
		t.Errorf("state: %+v %v", st, err)
	}
	setSecret(t, s, local, "new")
	if v, err := secret(t, s, local); err != nil || v != "new" {
		t.Errorf("secret: %q %v", v, err)
	}
	if keyLocation(t, dir) != inKeychain {
		t.Errorf("key in %s", keyLocation(t, dir))
	}
}

func TestNoKeyYet(t *testing.T) {
	keyring.MockInit()
	s := &Store{Dir: t.TempDir()}
	_, err := secret(t, s, local)
	var lerr *LoginError
	if !errors.As(err, &lerr) {
		t.Errorf("secret: %v", err)
	}
	if s.KeyLocation() != "" {
		t.Errorf("KeyLocation: %q", s.KeyLocation())
	}
	if _, err := os.Stat(s.Dir + "/key.json"); !os.IsNotExist(err) {
		t.Error("a reader created a key")
	}
}

// A key file that exists but can't be read is never replaced.
func TestUnreadableKeyFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads any file")
	}
	keyring.MockInitWithError(errNoKeychain)
	dir := t.TempDir()
	s := &Store{Dir: dir}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	setSecret(t, s, local, "pw")
	os.Remove(filepath.Join(dir, "key.json"))
	key := filepath.Join(dir, "key")
	before, _ := os.ReadFile(key)
	os.Chmod(key, 0)
	defer os.Chmod(key, 0o600)
	if _, err := (&Store{Dir: dir}).EnsureKey(); err == nil {
		t.Error("EnsureKey made a new key")
	}
	os.Chmod(key, 0o600)
	if after, _ := os.ReadFile(key); !bytes.Equal(before, after) {
		t.Error("the key file changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials", "pg.v1.age")); err != nil {
		t.Errorf("the record was moved: %v", err)
	}
}

// Without key.json, a keychain that can't be reached may hold the key of
// the stored credentials: no new key is made.
func TestAdoptWithUnreachableKeychain(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	s := &Store{Dir: dir}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	setSecret(t, s, local, "pw")
	os.Remove(filepath.Join(dir, "key.json"))
	keyring.MockInitWithError(errNoKeychain)
	var kerr *KeychainError
	if _, err := (&Store{Dir: dir}).EnsureKey(); !errors.As(err, &kerr) {
		t.Errorf("EnsureKey: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "key")); !os.IsNotExist(err) {
		t.Errorf("a key file was made: %v", err)
	}
}

// A process that read the key before it was replaced saves with the new
// one.
func TestKeyReplacedWhileRunning(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	a := &Store{Dir: dir}
	if _, err := a.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	setSecret(t, a, local, "old")
	keyring.Delete(keyService, keyAccount)
	if _, err := (&Store{Dir: dir}).EnsureKey(); err != nil {
		t.Fatal(err)
	}
	setSecret(t, a, local, "new")
	if v, err := secret(t, &Store{Dir: dir}, local); err != nil || v != "new" {
		t.Errorf("secret: %q %v", v, err)
	}
}

// Key recovery leaves a newer ctxtra's credentials alone.
func TestRecoveryWithNewerRecords(t *testing.T) {
	keyring.MockInitWithError(errNoKeychain)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "credentials"), 0o700)
	os.WriteFile(filepath.Join(dir, "credentials", "pg.v2.age"), []byte("v2"), 0o600)
	if _, err := (&Store{Dir: dir}).EnsureKey(); err == nil || !strings.Contains(err.Error(), "upgrade ctxtra") {
		t.Errorf("EnsureKey: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "key")); !os.IsNotExist(err) {
		t.Errorf("a key was made: %v", err)
	}
}

// A key in both the keychain and a file, without key.json: the keychain
// keeps it, and the file goes.
func TestAdoptDuplicateKey(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	s := &Store{Dir: dir}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	setSecret(t, s, local, "pw")
	v, _ := keyring.Get(keyService, keyAccount)
	os.WriteFile(filepath.Join(dir, "key"), []byte(v+"\n"), 0o600)
	os.Remove(filepath.Join(dir, "key.json"))
	s = &Store{Dir: dir}
	if fileKey, err := s.EnsureKey(); err != nil || fileKey {
		t.Fatalf("EnsureKey: %v %v", fileKey, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "key")); !os.IsNotExist(err) {
		t.Errorf("the key file stayed: %v", err)
	}
	if v, err := secret(t, s, local); err != nil || v != "pw" {
		t.Errorf("secret: %q %v", v, err)
	}
}

func TestEnsureKeyRemovesLeftoverTempFiles(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	os.MkdirAll(dir, 0o700)
	tmp := filepath.Join(dir, ".tmp-123")
	os.WriteFile(tmp, []byte("AGE-SECRET-KEY-1..."), 0o600)
	if _, err := (&Store{Dir: dir}).EnsureKey(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("the temporary file stayed: %v", err)
	}
}

// failingKeychain fails a test that touches it.
type failingKeychain struct{ t *testing.T }

func (k failingKeychain) get(time.Duration) (string, error) {
	k.t.Error("the keychain was read")
	return "", errNoKeychain
}

func (k failingKeychain) set(string) error {
	k.t.Error("the keychain was written")
	return errNoKeychain
}

// What the hook reads, a server's state, needs no key and no keychain.
func TestStateNeedsNoKey(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	s := &Store{Dir: dir}
	if _, err := s.EnsureKey(); err != nil {
		t.Fatal(err)
	}
	setSecret(t, s, local, "pw")
	old := kc
	t.Cleanup(func() { kc = old })
	kc = failingKeychain{t}
	if st, err := (&Store{Dir: dir}).State(local); err != nil || !st.SignedIn {
		t.Errorf("state: %+v %v", st, err)
	}
}
