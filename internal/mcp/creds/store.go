// Package creds is the credential store of MCP servers: one encrypted
// record per server, with its tokens, client registration and secret
// values, and a state file without secrets that the hook can read. Every
// change of a server's record and state happens under that server's lock,
// so sessions of any ctxtra version can refresh at the same time.
//
// The file names, their format version, the lock paths and the meaning of
// the state file are a contract between ctxtra versions: within a format
// version, fields are only added, and every writer keeps the fields it
// doesn't know.
package creds

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/ctxtra/ctxtra/internal/fsutil"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
	"github.com/ctxtra/ctxtra/internal/paths"
)

// version is the format version of the credential records this ctxtra
// writes; it is in their file names, so it can be read without the key.
const version = 1

// stateVersion is the version of the state files this ctxtra writes.
const stateVersion = 1

// maxRecord bounds what a record or state file may be.
const maxRecord = 1 << 20

// Store is the credential store in Dir.
type Store struct {
	Dir string
	// KeyTimeout bounds the wait for the keychain; 0 waits as long as it
	// takes. The proxy sets it: it must never wait for a prompt.
	KeyTimeout time.Duration
	// HTTP sends refresh requests; nil uses http.DefaultClient.
	HTTP *http.Client
	// LockTimeout bounds the wait for a server's lock; 0 is 30 seconds.
	LockTimeout time.Duration
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu sync.Mutex
	id *age.X25519Identity
}

// Open is the store in ~/.ctxtra/mcp.
func Open() *Store { return &Store{Dir: filepath.Join(paths.StateDir(), "mcp")} }

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) lockTimeout() time.Duration {
	if s.LockTimeout > 0 {
		return s.LockTimeout
	}
	return 30 * time.Second
}

// mkdir creates the store's folder sub, readable by the user only.
func (s *Store) mkdir(sub string) error {
	if err := os.MkdirAll(filepath.Join(s.Dir, sub), 0o700); err != nil {
		return err
	}
	return os.Chmod(s.Dir, 0o700)
}

// fileID is the identity of a server as a file name: one to one, so no two
// identities share a file, also on file systems that ignore case. Every
// byte but a-z, 0-9, _ and - is written as %XX.
func fileID(id string) string {
	var b strings.Builder
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func (s *Store) recordPath(id string, v int) string {
	return filepath.Join(s.Dir, "credentials", fileID(id)+".v"+strconv.Itoa(v)+".age")
}

func (s *Store) statePath(fid string) string {
	return filepath.Join(s.Dir, "state", fid+".json")
}

func (s *Store) storeLockPath() string { return filepath.Join(s.Dir, "locks", "store.lock") }

func (s *Store) lockPath(fid string) string {
	return filepath.Join(s.Dir, "locks", fid+".lock")
}

// versions are the format versions of id's record files.
func (s *Store) versions(id string) []int {
	matches, _ := filepath.Glob(filepath.Join(s.Dir, "credentials", fileID(id)+".v*.age"))
	var out []int
	for _, m := range matches {
		v := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), fileID(id)+".v"), ".age")
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			out = append(out, n)
		}
	}
	return out
}

// NewerError is a record written by a newer ctxtra, in a format this one
// can't read. Nothing is changed then.
type NewerError struct{ Name string }

func (e *NewerError) Error() string {
	return "the credentials of " + e.Name + " were saved by a newer ctxtra; upgrade ctxtra"
}

func (s *Store) checkVersion(id, name string) error {
	for _, v := range s.versions(id) {
		if v > version {
			return &NewerError{name}
		}
	}
	return nil
}

// Record is a server's credentials.
type Record struct {
	// ID is the server's identity; URL is a remote server's canonical
	// URL, Command a local server's command line; Issuer is the
	// authorization server that issued the tokens. Tokens and secret
	// values are used only while these match the server's definition.
	ID      string   `json:"id"`
	URL     string   `json:"url"`
	Command []string `json:"command"`
	Issuer  string   `json:"issuer"`
	// Generation counts the sign-ins; a refresh keeps it.
	Generation int     `json:"generation"`
	Client     Client  `json:"client"`
	Token      Token   `json:"token"`
	Secrets    Secrets `json:"secrets"`

	// raw is the record as it was read, so that a rewrite keeps the
	// fields that a newer ctxtra added.
	raw []byte
}

// Client is the OAuth client registration.
type Client struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
	// AuthMethod is the token endpoint's client authentication:
	// client_secret_basic, client_secret_post or none.
	AuthMethod string `json:"auth_method"`
	TokenURL   string `json:"token_url"`
}

// Token is the OAuth grant.
type Token struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	Type    string `json:"type"`
	// Expiry is zero for a token without one; Obtained is when it was
	// issued.
	Expiry   time.Time `json:"expiry"`
	Obtained time.Time `json:"obtained"`
	Scopes   []string  `json:"scopes"`
}

// Secrets are the secret values: environment variables of a local
// server, headers of a remote one.
type Secrets struct {
	Env     map[string]string `json:"env"`
	Headers map[string]string `json:"headers"`
}

// nested are the objects of a record whose own unknown fields a rewrite
// keeps too. Maps of values (secrets.env) are replaced as a whole.
var nested = []string{"client", "token", "secrets"}

// Matches reports whether r was made for s as it is defined now: the same
// identity, and the same canonical URL or command.
func (r *Record) Matches(s upstream.Server) bool {
	if r.ID != s.ID {
		return false
	}
	if s.Local() {
		return slices.Equal(r.Command, s.Command)
	}
	return r.URL != "" && r.URL == CanonicalURL(s.URL)
}

// bind makes r a record for s: the same identity, URL or command. A record
// made for another definition loses its tokens and secret values.
func (r *Record) bind(s upstream.Server) {
	if r.ID != "" && !r.Matches(s) {
		*r = Record{Generation: r.Generation}
	}
	r.ID = s.ID
	if s.Local() {
		r.URL, r.Command = "", slices.Clone(s.Command)
	} else {
		r.URL, r.Command = CanonicalURL(s.URL), nil
	}
}

// encode is r as JSON, with the unknown fields of the record it was read
// from.
func (r *Record) encode() ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return mergeUnknown(r.raw, data, nested)
}

// mergeUnknown is cur with the fields of old that cur doesn't have, at the
// top and in the objects named by sub.
func mergeUnknown(old, cur []byte, sub []string) ([]byte, error) {
	if len(old) == 0 {
		return cur, nil
	}
	var o, c map[string]json.RawMessage
	if json.Unmarshal(old, &o) != nil {
		return cur, nil
	}
	if err := json.Unmarshal(cur, &c); err != nil {
		return nil, err
	}
	for k, v := range o {
		if _, ok := c[k]; !ok {
			c[k] = v
		}
	}
	for _, k := range sub {
		if merged, err := mergeUnknown(o[k], c[k], nil); err == nil && len(merged) > 0 && merged[0] == '{' {
			c[k] = merged
		}
	}
	return json.Marshal(c)
}

// load reads id's record: nil without one. The caller checked the
// version.
func (s *Store) load(id string) (*Record, error) {
	f, err := fsutil.OpenRegular(s.recordPath(id, version), os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	enc, err := io.ReadAll(io.LimitReader(f, maxRecord))
	if err != nil {
		return nil, err
	}
	data, err := s.decrypt(enc)
	if err != nil {
		return nil, err
	}
	r := &Record{raw: data}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, errUnreadable
	}
	return r, nil
}

// decrypt decrypts a record with the key, read again when the one this
// process kept doesn't fit: the key may have been replaced since.
func (s *Store) decrypt(enc []byte) ([]byte, error) {
	for range 2 {
		key, err := s.key()
		if errors.Is(err, ErrNoKey) || errors.Is(err, errKeyGone) {
			return nil, errUnreadable
		}
		if err != nil {
			return nil, err
		}
		dec, err := age.Decrypt(bytes.NewReader(enc), key)
		if err == nil {
			data, err := io.ReadAll(io.LimitReader(dec, maxRecord))
			if err != nil {
				return nil, errUnreadable
			}
			return data, nil
		}
		s.forgetKey()
	}
	return nil, errUnreadable
}

// errUnreadable is a record the key can't read.
var errUnreadable = errors.New("credentials can't be read")

// Load reads the record of s without the lock; nil when there is none.
func (s *Store) Load(srv upstream.Server) (*Record, error) {
	if err := s.checkVersion(srv.ID, srv.Name); err != nil {
		return nil, err
	}
	return s.load(srv.ID)
}

// save writes r durably, under id's lock, and deletes record files of
// older format versions, so no stale file keeps a rotated-away token.
func (s *Store) save(r *Record) error {
	data, err := r.encode()
	if err != nil {
		return err
	}
	key, err := s.key()
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, key.Recipient())
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := writeDurable(s.recordPath(r.ID, version), buf.Bytes()); err != nil {
		return err
	}
	r.raw = data
	for _, v := range s.versions(r.ID) {
		if v < version {
			_ = os.Remove(s.recordPath(r.ID, v))
		}
	}
	return nil
}

// lock takes the lock of srv's record and state. It holds the store's
// lock too, shared, so that the key can't change until it is released,
// and the key is read again: one this process read earlier may have been
// replaced.
func (s *Store) lock(srv upstream.Server) (func(), error) {
	if err := s.mkdir("locks"); err != nil {
		return nil, err
	}
	unlockStore, err := fsutil.LockShared(s.storeLockPath(), s.lockTimeout())
	if err != nil {
		return nil, s.lockError("the credential store", err)
	}
	unlock, err := fsutil.LockTimeout(s.lockPath(fileID(srv.ID)), s.lockTimeout())
	if err != nil {
		unlockStore()
		return nil, s.lockError("the credentials of "+srv.Name, err)
	}
	s.forgetKey()
	return func() {
		unlock()
		unlockStore()
	}, nil
}

func (s *Store) lockError(what string, err error) error {
	if errors.Is(err, fsutil.ErrLockTimeout) {
		return fmt.Errorf("timed out waiting for %s, which another ctxtra process holds; try again", what)
	}
	return err
}

// ErrDeleted is the error of Update for a record that doesn't exist and
// must not be created, such as one removed while a session refreshed it.
var ErrDeleted = errors.New("the credentials were deleted")

// Update changes the record of srv under its lock: it reads the record
// again, lets change edit it, and saves it. A missing record (or one the
// key can't read) is created only with create; otherwise Update returns
// ErrDeleted and writes nothing. A record made for another definition of
// srv starts empty.
func (s *Store) Update(srv upstream.Server, create bool, change func(*Record) error) (*Record, error) {
	unlock, err := s.lock(srv)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.updateLocked(srv, create, change)
}

func (s *Store) updateLocked(srv upstream.Server, create bool, change func(*Record) error) (*Record, error) {
	if err := s.checkVersion(srv.ID, srv.Name); err != nil {
		return nil, err
	}
	r, err := s.load(srv.ID)
	if errors.Is(err, errUnreadable) && create {
		r, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r == nil {
		if !create {
			return nil, ErrDeleted
		}
		r = &Record{}
	}
	r.bind(srv)
	if err := change(r); err != nil {
		return nil, err
	}
	if err := s.save(r); err != nil {
		return nil, fmt.Errorf("the credentials of %s could not be saved: %w", srv.Name, err)
	}
	return r, nil
}

// Delete removes srv's record and state file, under its lock.
func (s *Store) Delete(srv upstream.Server) error {
	unlock, err := s.lock(srv)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.checkVersion(srv.ID, srv.Name); err != nil {
		return err
	}
	var errs []error
	for _, v := range s.versions(srv.ID) {
		if err := os.Remove(s.recordPath(srv.ID, v)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := os.Remove(s.statePath(fileID(srv.ID))); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		errs = append(errs, syncDir(filepath.Join(s.Dir, "credentials")))
	}
	return errors.Join(errs...)
}

// readsAny reports whether key reads one of the stored records.
func (s *Store) readsAny(key *age.X25519Identity) bool {
	for _, path := range s.recordFiles() {
		if ok, _ := readable(path, key); ok {
			return true
		}
	}
	return false
}

// readable reports whether key decrypts the record at path. A record that
// can't be opened is an error, not an unreadable record.
func readable(path string, key *age.X25519Identity) (bool, error) {
	f, err := fsutil.OpenRegular(path, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = age.Decrypt(io.LimitReader(f, maxRecord), key)
	return err == nil, nil
}

// recordFiles are the records of this format version.
func (s *Store) recordFiles() []string {
	matches, _ := filepath.Glob(filepath.Join(s.Dir, "credentials", "*.v"+strconv.Itoa(version)+".age"))
	return matches
}

// moveUnreadable moves the records that key can't read (all of them for a
// nil key) to credentials/unreadable, where nothing reads them. Each
// server's state says it needs sign-in.
func (s *Store) moveUnreadable(key *age.X25519Identity) error {
	for _, path := range s.recordFiles() {
		if key != nil {
			ok, err := readable(path, key)
			if errors.Is(err, os.ErrNotExist) || ok {
				continue
			}
			if err != nil {
				return err
			}
		}
		if err := s.moveAside(path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) moveAside(path string) error {
	if err := s.mkdir(filepath.Join("credentials", "unreadable")); err != nil {
		return err
	}
	if err := s.mkdir("locks"); err != nil {
		return err
	}
	fid := strings.TrimSuffix(filepath.Base(path), ".v"+strconv.Itoa(version)+".age")
	unlock, err := fsutil.LockTimeout(s.lockPath(fid), s.lockTimeout())
	if err != nil {
		return s.lockError("the credentials of "+fid, err)
	}
	defer unlock()
	dst := filepath.Join(s.Dir, "credentials", "unreadable", filepath.Base(path)+"."+strconv.FormatInt(s.now().UnixNano(), 10))
	if err := os.Rename(path, dst); err != nil {
		return err
	}
	return s.writeState(fid, func(st *State) {
		st.SignedIn, st.NeedsLogin, st.Reason = false, true, errUnreadable.Error()+": sign in again"
	})
}

// syncFile and syncDir make writes durable; tests replace them.
var (
	syncFile = (*os.File).Sync
	syncDir  = func(dir string) error {
		f, err := os.Open(dir)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Sync()
	}
)

// writeDurable replaces path with data, mode 0600, so that a reader never
// sees a partial file and the new content is on disk before it returns:
// the file is synced before the rename, and its folder and the folder's
// parent after it. A failed sync fails the write.
func writeDurable(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-") // mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := syncFile(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	// The folder may be new: its own entry must reach the disk too.
	return syncDir(filepath.Dir(dir))
}
