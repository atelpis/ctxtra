package resolve

import (
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// maxSkillDesc limits a skill's description, in bytes, including the "…"
// added to a cut one.
const maxSkillDesc = 1024

const ellipsis = "…"

// parseSkill reads name and description from a SKILL.md's YAML frontmatter.
// Unknown keys are ignored: skills are written for other tools too.
func parseSkill(content string) (name, desc string, err error) {
	content = strings.TrimPrefix(content, "\ufeff")
	rest, ok := strings.CutPrefix(strings.ReplaceAll(content, "\r\n", "\n"), "---\n")
	if !ok {
		return "", "", errors.New("no YAML frontmatter (a SKILL.md starts with ---)")
	}
	front, ok := frontmatter(rest)
	if !ok {
		return "", "", errors.New("frontmatter is not closed with a --- line")
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
		return "", "", fmt.Errorf("bad frontmatter: %s", strings.Join(strings.Fields(err.Error()), " "))
	}
	desc = strings.Join(strings.Fields(meta.Description), " ")
	if desc == "" {
		return "", "", errors.New("frontmatter has no description")
	}
	if len(desc) > maxSkillDesc {
		desc = truncate(desc, maxSkillDesc-len(ellipsis)) + ellipsis
	}
	return strings.TrimSpace(meta.Name), desc, nil
}

// frontmatter returns the lines of s before the first line that is exactly
// "---" (trailing spaces allowed), and whether there is one.
func frontmatter(s string) (string, bool) {
	n := 0
	for line := range strings.Lines(s) {
		if strings.TrimRight(line, " \t\n") == "---" {
			return s[:n], true
		}
		n += len(line)
	}
	return "", false
}

// truncate cuts s to at most n bytes without splitting a UTF-8 character.
func truncate(s string, n int) string {
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
