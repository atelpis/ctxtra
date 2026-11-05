//go:build !unix

package update

// start starts no check: ctxtra ships for unix systems only.
func start([]string) error { return nil }
