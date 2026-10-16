package resolve

import (
	"fmt"
	"strings"

	"github.com/atelpis/ctxtra/internal/paths"
)

const blockSep = "\n\n"

// Render is the text given to the agent: each file with a short header, then
// the list of skills. A nil Result renders as "".
func (r *Result) Render() string {
	if r == nil {
		return ""
	}
	blocks := r.fileBlocks()
	if len(r.Skills) > 0 {
		blocks = append(blocks, r.skillBlock())
	}
	return strings.Join(blocks, blockSep)
}

// RenderFiles is Render without the list of skills, for agents that get the
// skills as native skills.
func (r *Result) RenderFiles() string {
	if r == nil {
		return ""
	}
	return strings.Join(r.fileBlocks(), blockSep)
}

func (r *Result) fileBlocks() []string {
	blocks := make([]string, 0, len(r.Files)+1)
	for _, h := range r.Files {
		blocks = append(blocks, r.block(h))
	}
	return blocks
}

func (r *Result) block(h Hit) string {
	scope := "for every session"
	switch {
	case h.Global:
	case r.Repo != nil:
		scope = fmt.Sprintf("for this repository, %s, and all its worktrees", paths.Tilde(r.Repo.Root))
	default:
		scope = "for sessions in " + paths.Tilde(r.Dir)
	}
	return fmt.Sprintf("Contents of %s (instructions added by ctxtra %s; binding: %s):\n\n%s",
		h.Path, scope, h.Origin, strings.TrimRight(h.Content, " \t\r\n"))
}

// skillBlock lists the skills in the format Pi uses for its own skills, which
// agents already follow: name, description, and where to read the rest.
func (r *Result) skillBlock() string {
	var b strings.Builder
	b.WriteString("Skills added by ctxtra for this session. Each skill is a folder with a SKILL.md file of instructions for one kind of task. " +
		"When a task matches a skill's description, read its SKILL.md before you start and follow it, as you would with your own skills. " +
		"When a SKILL.md references a relative path, resolve it against the skill's folder (the folder of SKILL.md).\n\n<available_skills>\n")
	for _, h := range r.Skills {
		fmt.Fprintf(&b, "  <skill>\n    <name>%s</name>\n    <description>%s</description>\n    <location>%s</location>\n  </skill>\n",
			xmlEscape(h.Name), xmlEscape(h.Description), xmlEscape(h.Path))
	}
	b.WriteString("</available_skills>")
	return b.String()
}

var xmlEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace
