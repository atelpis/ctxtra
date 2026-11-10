package status

import (
	"testing"
	"time"
)

// Each folder has its own record: a start in folder B leaves folder A's,
// for the same server, as it was.
func TestFoldersKeepTheirRecords(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := Record{Dir: "/home/u/dev/a", Agent: "codex", Started: time.Now().UTC(), DeadlineMs: 10000,
		Servers: []Server{{Name: "pg", State: Late, Reason: "too slow for the first tool list", Ms: 15200}}}
	b := Record{Dir: "/home/u/dev/b", Agent: "claude", Started: time.Now().UTC(), DeadlineMs: 1500,
		Servers: []Server{{Name: "pg", State: Ready, Ms: 300}}}
	if err := Write(a); err != nil {
		t.Fatal(err)
	}
	if err := Write(b); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Read(a.Dir)
	if err != nil || !ok || got.Servers[0].State != Late || got.Servers[0].Ms != 15200 {
		t.Errorf("folder a: %+v %v %v", got, ok, err)
	}
	if _, ok, _ := Read("/home/u/dev/c"); ok {
		t.Error("a folder without a session has a record")
	}
}

// An older session in the folder doesn't replace a newer one's record.
func TestNewerSessionWins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now().UTC()
	newer := Record{Dir: "/d", Agent: "claude", Started: now, Servers: []Server{{Name: "pg", State: Ready}}}
	older := Record{Dir: "/d", Agent: "codex", Started: now.Add(-time.Minute), Servers: []Server{{Name: "pg", State: Late, Ms: 15000}}}
	if err := Write(newer); err != nil {
		t.Fatal(err)
	}
	if err := Write(older); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := Read("/d"); got.Agent != "claude" {
		t.Errorf("record: %+v", got)
	}
}
