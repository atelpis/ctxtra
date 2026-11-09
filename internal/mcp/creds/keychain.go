package creds

import (
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

// The key's item in the OS keychain.
const (
	keyService = "ctxtra"
	keyAccount = "mcp-credentials"
)

// keychain is where the key's item is read and written.
type keychain interface {
	// get reads the item. With a timeout, it never asks the user (a
	// locked keychain is an error) and gives up after timeout.
	get(timeout time.Duration) (string, error)
	set(v string) error
}

// kc is the OS keychain, or go-keyring's in-memory one after MockKeychain.
var kc keychain = osKeychain{}

// MockKeychain makes the store use an in-memory keychain instead of the
// user's: a working one, or one that fails with err. For tests.
func MockKeychain(err error) {
	if err != nil {
		keyring.MockInitWithError(err)
	} else {
		keyring.MockInit()
	}
	kc = libKeychain{}
}

// getKey reads the key's item through go-keyring; tests replace it.
var getKey = func() (string, error) { return keyring.Get(keyService, keyAccount) }

// libKeychain is go-keyring's keychain. Its reads can't be interrupted:
// after timeout, the read is abandoned.
type libKeychain struct{}

func (libKeychain) get(timeout time.Duration) (string, error) {
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

func (libKeychain) set(v string) error { return keyring.Set(keyService, keyAccount, v) }
