package cli

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/risk"
	"github.com/atelpis/ctxtra/internal/source"
)

// Content warnings point out text that is easy to miss in a review (package
// risk). They are printed and added to the --json diagnostics; they never
// stop anything.

// diffLine is one line of git diff output.
type diffLine struct {
	text   string // with its newline
	path   string // the file of the section, in the new commit; "" for a removed file
	line   int    // the line in the new file, for an added line
	added  bool
	header bool // the +++ line that names path
}

var reHunk = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// walkDiff calls fn for each line of a diff, with the added lines numbered.
// The hunk headers' line counts decide which lines are content, so a content
// line never passes for a header.
func walkDiff(d string, fn func(diffLine)) {
	path := ""
	oldLeft, newLeft, n := 0, 0, 0
	for text := range strings.SplitAfterSeq(d, "\n") {
		if text == "" {
			continue
		}
		t := strings.TrimSuffix(text, "\n")
		l := diffLine{text: text}
		switch {
		case oldLeft > 0 || newLeft > 0:
			switch {
			case strings.HasPrefix(t, "+"):
				l.added, l.line = true, n
				n++
				newLeft--
			case strings.HasPrefix(t, "-"):
				oldLeft--
			case strings.HasPrefix(t, `\`):
			default: // context; an empty line too, with diff.suppressBlankEmpty
				n++
				oldLeft--
				newLeft--
			}
		case strings.HasPrefix(t, "diff --git "):
			path = ""
		case strings.HasPrefix(t, "+++ "):
			path, l.header = diffPath(t[len("+++ "):]), true
		case reHunk.MatchString(t):
			m := reHunk.FindStringSubmatch(t)
			oldLeft, newLeft = count(m[1]), count(m[3])
			n, _ = strconv.Atoi(m[2])
		}
		l.path = path
		fn(l)
	}
}

// spans are line numbers as ascending ranges: a diff adds lines in runs.
type spans [][2]int

func (s *spans) add(n int) {
	if k := len(*s); k > 0 && (*s)[k-1][1] == n-1 {
		(*s)[k-1][1] = n
		return
	}
	*s = append(*s, [2]int{n, n})
}

func (s spans) has(n int) bool {
	i := sort.Search(len(s), func(i int) bool { return s[i][1] >= n })
	return i < len(s) && s[i][0] <= n
}

// count is a hunk header's line count, which git leaves out when it is 1.
func count(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s)
	return n
}

// diffPath is the path in a +++ line: b/PATH, quoted when it has unusual
// characters, with a tab after it when it has a space. /dev/null is "".
func diffPath(s string) string {
	s = strings.TrimSuffix(s, "\t")
	if strings.HasPrefix(s, `"`) {
		u, err := strconv.Unquote(s)
		if err != nil {
			return ""
		}
		s = u
	}
	p, ok := strings.CutPrefix(s, "b/")
	if !ok {
		return ""
	}
	return p
}

// maxWarnings is the most content warnings shown for one file. A file with
// more gets a note with their number instead of the rest.
const maxWarnings = 50

// contentCheck checks the files of one review of the source src.
type contentCheck struct {
	s       *sources
	src     string
	checker *risk.Checker
}

func (s *sources) contentCheck(src string) *contentCheck {
	return &contentCheck{s: s, src: src, checker: risk.NewChecker()}
}

// read is a file of a generation, and whether it can be checked. A file that
// can't be read is treated as one that can't be checked.
func read(gen, p string) ([]byte, bool) {
	data, err := fsutil.ReadBounded(filepath.Join(gen, filepath.FromSlash(p)), source.MaxFileBytes)
	return data, err == nil && risk.Text(data)
}

// walkGen calls fn for each regular file of a generation, by its path in the
// commit.
func walkGen(gen string, fn func(rel string)) {
	_ = filepath.WalkDir(gen, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			fn(filepath.ToSlash(must(filepath.Rel(gen, p))))
		}
		return nil
	})
}

// known records the URLs of the files of gen that use selects: in the
// accepted commit, so that its URLs aren't new.
func (c *contentCheck) known(gen string, use func(rel string) bool) {
	walkGen(gen, func(rel string) {
		if !use(rel) {
			return
		}
		if data, ok := read(gen, rel); ok {
			c.checker.Known(data)
		}
	})
}

// scan checks the lines of a file that changed selects, adds the warnings to
// the diagnostics, and returns the first maxWarnings of them and how many
// there are.
func (c *contentCheck) scan(path string, data []byte, changed func(int) bool) ([]risk.Finding, int) {
	var shown []risk.Finding
	n := 0
	for f := range c.checker.Scan(data, changed) {
		if n++; n <= maxWarnings {
			c.checker.Shown(f)
			shown = append(shown, f)
			c.diag("warning", fmt.Sprintf("%s:%d: %s: %s", path, f.Line, f.Check, f.Text))
		}
	}
	if n > maxWarnings {
		c.diag("notice", fmt.Sprintf("%s: %d content warnings, the first %d listed", path, n, maxWarnings))
	}
	return shown, n
}

func (c *contentCheck) diag(severity, msg string) {
	c.s.diags = append(c.s.diags, diagnostic{severity, source.Sanitize(c.src + ": " + msg)})
}

const notCheckedWhy = "not checked for content warnings: binary or not UTF-8"

// newExecutable warns about a file that the change makes executable. In the
// text output it is printed only when print is set: a file listed by name
// says "executable" already.
func (c *contentCheck) newExecutable(w io.Writer, ch source.Change, print bool) {
	if ch.NewMode != "100755" || ch.OldMode == "100755" {
		return
	}
	c.diag("warning", ch.Path+": new executable file")
	if print {
		fmt.Fprintf(w, "warning: new executable file: %s\n", source.Sanitize(ch.Path))
	}
}

// printDiff prints a diff of files in gen with the content warnings of its
// added lines, each after its line. Only files in check are read: the
// regular files the diff shows.
func (c *contentCheck) printDiff(w io.Writer, d string, gen string, check map[string]bool) {
	added := map[string]*spans{}
	var order []string
	walkDiff(d, func(l diffLine) {
		if !l.added || !check[l.path] {
			return
		}
		if added[l.path] == nil {
			added[l.path] = &spans{}
			order = append(order, l.path)
		}
		added[l.path].add(l.line)
	})
	findings := map[string]map[int][]risk.Finding{}
	notes := map[string]string{}
	for _, p := range order {
		data, ok := read(gen, p)
		if !ok {
			notes[p] = notCheckedWhy
			c.diag("notice", p+": "+notCheckedWhy)
			continue
		}
		shown, n := c.scan(p, data, added[p].has)
		if n > maxWarnings {
			notes[p] = fmt.Sprintf("%d content warnings in this file; the first %d are shown", n, maxWarnings)
		}
		findings[p] = map[int][]risk.Finding{}
		for _, f := range shown {
			findings[p][f.Line] = append(findings[p][f.Line], f)
		}
	}
	walkDiff(d, func(l diffLine) {
		fmt.Fprint(w, l.text)
		if !strings.HasSuffix(l.text, "\n") {
			fmt.Fprintln(w)
		}
		switch {
		case l.header && notes[l.path] != "":
			fmt.Fprintf(w, "note: %s\n", notes[l.path])
		case l.added:
			for _, f := range findings[l.path][l.line] {
				fmt.Fprintf(w, "warning: line %d: %s: %s\n", f.Line, f.Check, source.Sanitize(f.Text))
			}
		}
	})
}

// checkAdded prints the content warnings of the files that the config uses
// from the source name at commit, whose files are in gen, now that it is
// added: the first version of a source is accepted without a review.
func (s *sources) checkAdded(name, commit, gen string) {
	u := &used{files: map[string]bool{}}
	u.add(config.LoadAt(s.file, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes), name, commit), gen)
	c := s.contentCheck(name)
	walkGen(gen, func(rel string) {
		if !u.has(rel) {
			return
		}
		data, ok := read(gen, rel)
		if !ok {
			c.diag("notice", rel+": "+notCheckedWhy)
			fmt.Fprintf(s.Out, "note: %s: %s\n", source.Sanitize(rel), notCheckedWhy)
			return
		}
		shown, n := c.scan(rel, data, nil)
		for _, f := range shown {
			fmt.Fprintf(s.Out, "warning: %s:%d: %s: %s\n", source.Sanitize(rel), f.Line, f.Check, source.Sanitize(f.Text))
		}
		if n > maxWarnings {
			fmt.Fprintf(s.Out, "note: %s: %d content warnings; the first %d are shown\n", source.Sanitize(rel), n, maxWarnings)
		}
	})
}
