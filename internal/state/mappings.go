package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Mapping says that plain paths under a clone's folder are served from the
// git source the clone was imported as. It names the source by its name and
// URL: a mapping whose source is gone, renamed or changed stays, and its
// paths are then skipped, not read from disk.
type Mapping struct {
	Path   string `json:"path"` // the clone's top when it was imported, without symlinks
	Source string `json:"source"`
	URL    string `json:"url"`
}

// Mappings are the mappings of this machine.
type Mappings []Mapping

// MappingsPath is ~/.ctxtra/mappings.json.
func MappingsPath() string { return filepath.Join(paths.StateDir(), "mappings.json") }

// LoadMappings reads the mappings. A missing file is none; a broken one is
// an error: without it, ctxtra can't tell which paths were mapped.
func LoadMappings() (Mappings, error) {
	b, err := fsutil.ReadBounded(MappingsPath(), maxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var ms Mappings
	if err == nil {
		err = json.Unmarshal(b, &ms)
	}
	if err != nil {
		return nil, fmt.Errorf("%s can't be read (%v); fix or delete it", paths.Tilde(MappingsPath()), err)
	}
	return ms, nil
}

// Lookup is the mapping p is under, and p relative to the clone, compared by
// path components.
func (ms Mappings) Lookup(p string) (Mapping, string, bool) {
	for _, m := range ms {
		if rel, ok := paths.Rel(m.Path, p); ok {
			return m, rel, true
		}
	}
	return Mapping{}, "", false
}

// Map maps the folder dir to the source called name with url, and reports
// whether it added the mapping (false: it was there). A folder inside or
// around another mapped folder is an error.
func Map(dir, name, url string) (bool, error) {
	added := false
	err := updateMappings(func(ms Mappings) (Mappings, error) {
		for _, m := range ms {
			_, inside := paths.Rel(m.Path, dir)
			_, around := paths.Rel(dir, m.Path)
			switch {
			case m == Mapping{dir, name, url}:
				return ms, nil
			case inside || around:
				return nil, fmt.Errorf("%s is already mapped to the source %s (%s)", paths.Tilde(m.Path), m.Source, m.URL)
			}
		}
		added = true
		return append(ms, Mapping{dir, name, url}), nil
	})
	return added && err == nil, err
}

// Unmap removes the mapping of the folder dir.
func Unmap(dir string) error {
	return updateMappings(func(ms Mappings) (Mappings, error) {
		return slices.DeleteFunc(ms, func(m Mapping) bool { return m.Path == dir }), nil
	})
}

// ConfigsPath is ~/.ctxtra/configs.json: the config files that add and
// sync used. Cleaning the store keeps the accepted commits of each.
func ConfigsPath() string { return filepath.Join(paths.StateDir(), "configs.json") }

// LoadConfigs reads the config files. A missing file is none; a broken one
// is an error, and the store isn't cleaned.
func LoadConfigs() ([]string, error) {
	b, err := fsutil.ReadBounded(ConfigsPath(), maxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var cs []string
	if err == nil {
		err = json.Unmarshal(b, &cs)
	}
	if err != nil {
		return nil, fmt.Errorf("%s can't be read (%v); fix or delete it", paths.Tilde(ConfigsPath()), err)
	}
	return cs, nil
}

// RecordConfig adds the config file path to configs.json.
func RecordConfig(path string) error {
	unlock, err := fsutil.Lock(filepath.Join(paths.StateDir(), "configs.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	cs, err := LoadConfigs()
	if err != nil || slices.Contains(cs, path) {
		return err
	}
	return writeJSON(ConfigsPath(), append(cs, path))
}

func updateMappings(change func(Mappings) (Mappings, error)) error {
	unlock, err := fsutil.Lock(filepath.Join(paths.StateDir(), "mappings.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	ms, err := LoadMappings()
	if err != nil {
		return err
	}
	if ms, err = change(ms); err != nil {
		return err
	}
	return writeJSON(MappingsPath(), ms)
}
