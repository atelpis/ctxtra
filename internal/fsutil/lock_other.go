//go:build !unix

package fsutil

// Lock doesn't lock on systems without flock; ctxtra doesn't support them
// yet.
func Lock(path string) (unlock func(), err error) { return func() {}, nil }
