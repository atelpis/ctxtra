// Package risk finds text in a source's files that is easy to miss when you
// read a diff: hidden characters, HTML comments, encoded blocks, downloaded
// code that runs, paths of secrets, and new URLs. It helps a review; it
// doesn't decide whether a file is safe. Plain text can still do harm.
//
// The checks are fixed patterns: the same text always gives the same result.
package risk

import (
	"bytes"
	"fmt"
	"iter"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The checks, as the review names them.
const (
	Hidden  = "hidden character"
	Comment = "HTML comment"
	Encoded = "encoded text"
	Remote  = "remote code"
	Secret  = "secret path"
	URL     = "new URL"
)

// MinEncoded is the shortest run of base64 or hex characters that is
// reported as encoded text. A SHA-256 checksum, 64 hex digits, is shorter.
const MinEncoded = 80

// maxExcerpt is the most characters of file text in a finding.
const maxExcerpt = 80

// Finding is one check that matched on one line.
type Finding struct {
	Line  int    // from 1
	Check string // one of the constants above
	Text  string // what matched; may hold any character, so escape it before printing
	url   string // the whole URL, for a new URL
}

// Text reports whether data can be checked: UTF-8 without NUL bytes.
func Text(data []byte) bool {
	return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

// Checker checks the files of one review. A URL is new unless Known saw it
// or Shown was called with a finding for it.
type Checker struct {
	urls map[string]bool
}

func NewChecker() *Checker { return &Checker{urls: map[string]bool{}} }

// Shown records that a finding was shown, so that a new URL in it isn't
// reported again. A finding that isn't shown, because a file has too many,
// leaves the URL new for the next file.
func (c *Checker) Shown(f Finding) {
	if f.url != "" {
		c.urls[f.url] = true
	}
}

// Known records the URLs in the previous version of a file.
func (c *Checker) Known(data []byte) {
	for line := range strings.SplitSeq(string(data), "\n") {
		for _, u := range urls(line) {
			c.urls[u] = true
		}
	}
}

// Scan checks the lines of data for which changed is true, every line when
// changed is nil, and yields the findings in line order. data must be Text.
// An HTML comment is reported once, at its first changed line, but the whole
// file is read to know where comments are.
func (c *Checker) Scan(data []byte, changed func(line int) bool) iter.Seq[Finding] {
	return func(yield func(Finding) bool) {
		inComment, comments, reported := false, 0, 0
		seen := map[string]bool{} // new URLs reported in this file
		n := 0
		for line := range strings.SplitSeq(string(data), "\n") {
			n++
			var ids []int
			ids, inComment, comments = commentsOn(line, inComment, comments)
			if changed != nil && !changed(n) {
				continue
			}
			var fs []Finding
			add := func(check, text string) { fs = append(fs, Finding{Line: n, Check: check, Text: text}) }
			if t := hidden(line, n == 1); t != "" {
				add(Hidden, t)
			}
			// Comments are numbered in file order, so the ones not reported
			// yet are those after the last reported one.
			if len(ids) > 0 && ids[len(ids)-1] > reported {
				reported = ids[len(ids)-1]
				add(Comment, excerpt(commentStart(line)))
			}
			if t := encoded(line); t != "" {
				add(Encoded, t)
			}
			if m := remote(line); m != "" {
				add(Remote, excerpt(m))
			}
			if m := secret(line); m != "" {
				add(Secret, m)
			}
			for _, u := range urls(line) {
				if !c.urls[u] && !seen[u] {
					seen[u] = true
					fs = append(fs, Finding{Line: n, Check: URL, Text: excerpt(u), url: u})
				}
			}
			for _, f := range fs {
				if !yield(f) {
					return
				}
			}
		}
	}
}

// commentsOn returns the HTML comments that line is part of, numbered from 1
// in the file, given whether a comment is open where the line starts and how
// many comments came before it. It also returns the state after the line.
func commentsOn(line string, open bool, count int) (ids []int, stillOpen bool, n int) {
	if open {
		ids = append(ids, count)
	}
	for {
		if open {
			i := strings.Index(line, "-->")
			if i < 0 {
				return ids, true, count
			}
			line, open = line[i+3:], false
			continue
		}
		i := strings.Index(line, "<!--")
		if i < 0 {
			return ids, false, count
		}
		count++
		ids = append(ids, count)
		line, open = line[i+4:], true
	}
}

// commentStart is line from its first "<!--", or all of it inside a comment.
func commentStart(line string) string {
	if i := strings.Index(line, "<!--"); i >= 0 {
		return line[i:]
	}
	return strings.TrimSpace(line)
}

// hidden describes the hidden characters of line, or is "". A byte order
// mark at the start of the file, a carriage return at the end of a line and
// a zero-width joiner in an emoji sequence are not reported.
func hidden(line string, first bool) string {
	var found []rune
	var prev rune
	selectors := 0
	for i, r := range line {
		next, _ := utf8.DecodeRuneInString(line[i+utf8.RuneLen(r):])
		if isSelector(r) {
			selectors++
		} else {
			selectors = 0
		}
		switch {
		case r == 0xFEFF && first && i == 0:
		case r == '\r' && i == len(line)-1:
		case r == 0x200D && emoji(prev) && emoji(next):
		case selectors == 2:
			found = append(found, r)
		case kind(r) != "":
			found = append(found, r)
		}
		prev = r
	}
	if len(found) == 0 {
		return ""
	}
	k := kind(found[0])
	if k == "" {
		k = "variation selectors in a row"
	}
	s := fmt.Sprintf("U+%04X (%s)", found[0], k)
	if len(found) > 1 {
		s += fmt.Sprintf(" and %d more", len(found)-1)
	}
	return s
}

// kind is what a hidden character does, or "" for a visible one.
func kind(r rune) string {
	switch {
	case r == 0x200B || r == 0x200C || r == 0x200D || r == 0x2060 || r == 0xFEFF:
		return "zero width"
	case r == 0x200E || r == 0x200F || r == 0x061C || r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069:
		return "text direction"
	case r >= 0xE0000 && r <= 0xE007F:
		return "tag"
	case r == 0x2028 || r == 0x2029:
		return "line separator"
	case unicode.Is(unicode.Cf, r):
		return "format"
	case r != '\t' && unicode.IsControl(r):
		return "control"
	}
	return ""
}

func isSelector(r rune) bool {
	return r >= 0xFE00 && r <= 0xFE0F || r >= 0xE0100 && r <= 0xE01EF
}

// emoji reports whether r can be part of an emoji sequence.
func emoji(r rune) bool {
	return unicode.Is(unicode.So, r) || r == 0xFE0F || r >= 0x1F3FB && r <= 0x1F3FF
}

var (
	reHex    = regexp.MustCompile(fmt.Sprintf(`[0-9A-Fa-f]{%d,}`, MinEncoded))
	reBase64 = regexp.MustCompile(fmt.Sprintf(`[A-Za-z0-9+/_-]{%d,}=*`, MinEncoded))
)

// encoded describes the longest encoded block in line, outside URLs, or is
// "". A base64 block needs an uppercase letter, a lowercase letter and a
// digit, so that long words, paths and lines of dashes don't count.
func encoded(line string) string {
	line = reURL.ReplaceAllString(line, " ")
	if m := longest(reHex.FindAllString(line, -1)); m != "" {
		return fmt.Sprintf("%d hex characters", len(m))
	}
	var best string
	for _, m := range reBase64.FindAllString(line, -1) {
		if len(m) > len(best) && strings.ContainsFunc(m, unicode.IsUpper) && strings.ContainsFunc(m, unicode.IsLower) && strings.ContainsFunc(m, unicode.IsDigit) {
			best = m
		}
	}
	if best != "" {
		return fmt.Sprintf("%d base64 characters", len(best))
	}
	return ""
}

func longest(ms []string) string {
	var best string
	for _, m := range ms {
		if len(m) > len(best) {
			best = m
		}
	}
	return best
}

// A download command whose output a shell or an interpreter runs: through a
// pipe or after && (also sudo, env or a path before the interpreter), inside $( ), <( ) or backticks, or PowerShell's
// iex (iwr ...).
const (
	download    = `(?:curl|wget|iwr|irm|invoke-webrequest|invoke-restmethod)`
	interpreter = `(?:sh|bash|zsh|dash|ksh|fish|python[0-9.]*|perl|ruby|node|iex|invoke-expression|pwsh|powershell)`
)

var reRemote = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b` + download + `\b.*(?:\||&&)\s*(?:sudo\s+(?:-\S+\s+)*)?(?:env\s+)?(?:[\w./~-]*/)?` + interpreter + `\b`),
	regexp.MustCompile(`(?i)\b(?:` + interpreter + `|source|eval)\b.*(?:\$\(|<\(|` + "`" + `)\s*` + download + `\b`),
	regexp.MustCompile(`(?i)\b(?:iex|invoke-expression)\s*\(\s*` + download + `\b`),
}

func remote(line string) string {
	for _, re := range reRemote {
		if m := re.FindString(line); m != "" {
			return m
		}
	}
	return ""
}

// Files and folders that hold keys, tokens or passwords.
var reSecret = regexp.MustCompile(`(?:^|[^\w.-])(\.ssh|\.gnupg|\.aws|\.azure|\.kube|\.docker/config\.json|\.config/gcloud|\.netrc|\.npmrc|\.pypirc|\.git-credentials|\.env(?:\.[\w.-]+)?)(?:$|[^\w-])|\b(id_(?:rsa|dsa|ecdsa|ed25519)\w*)`)

func secret(line string) string {
	m := reSecret.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1] + m[2]
}

var reURL = regexp.MustCompile(`(?i)\b(?:https?|ftp|wss?)://[^\s<>"'` + "`" + `\\)\]}]+`)

// urls are the URLs in line, without trailing punctuation.
func urls(line string) []string {
	var out []string
	for _, u := range reURL.FindAllString(line, -1) {
		out = append(out, strings.TrimRight(u, ".,;:!?*_~"))
	}
	return out
}

// excerpt is s cut to maxExcerpt characters.
func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxExcerpt {
		return s
	}
	return string([]rune(s)[:maxExcerpt]) + "…"
}
