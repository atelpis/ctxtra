//go:build unix

package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadBounded(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small")
	write(t, small, "12345")
	if b, err := ReadBounded(small, 5); err != nil || string(b) != "12345" {
		t.Errorf("at the limit: %q %v", b, err)
	}
	if _, err := ReadBounded(small, 4); err == nil {
		t.Error("over the limit: no error")
	}
	if _, err := ReadBounded(dir, 10); err == nil {
		t.Error("directory: no error")
	}
	// A FIFO is refused without blocking, for reading and for writing.
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip(err)
	}
	if _, err := ReadBounded(fifo, 10); err == nil {
		t.Error("FIFO: no error")
	}
	if f, err := OpenRegular(fifo, os.O_WRONLY|os.O_APPEND, 0); err == nil {
		f.Close()
		t.Error("FIFO opened for writing")
	}
}

func TestReaderTotalLimit(t *testing.T) {
	dir := t.TempDir()
	for name, n := range map[string]int{"a": 6, "b": 6, "c": 3, "big": 20} {
		write(t, filepath.Join(dir, name), strings.Repeat("x", n))
	}
	r := NewReader(10, 15)
	read := func(name string) error {
		_, err := r.Read(filepath.Join(dir, name))
		return err
	}
	// Over the per-file limit: refused, but the 11 bytes read count.
	if err := read("big"); err == nil || errors.Is(err, ErrTotalLimit) {
		t.Errorf("big: %v", err)
	}
	// 4 bytes left: a 6-byte file is over the total.
	if err := read("a"); !errors.Is(err, ErrTotalLimit) {
		t.Errorf("a: %v", err)
	}
	if err := read("c"); !errors.Is(err, ErrTotalLimit) {
		t.Errorf("c after the total is used up: %v", err)
	}
	// A path is read once: the same result again, without counting.
	r = NewReader(10, 12)
	for range 3 {
		if err := read("a"); err != nil {
			t.Fatalf("a: %v", err)
		}
	}
	if err := read("b"); err != nil {
		t.Errorf("b: %v", err)
	}
}
