package resolve

import (
	"fmt"
	"strings"

	"github.com/atelpis/ctxtra/internal/chunk"
	"github.com/atelpis/ctxtra/internal/paths"
)

// Render is the text given to the agent: each file with a short header, then
// the list of skills. A nil Result renders as "".
func (r *Result) Render() string { return chunk.Join(r.Blocks()) }

// RenderFiles is Render without the list of skills, for agents that get the
// skills as native skills.
func (r *Result) RenderFiles() string {
	if r == nil {
		return ""
	}
	return chunk.Join(r.fileBlocks())
}

// Blocks are the parts of Render: a block per file, then one for the list
// of skills.
func (r *Result) Blocks() []chunk.Block {
	if r == nil {
		return nil
	}
	blocks := r.fileBlocks()
	if len(r.Skills) > 0 {
		blocks = append(blocks, chunk.Block{Title: "Skills added by ctxtra", Text: r.skillBlock()})
	}
	return blocks
}

func (r *Result) fileBlocks() []chunk.Block {
	blocks := make([]chunk.Block, 0, len(r.Files)+2)
	for _, h := range r.Files {
		blocks = append(blocks, chunk.Block{Title: "Contents of " + h.Path, Text: r.block(h)})
	}
	if len(r.Known) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "Files that ctxtra also adds %s, which this session already has:", r.scope(false))
		for _, h := range r.Known {
			fmt.Fprintf(&b, "\n- %s (binding: %s)", h.Path, h.Origin)
		}
		blocks = append(blocks, chunk.Block{Title: "Files that ctxtra also adds", Text: b.String()})
	}
	return blocks
}

func (r *Result) block(h Hit) string {
	return fmt.Sprintf("Contents of %s (instructions added by ctxtra %s; binding: %s):\n\n%s",
		h.Path, r.scope(h.Global), h.Origin, strings.TrimRight(h.Content, " \t\r\n"))
}

// scope says which sessions get a file, of a global binding or not.
func (r *Result) scope(global bool) string {
	switch {
	case global:
		return "for every session"
	case r.Repo != nil:
		return fmt.Sprintf("for this repository, %s, and all its worktrees", paths.Tilde(r.Repo.Root))
	}
	return "for sessions in " + paths.Tilde(r.Dir)
}

// skillBlock lists the skills in the format Pi uses for its own skills, which
// agents already follow: name, description, and where to read the rest.
func (r *Result) skillBlock() string {
	var b strings.Builder
	b.WriteString("Skills added by ctxtra for this session. Each skill is a folder with a SKILL.md file of instructions for one kind of task. " +
		"These skills are not installed in your agent: your skill tool and slash commands can't load them. " +
		"To use one, read its SKILL.md at the location below with your file-reading tool. " +
		"When a task matches a skill's description, or an instruction names one of these skills, read its SKILL.md before you start and follow it. " +
		"When a SKILL.md references a relative path, resolve it against the skill's folder (the folder of SKILL.md).\n\n<available_skills>\n")
	for _, h := range r.Skills {
		fmt.Fprintf(&b, "  <skill>\n    <name>%s</name>\n    <description>%s</description>\n    <location>%s</location>\n  </skill>\n",
			xmlEscape(h.Name), xmlEscape(h.Description), xmlEscape(h.Path))
	}
	b.WriteString("</available_skills>")
	return b.String()
}

var xmlEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace
