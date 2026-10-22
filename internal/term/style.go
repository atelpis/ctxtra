package term

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Style is how a terminal shows text: an SGR parameter.
type Style string

const (
	Bold   Style = "1"
	Dim    Style = "2"
	Red    Style = "31"
	Green  Style = "32"
	Yellow Style = "33"
)

// Fprintf writes to w as fmt.Fprintf does, with the text in style s when
// w is a Writer to a terminal that shows colors. The text is sanitized as
// Writer does; only the style's own escapes reach the terminal. A trailing
// newline comes after the end of the style.
func Fprintf(w io.Writer, s Style, format string, a ...any) {
	text := fmt.Sprintf(format, a...)
	tw, ok := w.(Writer)
	if !ok || !colors(tw.W) {
		io.WriteString(w, text)
		return
	}
	text, nl := strings.CutSuffix(text, "\n")
	end := "\x1b[0m"
	if nl {
		end += "\n"
	}
	io.WriteString(tw.W, "\x1b["+string(s)+"m"+Sanitize(text)+end)
}

// colors reports whether w is a terminal that shows colors, unless the
// user turned them off (https://no-color.org); a variable so tests can
// replace it.
var colors = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" && showsColors(f)
}
