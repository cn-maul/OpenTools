package parse

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"opentools/tools/govdoc/engine/docmodel"
)

// ---------------------------------------------------------------- decoding

// decodeText turns upload bytes into a string without ever guessing at the
// encoding. UTF-8 is required; a BOM selects UTF-16. Anything else — GBK and
// GB18030 above all, which are common for 公文 exports — is reported instead
// of being decoded lossily, because a wrong decode silently rewrites the
// source text and defeats the whole pipeline's invariant.
func decodeText(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		body := data[3:]
		if !utf8.Valid(body) {
			return "", fmt.Errorf("%w: 文件声明为 UTF-8 但字节序列无效", ErrUnsupportedEncoding)
		}
		return string(body), nil

	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		return decodeUTF16(data[2:], binary.LittleEndian)

	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return decodeUTF16(data[2:], binary.BigEndian)
	}

	if !utf8.Valid(data) {
		return "", fmt.Errorf("%w: 请将文件另存为 UTF-8 或 UTF-16 后重新上传", ErrUnsupportedEncoding)
	}
	return string(data), nil
}

func decodeUTF16(b []byte, order binary.ByteOrder) (string, error) {
	if len(b)%2 != 0 {
		return "", fmt.Errorf("%w: UTF-16 字节数为奇数", ErrUnsupportedEncoding)
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, order.Uint16(b[i:i+2]))
	}
	return string(utf16.Decode(units)), nil
}

// ---------------------------------------------------------------- line model

// line is one source line with the terminator that followed it, kept apart so
// terminators can be reproduced exactly (a CRLF source must not come back as
// LF) while still being treated as structure rather than as text.
type line struct {
	text string
	eol  string
}

// splitLines splits on any of CRLF, LF and CR. A trailing terminator does not
// produce a final empty line, so a file ending in a newline has no phantom
// blank line.
func splitLines(s string) []line {
	var out []line
	for i := 0; i < len(s); {
		j := strings.IndexAny(s[i:], "\r\n")
		if j < 0 {
			out = append(out, line{text: s[i:]})
			break
		}
		j += i
		eol := "\n"
		if s[j] == '\r' {
			if j+1 < len(s) && s[j+1] == '\n' {
				eol = "\r\n"
			} else {
				eol = "\r"
			}
		}
		out = append(out, line{text: s[i:j], eol: eol})
		i = j + len(eol)
	}
	return out
}

// joinLines reconstructs a paragraph's text: the lines' own characters joined
// by their own terminators. The terminator of the final line is a paragraph
// boundary rather than content and is therefore dropped. Nothing else about
// the text is touched — interior CRLF stays CRLF, interior spaces stay spaces.
func joinLines(lines []line) string {
	switch len(lines) {
	case 0:
		return ""
	case 1:
		return lines[0].text
	}
	var sb strings.Builder
	for i, l := range lines {
		sb.WriteString(l.text)
		if i < len(lines)-1 {
			sb.WriteString(l.eol)
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------- txt

// parsePlain reads a plain-text upload. The source's own layout is the only
// structure available, so paragraphs are delimited by genuinely empty lines,
// and a line that begins with a 公文 层次序数 marker (第X章, 一、, （一）,
// 1.) is its own heading node — those markers are the document's own markup
// and are never guessed from punctuation.
//
// A line holding only spaces is content, not a separator: whitespace is
// verbatim source material (a 全角空格 line is meaningful in 公文 layout), so
// only a line with no characters at all separates paragraphs.
func parsePlain(name string, data []byte) (*docmodel.Document, error) {
	src, err := decodeText(data)
	if err != nil {
		return nil, err
	}
	b := &builder{format: "txt", name: name}
	for _, para := range plainParagraphs(splitLines(src)) {
		b.add(docmodel.Node{
			Kind:        para.kind,
			Level:       para.level,
			Text:        para.text,
			SourceStyle: para.style,
			SourcePage:  1,
		})
	}
	return b.doc(), nil
}

// paragraph is an assembled text block plus the classification of the line
// that started it.
type paragraph struct {
	text  string
	kind  docmodel.NodeKind
	level int
	style string
}

// plainParagraphs groups lines into paragraphs. A blank line ends a paragraph;
// so does a heading line, and so does the line right after a heading, because
// a 层次序数 heading is a line of its own in plain text and its following body
// text must not be glued onto it.
func plainParagraphs(lines []line) []paragraph {
	var out []paragraph
	var cur []line
	var curKind docmodel.NodeKind
	var curLevel int
	var curStyle string
	afterHeading := false

	flush := func() {
		if len(cur) == 0 {
			return
		}
		out = append(out, paragraph{text: joinLines(cur), kind: curKind, level: curLevel, style: curStyle})
		cur = nil
		curKind, curLevel, curStyle = docmodel.KindParagraph, 0, ""
	}

	for _, l := range lines {
		if l.text == "" {
			flush()
			afterHeading = false
			continue
		}
		kind, level, style, isHeading := classifyTextLine(l.text)
		switch {
		case isHeading:
			flush()
			cur = append(cur, l)
			curKind, curLevel, curStyle = kind, level, style
			flush()
			afterHeading = true
			continue
		case afterHeading, len(cur) == 0:
			flush()
			curKind, curLevel, curStyle = kind, level, style
			afterHeading = false
		}
		cur = append(cur, l)
	}
	flush()
	return out
}

// ---------------------------------------------------------------- markdown

// parseMarkdown reads a Markdown upload block by block, stripping the source's
// own block markup ("#", "-", ">", table pipes) so the node text is the prose
// the author wrote, while preserving every character of that prose. Inline
// emphasis markers are intentionally left in place: block structure is
// unambiguous, whereas rewriting inline spans risks altering the text, and the
// verbatim invariant outranks cosmetics.
//
// A block never spans paragraphs: each paragraph line, list item, heading and
// table becomes one node.
func parseMarkdown(name string, data []byte) (*docmodel.Document, error) {
	src, err := decodeText(data)
	if err != nil {
		return nil, err
	}
	lines := splitLines(src)
	b := &builder{format: "md", name: name}

	for i := 0; i < len(lines); {
		t := lines[i].text
		if strings.TrimSpace(t) == "" {
			i++
			continue
		}

		// Fenced code block: keep the content line by line, drop the fences.
		if fence, ok := fenceMarker(strings.TrimSpace(t)); ok {
			i++
			for i < len(lines) {
				inner := strings.TrimSpace(lines[i].text)
				if marker, ok := fenceMarker(inner); ok && marker == fence {
					i++
					break
				}
				if lines[i].text != "" {
					b.add(docmodel.Node{
						Kind: docmodel.KindParagraph, Text: lines[i].text,
						SourceStyle: "code", SourcePage: 1,
					})
				}
				i++
			}
			continue
		}

		// Pipe table: the header row followed by its delimiter row.
		if strings.HasPrefix(strings.TrimSpace(t), "|") && i+1 < len(lines) &&
			isTableDelimiter(lines[i+1].text) {
			var rows [][]string
			rows = append(rows, splitTableRow(t))
			i += 2 // consume header and delimiter
			for i < len(lines) && strings.Contains(lines[i].text, "|") &&
				strings.TrimSpace(lines[i].text) != "" {
				rows = append(rows, splitTableRow(lines[i].text))
				i++
			}
			b.add(docmodel.Node{
				Kind: docmodel.KindTable, Rows: normalizeRows(rows),
				HeaderRow: true, SourcePage: 1,
			})
			continue
		}

		if level, text, ok := mdHeading(t); ok {
			b.add(docmodel.Node{
				Kind: docmodel.KindHeading, Level: level, Text: text,
				SourceStyle: strings.Repeat("#", level), SourcePage: 1,
			})
			i++
			continue
		}

		if level, bullet, text, ok := mdListMarker(t); ok {
			b.add(docmodel.Node{
				Kind: docmodel.KindListItem, Level: level, Text: text,
				SourceStyle: bullet, SourcePage: 1,
			})
			i++
			continue
		}

		if rest, ok := mdBlockquote(t); ok {
			if rest != "" {
				b.add(docmodel.Node{Kind: docmodel.KindParagraph, Text: rest, SourcePage: 1})
			}
			i++
			continue
		}

		b.add(docmodel.Node{Kind: docmodel.KindParagraph, Text: t, SourcePage: 1})
		i++
	}
	return b.doc(), nil
}

// fenceMarker reports whether a line opens or closes a fenced code block and
// returns the fence character run (for example "```").
func fenceMarker(trimmed string) (string, bool) {
	if trimmed == "" {
		return "", false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return "", false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if n < 3 {
		return "", false
	}
	return trimmed[:n], true
}

// mdHeading parses an ATX heading ("## Title"), returning the level and the
// title text with the hashes and the closing hash run removed. The remaining
// characters are returned exactly as written.
func mdHeading(l string) (int, string, bool) {
	t := strings.TrimLeft(l, " \t")
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return 0, "", false
	}
	rest := t[n:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		// "#hashtag" is prose, not a heading.
		return 0, "", false
	}
	rest = strings.TrimLeft(rest, " \t")
	return n, trimClosingHashes(rest), true
}

// trimClosingHashes removes the optional closing "###" run of an ATX heading.
// It only cuts characters that Markdown itself defines as markup: a "#" run
// preceded by whitespace at the very end of the line.
func trimClosingHashes(s string) string {
	t := strings.TrimRight(s, " \t")
	if t == "" || !strings.HasSuffix(t, "#") {
		return s
	}
	i := len(t)
	for i > 0 && t[i-1] == '#' {
		i--
	}
	if i > 0 && (t[i-1] == ' ' || t[i-1] == '\t') {
		return strings.TrimRight(t[:i], " \t")
	}
	return s
}

// mdListMarker parses an unordered ("-", "*", "+") or ordered ("1.", "1)")
// list marker and returns the indent depth, the marker as written and the item
// text without the marker.
func mdListMarker(l string) (level int, bullet, text string, ok bool) {
	indent, rest := splitIndent(l)
	if rest == "" {
		return 0, "", "", false
	}
	switch rest[0] {
	case '-', '*', '+':
		if len(rest) > 1 && (rest[1] == ' ' || rest[1] == '\t') {
			return indent/2 + 1, string(rest[0]), strings.TrimLeft(rest[1:], " \t"), true
		}
		return 0, "", "", false
	}
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i == 0 || i > 9 || i >= len(rest) {
		return 0, "", "", false
	}
	if rest[i] != '.' && rest[i] != ')' {
		return 0, "", "", false
	}
	if i+1 >= len(rest) || (rest[i+1] != ' ' && rest[i+1] != '\t') {
		return 0, "", "", false
	}
	return indent/2 + 1, rest[:i+1], strings.TrimLeft(rest[i+1:], " \t"), true
}

// splitIndent measures leading whitespace with tabs expanded to four columns
// and returns the remaining text.
func splitIndent(l string) (int, string) {
	col := 0
	i := 0
	for i < len(l) {
		switch l[i] {
		case ' ':
			col++
		case '\t':
			col += 4 - col%4
		default:
			return col, l[i:]
		}
		i++
	}
	return col, ""
}

// mdBlockquote strips one level of ">" quoting, returning the quoted text.
func mdBlockquote(l string) (string, bool) {
	t := strings.TrimLeft(l, " \t")
	if !strings.HasPrefix(t, ">") {
		return "", false
	}
	t = t[1:]
	return strings.TrimPrefix(t, " "), true
}

// isTableDelimiter reports whether a line is a Markdown table delimiter row
// such as "|---|:--:|". Requiring a pipe keeps a thematic break ("---") from
// being mistaken for a table.
func isTableDelimiter(l string) bool {
	t := strings.TrimSpace(l)
	if t == "" {
		return false
	}
	dash, pipe := false, false
	for _, r := range t {
		switch r {
		case '-':
			dash = true
		case '|':
			pipe = true
		case ':', ' ', '\t':
		default:
			return false
		}
	}
	return dash && pipe
}

// splitTableRow splits a pipe row into trimmed cells, in order.
func splitTableRow(l string) []string {
	t := strings.TrimSpace(l)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// normalizeRows pads every row to the widest one so a consumer can index
// columns without bounds checks. Existing cells are copied unchanged: padding
// adds empty cells, it never rewrites cell text.
func normalizeRows(rows [][]string) [][]string {
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	if width == 0 {
		return rows
	}
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = make([]string, width)
		copy(out[i], r)
	}
	return out
}

// ---------------------------------------------------------------- text markers

// 公文 层次序数 markers, matched against a line with its leading indentation
// removed. These are the document's own numbering, so a match is evidence and
// not a guess.
var (
	reChapter  = regexp.MustCompile(`^第[0-9〇零一二三四五六七八九十百千]{1,6}[章节篇部分条]`)
	reCnNumber = regexp.MustCompile(`^[一二三四五六七八九十]{1,3}、`)
	reCnParen  = regexp.MustCompile(`^[（(][一二三四五六七八九十]{1,3}[）)]`)
	reNumDot   = regexp.MustCompile(`^[0-9]{1,3}[.、]`)
	reNumParen = regexp.MustCompile(`^[（(][0-9]{1,3}[）)]`)
	reBullet   = regexp.MustCompile(`^[•·▪‣◦]\s*`)
)

// classifyTextLine inspects a plain-text line for the source's own structural
// markers and returns the node classification. isHeading is false when the
// line is ordinary prose, in which case kind is KindParagraph (or
// KindListItem for a bulleted line).
func classifyTextLine(l string) (kind docmodel.NodeKind, level int, style string, isHeading bool) {
	if l == "" {
		return docmodel.KindParagraph, 0, "", false
	}
	t := strings.TrimLeft(l, " \t\u3000")
	if t == "" {
		return docmodel.KindParagraph, 0, "", false
	}
	switch {
	case reChapter.MatchString(t):
		return docmodel.KindHeading, 1, "", true
	case reCnNumber.MatchString(t):
		return docmodel.KindHeading, 1, "", true
	case reCnParen.MatchString(t):
		return docmodel.KindHeading, 2, "", true
	case reNumDot.MatchString(t):
		return docmodel.KindHeading, 3, "", true
	case reNumParen.MatchString(t):
		return docmodel.KindHeading, 4, "", true
	case reBullet.MatchString(t):
		return docmodel.KindListItem, 0, string([]rune(t)[0]), false
	}
	return docmodel.KindParagraph, 0, "", false
}
