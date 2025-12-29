package theme

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type ColorKind int

const (
	COLOR_NONE ColorKind = iota
	COLOR_RGB
	COLOR_INDEX
)

type Color struct {
	Kind              ColorKind
	Red, Green, Blue  uint8
	Index             int
}

type TextStyle struct {
	Bold      bool
	Italic    bool
	Underline bool
	Reverse   bool
}

type TextHighlight struct {
	Foreground Color
	Background Color
	TxtStyle   TextStyle
}

type Theme struct {
	Name       string
	Background string // "dark" or "light"
	Groups     map[string]TextHighlight
	Links      map[string]string
	Terminal   [16]Color
}

func ParseVimColorscheme(r io.Reader) (*Theme, error) {
	t := &Theme{
		Groups: make(map[string]TextHighlight),
		Links:  make(map[string]string),
	}

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "\"") {
			continue
		}

		switch {
		case line == "hi clear" || line == "highlight clear":
			t.Groups = make(map[string]TextHighlight)
			t.Links = make(map[string]string)

		case strings.HasPrefix(line, "let g:colors_name"):
			if name, ok := parseLetString(line); ok {
				t.Name = name
			}

		case strings.HasPrefix(line, "set background="):
			t.Background = strings.TrimPrefix(line, "set background=")

		case strings.HasPrefix(line, "call s:hi("):
			if err := parseCallHi(t, line); err != nil {
				return nil, err
			}

		case strings.Contains(line, "hi! link") || strings.Contains(line, "hi link"):
			if from, to, ok := parseHiLink(line); ok {
				t.Links[from] = to
			}

		case strings.HasPrefix(line, "let g:terminal_color_"):
			if idx, col, ok := parseTerminalColor(line); ok && idx >= 0 && idx < 16 {
				t.Terminal[idx] = col
			}
		}
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Theme) ResolveGroup(name string) (TextHighlight, bool) {
	if t == nil {
		return TextHighlight{}, false
	}

	visited := make(map[string]bool)
	cur := name

	for {
		if visited[cur] {
			return TextHighlight{}, false
		}
		visited[cur] = true

		if hl, ok := t.Groups[cur]; ok {
			return hl, true
		}
		next, ok := t.Links[cur]
		if !ok {
			return TextHighlight{}, false
		}
		cur = next
	}
}

func parseLetString(line string) (string, bool) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return "", false
	}

	rhs := strings.TrimSpace(line[eq+1:])
	rhs = strings.Trim(rhs, " ")
	rhs = strings.Trim(rhs, "'")
	if rhs == "" {
		return "", false
	}
	return rhs, true
}

func parseCallHi(t *Theme, line string) error {
	l := strings.Index(line, "(")
	r := strings.LastIndex(line, ")")
	if l < 0 || r < 0 || r <= l {
		return fmt.Errorf("bad s:hi call: %q", line)
	}

	args := splitArguments(line[l+1 : r])
	if len(args) != 6 {
		return fmt.Errorf("s:hi expects 6 args, got %d: %q", len(args), line)
	}

	group := unquote(args[0])
	fg := parseColorArgument(args[1])
	bg := parseColorArgument(args[2])

	attr := parseTextStyle(unquote(args[5]))

	t.Groups[group] = TextHighlight{
		Foreground: fg,
		Background: bg,
		TxtStyle:   attr,
	}
	return nil
}

func parseHiLink(line string) (from, to string, ok bool) {
	fields := strings.Fields(line)
	for i := 0; i < len(fields)-2; i++ {
		if fields[i] == "link" && i+2 < len(fields) {
			return fields[i+1], fields[i+2], true
		}
	}
	return "", "", false
}

func parseTerminalColor(line string) (int, Color, bool) {
	prefix := "let g:terminal_color_"
	s := strings.TrimSpace(strings.TrimPrefix(line, prefix))

	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, Color{}, false
	}

	idx, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, Color{}, false
	}

	eq := strings.Index(s, "=")
	if eq < 0 {
		return 0, Color{}, false
	}
	rhs := strings.TrimSpace(s[eq+1:])
	return idx, parseColorArgument(rhs), true
}

func splitArguments(s string) []string {
	var out []string
	var b strings.Builder
	inQuote := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '\'':
			inQuote = !inQuote
			b.WriteByte(ch)
		case ',':
			if inQuote {
				b.WriteByte(ch)
			} else {
				out = append(out, strings.TrimSpace(b.String()))
				b.Reset()
			}
		default:
			b.WriteByte(ch)
		}
	}

	if tail := strings.TrimSpace(b.String()); tail != "" {
		out = append(out, tail)
	}
	return out
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "'")
	return s
}

func parseColorArgument(s string) Color {
	v := unquote(strings.TrimSpace(s))
	if v == "" || strings.EqualFold(v, "NONE") {
		return Color{Kind: COLOR_NONE}
	}

	if strings.HasPrefix(v, "#") && len(v) == 7 {
		r, errR := strconv.ParseUint(v[1:3], 16, 8)
		g, errG := strconv.ParseUint(v[3:5], 16, 8)
		b, errB := strconv.ParseUint(v[5:7], 16, 8)
		if errR == nil && errG == nil && errB == nil {
			return Color{
				Kind:  COLOR_RGB,
				Red:   uint8(r),
				Green: uint8(g),
				Blue:  uint8(b),
			}
		}
		return Color{Kind: COLOR_NONE}
	}

	if n, err := strconv.Atoi(v); err == nil {
		return Color{Kind: COLOR_INDEX, Index: n}
	}

	return Color{Kind: COLOR_NONE}
}

func parseTextStyle(s string) TextStyle {
	if s == "" || strings.EqualFold(s, "NONE") {
		return TextStyle{}
	}

	a := TextStyle{}
	parts := strings.Split(s, ",")
	for _, p := range parts {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "bold":
			a.Bold = true
		case "italic":
			a.Italic = true
		case "underline":
			a.Underline = true
		case "reverse":
			a.Reverse = true
		}
	}
	return a
}
