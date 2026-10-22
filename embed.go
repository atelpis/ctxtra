// Package ctxtra holds the files the binary serves as they are: the docs
// (ctxtra help TOPIC) and ctxtra's skills (ctxtra skill). They live at the
// top of the repository, where people and skill installers find them, and
// the binary embeds them, so it always serves the version it was built from.
package ctxtra

import (
	"embed"
	"io/fs"
	"slices"
)

//go:embed docs/guide.md docs/spec.md docs/scenarios.md
var docs embed.FS

//go:embed skills
var skills embed.FS

// Doc returns the content of an embedded docs file, by its path in the
// repository.
func Doc(name string) ([]byte, error) { return docs.ReadFile(name) }

// SkillNames are the names of ctxtra's skills, sorted: the folders in
// skills/.
func SkillNames() []string {
	entries, err := skills.ReadDir("skills")
	if err != nil {
		panic(err) // the folder is embedded above
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// Skill is the folder of the skill name, SKILL.md at its top, or nil when
// ctxtra has no skill of that name.
func Skill(name string) fs.FS {
	if !slices.Contains(SkillNames(), name) {
		return nil
	}
	sub, err := fs.Sub(skills, "skills/"+name)
	if err != nil {
		panic(err) // the path is embedded above
	}
	return sub
}
