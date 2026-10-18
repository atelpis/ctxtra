// Package ctxtra holds the files the binary serves as they are: the docs
// (ctxtra help TOPIC) and the ctxtra skill (ctxtra skill). They live at the
// top of the repository, where people and skill installers find them, and
// the binary embeds them, so it always serves the version it was built from.
package ctxtra

import (
	"embed"
	"io/fs"
)

//go:embed README.md docs/spec.md docs/scenarios.md
var docs embed.FS

//go:embed skills/ctxtra
var skill embed.FS

// Doc returns the content of an embedded docs file, by its path in the
// repository.
func Doc(name string) ([]byte, error) { return docs.ReadFile(name) }

// Skill is the ctxtra skill folder: SKILL.md at its top.
func Skill() fs.FS {
	sub, err := fs.Sub(skill, "skills/ctxtra")
	if err != nil {
		panic(err) // the path is embedded above
	}
	return sub
}
