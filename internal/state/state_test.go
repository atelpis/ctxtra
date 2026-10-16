package state

import (
	"os"
	"testing"
	"time"

	"github.com/atelpis/ctxtra/internal/testenv"
)

func TestState(t *testing.T) {
	testenv.Home(t)
	now := time.Now().Round(time.Second)
	if err := Update("id", "u", "main", func(e *Source) { e.LastCheck = now; e.Dismissed = "d" }); err != nil {
		t.Fatal(err)
	}
	Update("id", "u", "dev", func(e *Source) { e.Dismissed = "x" })
	s := Load()
	if e := s.Get("id", "u", "main"); !e.LastCheck.Equal(now) || e.Dismissed != "d" {
		t.Errorf("main: %+v", e)
	}
	if e := s.Get("id", "u", "dev"); !e.LastCheck.IsZero() || e.Dismissed != "x" {
		t.Errorf("dev: %+v", e)
	}
	// An entry for another URL with the same ID isn't used.
	if e := s.Get("id", "other", "main"); !e.LastCheck.IsZero() || e.Dismissed != "" {
		t.Errorf("other URL: %+v", e)
	}
	// A broken file is an empty state, also when part of it decodes.
	for _, b := range []string{"{", `{"sources": {"id@main": {"url": "u", "branch": "main", "dismissed": "d"}, "x": 5}}`} {
		os.WriteFile(Path(), []byte(b), 0o600)
		if s := Load(); len(s.Sources) != 0 {
			t.Errorf("%s: %+v", b, s)
		}
	}
}
