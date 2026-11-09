package creds

import (
	"errors"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
	ss "github.com/zalando/go-keyring/secret_service"
)

// osKeychain is the Secret Service. With a timeout, a read never unlocks
// a locked collection, which would show a prompt: it is an error.
type osKeychain struct{}

func (osKeychain) get(timeout time.Duration) (string, error) {
	if timeout <= 0 {
		return keyring.Get(keyService, keyAccount)
	}
	type result struct {
		v   string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := getUnlocked()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(timeout):
		return "", errors.New("the Secret Service didn't answer in time")
	}
}

// getUnlocked reads the item only when it is unlocked.
func getUnlocked() (string, error) {
	svc, err := ss.NewSecretService()
	if err != nil {
		return "", err
	}
	items, err := svc.SearchItems(svc.GetLoginCollection(), map[string]string{"username": keyAccount, "service": keyService})
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", keyring.ErrNotFound
	}
	locked, err := svc.Object("org.freedesktop.secrets", items[0]).GetProperty("org.freedesktop.Secret.Item.Locked")
	if err != nil {
		return "", err
	}
	if l, ok := locked.Value().(bool); !ok || l {
		return "", errors.New("the keyring is locked")
	}
	session, err := svc.OpenSession()
	if err != nil {
		return "", err
	}
	defer svc.Close(session)
	secret, err := svc.GetSecret(items[0], dbus.ObjectPath(session.Path()))
	if err != nil {
		return "", err
	}
	return string(secret.Value), nil
}

func (osKeychain) set(v string) error { return keyring.Set(keyService, keyAccount, v) }
