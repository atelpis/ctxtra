package creds

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/zalando/go-keyring"

	"github.com/ctxtra/ctxtra/internal/fsutil"
)

// The key's item in the OS keychain.
const (
	keyService = "ctxtra"
	keyAccount = "mcp-credentials"
)

// Where key.json says the key is.
const (
	inKeychain = "keychain"
	inFile     = "file"
)

// keyInfo is key.json: where the key is. It holds no secret.
type keyInfo struct {
	Version  int    `json:"version"`
	Location string `json:"location"`
}

// ErrNoKey is the error of a store that has no key yet: nothing was
// signed in or set on this machine.
var ErrNoKey = errors.New("no credentials are stored")

// errKeyGone is a key that key.json names but that isn't there: deleted
// from the keychain, or a deleted key file.
var errKeyGone = errors.New("the key of ctxtra's credentials is gone")

// KeychainError is a keychain that holds the key but can't be reached,
// for example over SSH or without a session bus. ctxtra never falls back
// to a key file then.
type KeychainError struct{ Err error }

func (e *KeychainError) Error() string {
	return "can't reach the OS keychain that holds the key of ctxtra's credentials (" + e.Err.Error() +
		"); unlock it, or run ctxtra in a desktop session"
}

func (e *KeychainError) Unwrap() error { return e.Err }

// getKey reads the key's keychain item; tests replace it.
var getKey = func() (string, error) { return keyring.Get(keyService, keyAccount) }

// keychainGet reads the key from the keychain, waiting at most timeout
// (0: no limit). A keychain that would ask the user is an error after
// timeout; the request it started is abandoned.
func keychainGet(timeout time.Duration) (string, error) {
	if timeout <= 0 {
		return getKey()
	}
	type result struct {
		v   string
		err error
	}
	ch := make(chan result, 1)
	get := getKey
	go func() {
		v, err := get()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(timeout):
		return "", fmt.Errorf("no answer after %s", timeout)
	}
}

func (s *Store) keyInfoPath() string { return filepath.Join(s.Dir, "key.json") }
func (s *Store) keyPath() string     { return filepath.Join(s.Dir, "key") }

func (s *Store) readKeyInfo() (*keyInfo, error) {
	data, err := os.ReadFile(s.keyInfoPath())
	if err != nil {
		return nil, err
	}
	var info keyInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("%s: %w", s.keyInfoPath(), err)
	}
	if info.Location != inKeychain && info.Location != inFile {
		return nil, fmt.Errorf("%s: unknown key location %q; upgrade ctxtra", s.keyInfoPath(), info.Location)
	}
	return &info, nil
}

// loadKey reads the key from where info says it is.
func (s *Store) loadKey(info *keyInfo) (*age.X25519Identity, error) {
	var v string
	switch info.Location {
	case inKeychain:
		got, err := keychainGet(s.KeyTimeout)
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, errKeyGone
		}
		if err != nil {
			return nil, &KeychainError{err}
		}
		v = got
	default:
		data, err := fsutil.ReadBounded(s.keyPath(), 1024)
		if errors.Is(err, os.ErrNotExist) {
			return nil, errKeyGone
		}
		if err != nil {
			return nil, err
		}
		v = string(data)
	}
	id, err := age.ParseX25519Identity(strings.TrimSpace(v))
	if err != nil {
		return nil, fmt.Errorf("the key of ctxtra's credentials is damaged: %w", err)
	}
	return id, nil
}

// key is the store's key. It never creates one.
func (s *Store) key() (*age.X25519Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.id != nil {
		return s.id, nil
	}
	info, err := s.readKeyInfo()
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoKey
	}
	if err != nil {
		return nil, err
	}
	id, err := s.loadKey(info)
	if err != nil {
		return nil, err
	}
	s.id = id
	return id, nil
}

// EnsureKey makes sure the store has a key, and creates one when it has
// none. Only the commands that sign in or set secret values call it. It
// reports whether the key is a file, which any process of the user can
// read: the caller warns about it.
//
// Under the store's lock: a key that key.json names is used, and a
// keychain that holds it but can't be reached is an error. Without
// key.json, a key left by an earlier run that stopped before writing it is
// adopted. A new key is created, in the keychain or else in a file, only
// when no key exists; credentials that no key can read are moved aside
// first, since nothing could ever read them.
func (s *Store) EnsureKey() (fileKey bool, err error) {
	if err := s.mkdir(""); err != nil {
		return false, err
	}
	unlock, err := fsutil.LockTimeout(filepath.Join(s.Dir, "locks", "store.lock"), s.lockTimeout())
	if err != nil {
		return false, s.lockError("the credential store", err)
	}
	defer unlock()
	s.mu.Lock()
	s.id = nil
	s.mu.Unlock()

	info, err := s.readKeyInfo()
	switch {
	case err == nil:
		id, err := s.loadKey(info)
		if err == nil {
			s.setKey(id)
			return info.Location == inFile, nil
		}
		if !errors.Is(err, errKeyGone) {
			return false, err
		}
		// The key is gone, and with it every stored credential: move them
		// aside, then make a new key where the old one was.
		if err := s.moveUnreadable(nil); err != nil {
			return false, err
		}
		loc, id, err := s.createKey(info.Location == inKeychain)
		if err != nil {
			return false, err
		}
		if err := s.writeKeyInfo(loc); err != nil {
			return false, err
		}
		s.setKey(id)
		return loc == inFile, nil
	case !errors.Is(err, os.ErrNotExist):
		return false, err
	}

	loc, id := s.adoptKey()
	if id == nil {
		if err := s.moveUnreadable(nil); err != nil {
			return false, err
		}
		if loc, id, err = s.createKey(true); err != nil {
			return false, err
		}
	}
	if err := s.writeKeyInfo(loc); err != nil {
		return false, err
	}
	s.setKey(id)
	return loc == inFile, s.moveUnreadable(id)
}

func (s *Store) setKey(id *age.X25519Identity) {
	s.mu.Lock()
	s.id = id
	s.mu.Unlock()
}

// adoptKey finds a key that a run which stopped before writing key.json
// left: the one that reads stored credentials, else the keychain's, else
// the file. nil when there is none.
func (s *Store) adoptKey() (string, *age.X25519Identity) {
	type found struct {
		loc string
		id  *age.X25519Identity
	}
	var keys []found
	if v, err := keychainGet(s.KeyTimeout); err == nil {
		if id, err := age.ParseX25519Identity(strings.TrimSpace(v)); err == nil {
			keys = append(keys, found{inKeychain, id})
		}
	}
	if data, err := fsutil.ReadBounded(s.keyPath(), 1024); err == nil {
		if id, err := age.ParseX25519Identity(strings.TrimSpace(string(data))); err == nil {
			keys = append(keys, found{inFile, id})
		}
	}
	if len(keys) == 0 {
		return "", nil
	}
	for _, k := range keys {
		if s.readsAny(k.id) {
			return k.loc, k.id
		}
	}
	return keys[0].loc, keys[0].id
}

// createKey makes a new key: in the keychain when keychain is set and it
// works, else in a file. The key is in place before key.json names it.
func (s *Store) createKey(keychain bool) (string, *age.X25519Identity, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", nil, err
	}
	if keychain {
		if err := keyring.Set(keyService, keyAccount, id.String()); err == nil {
			// Read it back: a keychain that accepts a value but can't
			// return it is no place for the key.
			if v, err := keychainGet(s.KeyTimeout); err == nil && strings.TrimSpace(v) == id.String() {
				return inKeychain, id, nil
			}
		}
	}
	if err := writeDurable(s.keyPath(), []byte(id.String()+"\n")); err != nil {
		return "", nil, err
	}
	return inFile, id, nil
}

func (s *Store) writeKeyInfo(loc string) error {
	data, _ := json.Marshal(keyInfo{Version: 1, Location: loc})
	return writeDurable(s.keyInfoPath(), append(data, '\n'))
}

// KeyLocation is where the key is, for doctor and mcp list: the keychain,
// the key file's path, or "" without a key.
func (s *Store) KeyLocation() string {
	info, err := s.readKeyInfo()
	if err != nil {
		return ""
	}
	if info.Location == inKeychain {
		return "the OS keychain"
	}
	return s.keyPath()
}
