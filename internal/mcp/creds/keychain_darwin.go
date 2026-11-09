package creds

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// osKeychain is the macOS keychain. Reads run security(1), like
// go-keyring, but are stopped at the timeout. macOS has no way to forbid
// the prompt of a locked keychain; the login keychain is unlocked while
// the user is logged in.
type osKeychain struct{}

func (osKeychain) get(timeout time.Duration) (string, error) {
	ctx, cancel := withTimeout(timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", keyService, "-wa", keyAccount).CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("no answer after %s", timeout)
	}
	if err != nil {
		if bytes.Contains(out, []byte("could not be found")) {
			return "", keyring.ErrNotFound
		}
		return "", errors.New(strings.TrimSpace(string(out)))
	}
	// go-keyring, which writes the item, encodes its value.
	v := strings.TrimSpace(string(out))
	if s, ok := strings.CutPrefix(v, "go-keyring-base64:"); ok {
		b, err := base64.StdEncoding.DecodeString(s)
		return string(b), err
	}
	if s, ok := strings.CutPrefix(v, "go-keyring-encoded:"); ok {
		b, err := hex.DecodeString(s)
		return string(b), err
	}
	return v, nil
}

func (osKeychain) set(v string) error { return keyring.Set(keyService, keyAccount, v) }

// withTimeout is a context that ends after timeout, or never for 0.
func withTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), timeout)
}
