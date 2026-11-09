//go:build !darwin && !linux

package creds

// osKeychain is go-keyring's keychain on other systems, which ctxtra
// doesn't ship for.
type osKeychain = libKeychain
