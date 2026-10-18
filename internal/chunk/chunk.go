// Package chunk splits the text that ctxtra adds into parts that each fit
// one hook output of an agent. Each part says which part it is, so the text
// stays correct also when an agent puts the parts in another order.
package chunk

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Unit is how an agent measures text.
type Unit int

const (
	Chars Unit = iota // Unicode code points
	Bytes             // bytes of UTF-8
	// UTF16 is UTF-16 code units, what JavaScript's length counts: a
	// character outside the Basic Multilingual Plane, such as an emoji, is 2.
	UTF16
	// JSON is bytes of the text as a JSON string in a hook answer, without
	// its quotes: UTF-8 with the escapes that jsonorder.Marshal writes, for
	// example 2 bytes for a quote, a backslash or a line end.
	JSON
)

// Len is the length of s in u.
func (u Unit) Len(s string) int {
	switch u {
	case Bytes:
		return len(s)
	case Chars:
		return utf8.RuneCountInString(s)
	}
	n := 0
	for i, r := range s {
		n += u.width(s[i:], r)
	}
	return n
}

// width is how many units r, the rune at the start of s, is. Not for Bytes.
func (u Unit) width(s string, r rune) int {
	switch u {
	case UTF16:
		if r > 0xFFFF {
			return 2
		}
	case JSON:
		return jsonWidth(s, r)
	}
	return 1
}

// jsonWidth is the length of r, the rune at the start of s, in a JSON
// string written by encoding/json without HTML escaping.
func jsonWidth(s string, r rune) int {
	switch {
	case r == '"' || r == '\\' || r == '\b' || r == '\f' || r == '\n' || r == '\r' || r == '\t':
		return 2
	case r < 0x20 || r == '\u2028' || r == '\u2029':
		return 6 // \u00XX, \u2028
	case r == utf8.RuneError:
		if _, n := utf8.DecodeRuneInString(s); n == 1 {
			return 6 // a byte that isn't UTF-8: \ufffd, or in newer Go U+FFFD itself
		}
	}
	return utf8.RuneLen(r)
}

// String names u in messages. UTF-16 units are "characters": they are, but
// for emoji and rare scripts.
func (u Unit) String() string {
	switch u {
	case Bytes, JSON:
		return "bytes"
	}
	return "characters"
}

// Block is one part of the text: a file with its header, or the list of
// skills.
type Block struct {
	Title string // names the block in the header of a continued part, for example "Contents of /a.md"
	Text  string
}

// Sep separates blocks.
const Sep = "\n\n"

// Join is the whole text of blocks.
func Join(blocks []Block) string {
	texts := make([]string, len(blocks))
	for i, b := range blocks {
		texts[i] = b.Text
	}
	return strings.Join(texts, Sep)
}

// Split returns the text of blocks in parts of at most size, in u, labels
// included. Text that fits one part is Join(blocks), without labels; empty
// text has no parts. Otherwise a block stays whole where that wastes little
// room, a larger one is split at line ends, and a line longer than a part is
// split between characters.
func Split(blocks []Block, size int, u Unit) []string {
	text := Join(blocks)
	switch {
	case text == "":
		return nil
	case size <= 0 || u.Len(text) <= size:
		return []string{text}
	}
	// Labels hold the number of parts, which is known only after the split:
	// room is kept for numbers of up to digits digits, and the split runs
	// again with more digits when there are more parts.
	for digits := 1; ; digits++ {
		if parts := split(blocks, size, u, digits); len(fmt.Sprint(len(parts))) <= digits {
			return render(blocks, parts)
		}
	}
}

// piece is a part of a block in one chunk: part n of the block's parts.
type piece struct {
	block, n int
	text     string
}

func split(blocks []Block, size int, u Unit, digits int) [][]piece {
	most := strings.Repeat("9", digits)
	labelLen := func(chunk int) int { return u.Len(label(fmt.Sprint(chunk), most)) }
	var chunks [][]piece
	var cur []piece
	used := labelLen(1)
	next := func() {
		chunks = append(chunks, cur)
		cur, used = nil, labelLen(len(chunks)+1)
	}
	for bi, b := range blocks {
		rest := b.Text
		for n := 1; rest != ""; {
			head := ""
			if n > 1 {
				head = continued(b.Title, fmt.Sprint(n), most)
			}
			sep := 0
			if len(cur) > 0 {
				sep = u.Len(Sep)
			}
			free := size - used - sep - u.Len(head)
			text, all, line := u.cut(rest, free)
			if all {
				cur = append(cur, piece{bi, n, rest})
				used += sep + u.Len(head) + u.Len(rest)
				break
			}
			if len(cur) > 0 {
				// A block that fits the next chunk goes there when little room
				// is left here, and so does a line that doesn't fit here, and
				// a block's first line (its header) without the next one.
				fresh := size - labelLen(len(chunks)+2) - u.Len(head)
				_, fits, _ := u.cut(rest, fresh)
				lines := strings.Count(strings.TrimRight(text, "\n"), "\n") + 1
				if !line || strings.TrimRight(text, "\n") == "" || (n == 1 && lines < 2) || (fits && free < size/10) {
					next()
					continue
				}
			}
			if text == "" {
				// Not even one character fits: size is smaller than the labels.
				_, w := utf8.DecodeRuneInString(rest)
				text = rest[:w]
			}
			cur = append(cur, piece{bi, n, strings.TrimSuffix(text, "\n")})
			rest = rest[len(text):]
			n++
			next()
		}
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// cut returns the longest start of s that is at most n long and ends at a
// line end, and whether that is all of s. When no line fits, it is the
// longest start that ends between characters, and line is false. It reads
// at most n units of s.
func (u Unit) cut(s string, n int) (text string, all, line bool) {
	n = max(n, 0)
	end := len(s) // after the first n units
	if u == Bytes {
		if len(s) <= n {
			return s, true, true
		}
		end = n
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
	} else {
		count := 0
		for i, r := range s {
			if w := u.width(s[i:], r); count+w > n {
				end = i
				break
			} else {
				count += w
			}
		}
		if end == len(s) {
			return s, true, true
		}
	}
	if i := strings.LastIndexByte(s[:end], '\n'); i >= 0 {
		return s[:i+1], false, true
	}
	return s[:end], false, false
}

func label(n, total string) string {
	if n == "1" {
		return fmt.Sprintf("[ctxtra part 1 of %s; read all %s parts as one text, in part order]%s", total, total, Sep)
	}
	return fmt.Sprintf("[ctxtra part %s of %s]%s", n, total, Sep)
}

func continued(title, n, total string) string {
	return fmt.Sprintf("%s, continued (part %s of %s):%s", title, n, total, Sep)
}

func render(blocks []Block, chunks [][]piece) []string {
	parts := map[int]int{}
	for _, c := range chunks {
		for _, p := range c {
			parts[p.block] = p.n
		}
	}
	total := fmt.Sprint(len(chunks))
	out := make([]string, len(chunks))
	for i, c := range chunks {
		var b strings.Builder
		b.WriteString(label(fmt.Sprint(i+1), total))
		for j, p := range c {
			if j > 0 {
				b.WriteString(Sep)
			}
			if p.n > 1 {
				b.WriteString(continued(blocks[p.block].Title, fmt.Sprint(p.n), fmt.Sprint(parts[p.block])))
			}
			b.WriteString(p.text)
		}
		out[i] = b.String()
	}
	return out
}
