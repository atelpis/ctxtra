package chunk

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/atelpis/ctxtra/internal/jsonorder"
)

// file is a block like resolve renders it, with n numbered lines of text.
func file(name string, n int, text string) Block {
	var b strings.Builder
	fmt.Fprintf(&b, "Contents of /%s (instructions added by ctxtra for every session; binding: me):\n\n", name)
	for i := range n {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s L%04d %s", name, i, text)
	}
	return Block{Title: "Contents of /" + name, Text: b.String()}
}

var reLabel = regexp.MustCompile(`^\[ctxtra part (\d+) of (\d+)[];]`)

// check checks the parts of blocks: each fits size, has a label with the
// right numbers, is valid UTF-8, and every line of the blocks is in exactly
// one part, in order, or for a line longer than a part, in consecutive parts.
func check(t *testing.T, blocks []Block, size int, u Unit, parts []string) {
	t.Helper()
	if len(parts) < 2 {
		t.Fatalf("%d parts, want several", len(parts))
	}
	for i, p := range parts {
		if u.Len(p) > size {
			t.Errorf("part %d: %d %s, over %d", i+1, u.Len(p), u, size)
		}
		if !utf8.ValidString(p) {
			t.Errorf("part %d: not valid UTF-8", i+1)
		}
		if strings.HasSuffix(p, "):") || strings.HasSuffix(p, "):\n\n") {
			t.Errorf("part %d ends with a header", i+1)
		}
		m := reLabel.FindStringSubmatch(p)
		if m == nil || m[1] != fmt.Sprint(i+1) || m[2] != fmt.Sprint(len(parts)) {
			t.Errorf("part %d of %d: label %q", i+1, len(parts), p[:min(len(p), 60)])
		}
	}
	if !strings.Contains(parts[0], "read all") {
		t.Errorf("part 1 doesn't say that the parts are one text: %q", parts[0][:60])
	}
	all := strings.Join(parts, "\n")
	pos := 0
	for _, b := range blocks {
		for line := range strings.Lines(b.Text) {
			line = strings.TrimSuffix(line, "\n")
			if line == "" {
				continue
			}
			if u.Len(line) > size/2 {
				continue // checked by the long line test
			}
			i := strings.Index(all[pos:], line)
			if i < 0 {
				t.Fatalf("line %q missing or out of order", line)
			}
			pos += i + len(line)
		}
	}
}

func TestOnePart(t *testing.T) {
	blocks := []Block{file("a.md", 10, "short"), {Title: "Skills", Text: "Skills added by ctxtra"}}
	text := Join(blocks)
	for _, size := range []int{0, len(text), len(text) + 100} {
		if got := Split(blocks, size, Chars); len(got) != 1 || got[0] != text {
			t.Errorf("size %d: %q, want the text as it is", size, got)
		}
	}
	if got := Split(nil, 100, Chars); got != nil {
		t.Errorf("empty text: %q", got)
	}
	if got := Split(blocks, len(text)-1, Chars); len(got) < 2 {
		t.Errorf("one character over the size: %d parts", len(got))
	}
}

func TestBlocks(t *testing.T) {
	// Small blocks that fit stay whole, and a large one is split at line
	// ends, with a header that names the file and its part.
	blocks := []Block{file("a.md", 20, "small file"), file("big.md", 400, "a larger file with more text"),
		file("c.md", 20, "small file")}
	parts := Split(blocks, 2000, Chars)
	check(t, blocks, 2000, Chars, parts)
	for _, name := range []string{"a.md", "c.md"} {
		n := 0
		for _, p := range parts {
			if strings.Contains(p, name+" L") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s is in %d parts, want 1", name, n)
		}
	}
	n := 0
	for _, p := range parts {
		if strings.Contains(p, "big.md L") {
			n++
		}
	}
	want := fmt.Sprintf("Contents of /big.md, continued (part 2 of %d):\n\nbig.md L", n)
	if !strings.Contains(strings.Join(parts, ""), want) {
		t.Errorf("no %q in the parts", want)
	}
	if !strings.Contains(parts[len(parts)-1], "Contents of /c.md (instructions") {
		t.Errorf("last part:\n%s", parts[len(parts)-1])
	}
}

func TestBytes(t *testing.T) {
	// Three bytes per character: the parts are cut by bytes, never inside
	// a character.
	blocks := []Block{file("uni.md", 300, "観測性と分散システム"), file("b.md", 5, "x")}
	for _, size := range []int{500, 1001, 4000} {
		parts := Split(blocks, size, Bytes)
		check(t, blocks, size, Bytes, parts)
		if chars := Split(blocks, size, Chars); len(chars) >= len(parts) {
			t.Errorf("size %d: %d parts by characters, %d by bytes", size, len(chars), len(parts))
		}
	}
}

func TestLongLine(t *testing.T) {
	// A line longer than a part is split between characters, also in the
	// middle of a multi-byte one's neighbours.
	line := strings.Repeat("ab界🚀", 2000)
	blocks := []Block{{Title: "Contents of /x.md", Text: "Contents of /x.md:\n\n" + line + "\nend"}}
	for _, u := range []Unit{Chars, Bytes} {
		parts := Split(blocks, 1000, u)
		check(t, blocks, 1000, u, parts)
		var got strings.Builder
		for _, p := range parts {
			p = p[strings.Index(p, "]\n\n")+3:]
			if i := strings.Index(p, "):\n\n"); i >= 0 && strings.HasPrefix(p, "Contents of /x.md, continued") {
				p = p[i+4:]
			}
			got.WriteString(p)
		}
		if !strings.Contains(got.String(), line) {
			t.Errorf("%s: the long line isn't whole when the parts are joined", u)
		}
	}
}

func TestLabelDigits(t *testing.T) {
	// More than 9 and more than 99 parts: the labels grow, and still fit.
	for _, n := range []int{50, 600} {
		blocks := []Block{file("a.md", n, strings.Repeat("y", 150))}
		parts := Split(blocks, 400, Chars)
		check(t, blocks, 400, Chars, parts)
		if n == 600 && len(parts) < 100 {
			t.Errorf("%d parts, want over 99", len(parts))
		}
	}
}

func TestSmallSize(t *testing.T) {
	// A size below the labels still ends, with a character per part.
	parts := Split([]Block{{Title: "T", Text: "abcdef"}}, 3, Chars)
	if len(parts) != 6 {
		t.Errorf("%d parts: %q", len(parts), parts)
	}
}

func TestUTF16(t *testing.T) {
	for s, want := range map[string]int{"": 0, "abc": 3, "観測": 2, "🚀": 2, "a🚀b": 4} {
		if got := UTF16.Len(s); got != want {
			t.Errorf("%q: %d units, want %d", s, got, want)
		}
	}
	// Lines of emoji: by characters the parts would be twice the size.
	blocks := []Block{file("emoji.md", 300, strings.Repeat("🚀", 30)), file("b.md", 3, "x")}
	parts := Split(blocks, 1000, UTF16)
	check(t, blocks, 1000, UTF16, parts)
	if len(parts) <= len(Split(blocks, 1000, Chars)) {
		t.Errorf("%d parts in UTF-16 units, no more than in characters", len(parts))
	}
	// A long line of emoji is split between characters, never between the
	// two halves of one.
	long := []Block{{Title: "Contents of /x.md", Text: "Contents of /x.md:\n\n" + strings.Repeat("🚀", 3000)}}
	check(t, long, 1001, UTF16, Split(long, 1001, UTF16))
}

func TestJSON(t *testing.T) {
	// Every ASCII character, the separators that encoding/json escapes and
	// a valid U+FFFD: the length is what the hook answer has. A byte that
	// isn't UTF-8 counts as the longer of the two ways Go writes it.
	var all strings.Builder
	for c := range 0x80 {
		all.WriteByte(byte(c))
	}
	for _, s := range []string{all.String(), "a\u2028b\u2029c", "x\xffy", "\ufffd", "観測🚀", `"\"\\`} {
		b, err := jsonorder.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := JSON.Len(s), len(b)-2; got != want && (got < want || utf8.ValidString(s)) {
			t.Errorf("%q: %d, want %d", s, got, want)
		}
	}
	// Quotes and backslashes take twice the room: more parts than by
	// bytes, each within the size once encoded.
	blocks := []Block{file("esc.md", 300, strings.Repeat(`\"`, 20)), file("b.md", 3, "x")}
	parts := Split(blocks, 1000, JSON)
	check(t, blocks, 1000, JSON, parts)
	if len(parts) <= len(Split(blocks, 1000, Bytes)) {
		t.Errorf("%d parts in JSON, no more than in bytes", len(parts))
	}
	for i, p := range parts {
		if b, _ := jsonorder.Marshal(p); len(b)-2 > 1000 {
			t.Errorf("part %d: %d bytes of JSON", i+1, len(b)-2)
		}
	}
}
