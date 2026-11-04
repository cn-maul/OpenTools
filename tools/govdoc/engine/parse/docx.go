package parse

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"opentools/tools/govdoc/engine/docmodel"
)

// maxZipEntry caps how much of a single zip entry is read into memory. It
// defends against a decompression bomb while staying far above any realistic
// picture or document part.
const maxZipEntry = 128 << 20 // 128 MiB

// parseDOCX reads an OOXML package and walks word/document.xml in document
// order, emitting one node per paragraph text span, table, picture and
// explicit page break.
func parseDOCX(name string, data []byte) (*docmodel.Document, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	entries := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		entries[path.Clean(strings.TrimPrefix(f.Name, "/"))] = f
	}

	docFile, ok := entries["word/document.xml"]
	if !ok {
		return nil, fmt.Errorf("%w: 缺少 word/document.xml", ErrCorrupt)
	}
	docXML, err := readZipEntry(docFile)
	if err != nil {
		return nil, err
	}
	root, err := parseXMLTree(docXML)
	if err != nil {
		return nil, fmt.Errorf("%w: word/document.xml: %v", ErrCorrupt, err)
	}
	body := root.find("body")
	if body == nil {
		return nil, fmt.Errorf("%w: word/document.xml 缺少 w:body", ErrCorrupt)
	}

	p := &docxParser{
		entries: entries,
		rels:    loadRelationships(entries),
		images:  map[string]*docmodel.ImageRef{},
		page:    1,
		b:       &builder{format: "docx", name: name},
	}
	if err := p.walkBlocks(body); err != nil {
		return nil, err
	}
	return p.b.doc(), nil
}

// readZipEntry reads one zip entry, refusing entries that are implausibly
// large even after decompression.
func readZipEntry(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxZipEntry {
		return nil, fmt.Errorf("%w: 条目 %s 过大", ErrCorrupt, f.Name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	defer rc.Close()
	buf, err := io.ReadAll(io.LimitReader(rc, maxZipEntry+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if len(buf) > maxZipEntry {
		return nil, fmt.Errorf("%w: 条目 %s 过大", ErrCorrupt, f.Name)
	}
	return buf, nil
}

// loadRelationships maps a part directory to its r:id → Target map. Failure to
// read a relationship part is not fatal: the picture is then emitted without
// bytes, but every paragraph still round-trips verbatim.
func loadRelationships(entries map[string]*zip.File) map[string]map[string]string {
	out := map[string]map[string]string{}
	for name, f := range entries {
		if !strings.HasSuffix(name, ".rels") {
			continue
		}
		raw, err := readZipEntry(f)
		if err != nil {
			continue
		}
		root, err := parseXMLTree(raw)
		if err != nil {
			continue
		}
		m := map[string]string{}
		for _, rel := range root.findAll("Relationship") {
			id, _ := rel.attr("Id")
			target, _ := rel.attr("Target")
			if id != "" && target != "" {
				m[id] = target
			}
		}
		// "word/_rels/document.xml.rels" documents the "word" part.
		out[path.Dir(path.Dir(name))] = m
	}
	return out
}

type docxParser struct {
	entries map[string]*zip.File
	// rels maps a part directory ("word", "word/charts", ...) to its
	// relationship id → target map.
	rels   map[string]map[string]string
	images map[string]*docmodel.ImageRef
	// page is the 1-based page the next block starts on, advanced by the
	// w:lastRenderedPageBreak markers Word leaves from its layout pass.
	page int
	b    *builder
}

// walkBlocks emits block-level content in order, descending into content
// controls and ignoring the sectPr that closes the body.
func (p *docxParser) walkBlocks(el *xnode) error {
	for _, c := range el.children {
		switch c.name.Local {
		case "p":
			p.emitParagraph(c)
		case "tbl":
			p.emitTable(c)
		case "sdt":
			if content := c.child("sdtContent"); content != nil {
				if err := p.walkBlocks(content); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// emitParagraph turns one w:p into nodes, preserving the paragraph's internal
// order. Kind comes from the paragraph's own markup: a Heading/标题 style or an
// outline level makes it a heading, numPr makes it a list item.
//
// A paragraph yields exactly as many nodes as it has payloads: its text spans
// split at page breaks and pictures. A paragraph with no characters, no
// picture and no page break yields nothing — it holds no source content, and
// emitting an empty-text content node could never be verified.
func (p *docxParser) emitParagraph(para *xnode) {
	style, _ := para.child("pPr").child("pStyle").attr("val")
	kind, level := classifyParagraph(para, style)

	// The page the paragraph's text starts on. Between text pieces the page
	// advances only when a page break occurs, so text after a mid-paragraph
	// break is attributed to the page it actually printed on.
	page := p.advancePage(para)

	for _, pc := range paragraphPieces(para) {
		switch {
		case pc.breakPage:
			p.b.add(docmodel.Node{
				Kind:        docmodel.KindPageBreak,
				SourceStyle: style,
				SourcePage:  page,
			})
			// An explicit page break also advances the page counter, so a
			// document without Word's layout markers still gets usable
			// SourcePage values.
			p.page++
			page = p.page
		case pc.ref != "":
			img := p.loadImage(pc.ref)
			// An image node has no prose, but VerifyVerbatim compares output
			// blocks to Node.Text by id, so the node carries its source file
			// name (taken from the package relationship, never invented) and
			// the renderer echoes it back on the block it emits.
			p.b.add(docmodel.Node{
				Kind:       docmodel.KindImage,
				Text:       img.Name,
				Image:      img,
				SourcePage: page,
			})
		default:
			p.b.add(docmodel.Node{
				Kind:        kind,
				Level:       level,
				Text:        joinRuns(pc.runs),
				Runs:        pc.runs,
				SourceStyle: style,
				SourcePage:  page,
			})
		}
	}
}

// advancePage updates the running page counter for one paragraph and returns
// the page its content starts on. Word records w:lastRenderedPageBreak inside
// the run whose text begins a new page, so a marker that appears before the
// paragraph has produced any text means the paragraph itself starts the page;
// a marker after text means the page turns partway through.
func (p *docxParser) advancePage(para *xnode) int {
	start := p.page
	textBefore := false
	for _, r := range para.kids("r") {
		for _, c := range r.children {
			if c.name.Local == "lastRenderedPageBreak" {
				if !textBefore {
					start = p.page + 1
				}
				p.page++
				continue
			}
			if c.name.Local == "t" && len(c.text) > 0 {
				textBefore = true
			}
		}
	}
	return start
}

// classifyParagraph reads the paragraph's own structural markup.
func classifyParagraph(para *xnode, style string) (docmodel.NodeKind, int) {
	if lv := headingLevelOf(style); lv > 0 {
		return docmodel.KindHeading, lv
	}
	if lv := outlineLevelOf(para); lv > 0 {
		return docmodel.KindHeading, lv
	}
	if isListParagraph(para, style) {
		return docmodel.KindListItem, 0
	}
	return docmodel.KindParagraph, 0
}

// emitTable turns one w:tbl into a KindTable node. Cell text is verbatim; a
// cell holding several paragraphs keeps its internal line breaks.
func (p *docxParser) emitTable(tbl *xnode) {
	var rows [][]string
	header := false
	for i, tr := range tbl.kids("tr") {
		if i == 0 {
			if hp := tr.child("trPr"); hp != nil && on(hp, "tblHeader") {
				header = true
			}
		}
		var row []string
		for _, tc := range tr.kids("tc") {
			row = append(row, cellText(tc))
		}
		rows = append(rows, row)
	}
	p.b.add(docmodel.Node{
		Kind:       docmodel.KindTable,
		Rows:       rows,
		HeaderRow:  header,
		SourcePage: p.page,
	})
}

// cellText concatenates a table cell's block content with the source's own
// line terminators, so multi-paragraph cells keep their shape.
func cellText(tc *xnode) string {
	var parts []string
	var rec func(*xnode)
	rec = func(el *xnode) {
		for _, c := range el.children {
			switch c.name.Local {
			case "p":
				parts = append(parts, paragraphText(c))
			case "tbl":
				for _, tr := range c.kids("tr") {
					var cells []string
					for _, inner := range tr.kids("tc") {
						cells = append(cells, cellText(inner))
					}
					parts = append(parts, strings.Join(cells, "\t"))
				}
			case "sdt":
				if content := c.child("sdtContent"); content != nil {
					rec(content)
				}
			}
		}
	}
	rec(tc)
	return strings.Join(parts, "\n")
}

// loadImage resolves a relationship id to the picture bytes in word/media.
// The result is cached per relationship so a repeated picture is read once.
func (p *docxParser) loadImage(relID string) *docmodel.ImageRef {
	if img, ok := p.images[relID]; ok {
		return img
	}
	img := &docmodel.ImageRef{}
	p.images[relID] = img

	target := p.rels["word"][relID]
	if target == "" {
		return img
	}
	full := resolveTarget("word", target)
	img.Name = path.Base(full)
	img.MimeType = mimeByExt(path.Ext(full))
	if f, ok := p.entries[full]; ok {
		if raw, err := readZipEntry(f); err == nil {
			img.Data = raw
		}
	}
	return img
}

// resolveTarget turns a relationship Target (absolute "/word/media/x.png" or
// relative "media/x.png") into a normalized package path.
func resolveTarget(baseDir, target string) string {
	if strings.HasPrefix(target, "/") {
		return path.Clean(strings.TrimPrefix(target, "/"))
	}
	return path.Clean(path.Join(baseDir, target))
}

func mimeByExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".webp":
		return "image/webp"
	case ".emf":
		return "image/x-emf"
	case ".wmf":
		return "image/x-wmf"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

// ------------------------------------------------------------------ text walk

// walkRuns calls fn for every w:r reachable from el without entering a nested
// block, in document order. Hyperlinks, tracked insertions, content controls
// and simple fields all contain ordinary runs and are traversed; deleted text
// (w:del) is skipped because it is revision history, not document content.
func walkRuns(el *xnode, fn func(*xnode)) {
	for _, c := range el.children {
		switch c.name.Local {
		case "r":
			fn(c)
		case "hyperlink", "ins", "smartTag", "sdt", "sdtContent", "fldSimple", "dir", "bdo":
			walkRuns(c, fn)
		}
	}
}

// paragraphRuns lists the paragraph's text spans in order, as one Run per w:r.
// It is the cell-text and styling view; page-breaking w:br becomes "\n" here
// because a table cell has no page concept.
func paragraphRuns(para *xnode) []docmodel.Run {
	var runs []docmodel.Run
	walkRuns(para, func(r *xnode) { runs = append(runs, runOf(r)) })
	return runs
}

// paragraphText is the paragraph's complete text, verbatim.
func paragraphText(para *xnode) string { return joinRuns(paragraphRuns(para)) }

// runOf converts one w:r into a styled span. w:delText is deliberately
// ignored: it is revision history for text the author deleted, while w:t holds
// the document's current text.
func runOf(r *xnode) docmodel.Run {
	run := runStyle(r)
	var sb strings.Builder
	for _, c := range r.children {
		switch c.name.Local {
		case "t":
			// Copied exactly: entity and numeric character references were
			// already resolved by the XML decoder, and nothing else is
			// touched. xml:space="preserve" needs no special handling because
			// the value is never trimmed — keeping the spaces unconditionally
			// is a superset of what the attribute asks for and never changes a
			// character.
			sb.Write(c.text)
		case "noBreakHyphen":
			sb.WriteByte('-')
		case "softHyphen":
			sb.WriteString("\u00ad")
		case "tab":
			sb.WriteByte('\t')
		case "br", "cr":
			// A run-internal line break is a character of the source text.
			sb.WriteByte('\n')
		}
	}
	run.Text = sb.String()
	return run
}

// joinRuns concatenates run text with no separator and no normalisation. This
// single function defines Node.Text for docx paragraph text, so the verbatim
// invariant is enforced by construction: it can only ever append.
func joinRuns(runs []docmodel.Run) string {
	switch len(runs) {
	case 0:
		return ""
	case 1:
		return runs[0].Text
	}
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(r.Text)
	}
	return sb.String()
}

// ------------------------------------------------------------------ pieces

// piece is one addressable payload inside a paragraph.
type piece struct {
	// runs are the styled spans of a text piece.
	runs []docmodel.Run
	// ref is the relationship id of a picture piece.
	ref string
	// breakPage marks an explicit page break.
	breakPage bool
}

// paragraphPieces splits a paragraph into ordered payloads: text, pictures and
// page breaks, exactly as they occur. Text is never reordered relative to the
// pictures and breaks around it, so the concatenation of the pieces in order
// is the paragraph's original content.
func paragraphPieces(para *xnode) []piece {
	var pb pieceBuilder
	walkRuns(para, pb.run)
	return pb.out
}

type pieceBuilder struct {
	out  []piece
	open bool // whether out's last element is a text piece still accepting runs
}

// textRun appends one styled span to the open text piece, opening a new one if
// the previous payload was a picture or a page break.
func (b *pieceBuilder) textRun(run docmodel.Run) {
	if run.Text == "" {
		return
	}
	if !b.open {
		b.out = append(b.out, piece{})
		b.open = true
	}
	last := &b.out[len(b.out)-1]
	last.runs = append(last.runs, run)
}

func (b *pieceBuilder) image(ref string) {
	b.open = false
	b.out = append(b.out, piece{ref: ref})
}

func (b *pieceBuilder) pageBreak() {
	b.open = false
	b.out = append(b.out, piece{breakPage: true})
}

// run walks one w:r and appends its content to the builder, splitting text at
// page breaks and extracting drawings. Nothing is dropped and nothing is
// reordered.
func (b *pieceBuilder) run(r *xnode) {
	style := runStyle(r)
	var sb strings.Builder
	flush := func() {
		if sb.Len() == 0 {
			return
		}
		run := style
		run.Text = sb.String()
		b.textRun(run)
		sb.Reset()
	}
	for _, c := range r.children {
		switch c.name.Local {
		case "t":
			sb.Write(c.text)
		case "noBreakHyphen":
			sb.WriteByte('-')
		case "softHyphen":
			sb.WriteString("\u00ad")
		case "tab":
			sb.WriteByte('\t')
		case "br", "cr":
			if t, ok := c.attr("type"); ok && t == "page" {
				flush()
				b.pageBreak()
			} else {
				sb.WriteByte('\n')
			}
		case "drawing", "pict", "object":
			flush()
			for _, ref := range imageRefsOf(c) {
				b.image(ref)
			}
		}
	}
	flush()
}

// runStyle extracts a run's inline formatting.
func runStyle(r *xnode) docmodel.Run {
	var run docmodel.Run
	rPr := r.child("rPr")
	if rPr == nil {
		return run
	}
	if el := rPr.child("b"); el != nil {
		run.Bold = !isOff(el)
	} else if el := rPr.child("bCs"); el != nil {
		run.Bold = !isOff(el)
	}
	if el := rPr.child("i"); el != nil {
		run.Italic = !isOff(el)
	} else if el := rPr.child("iCs"); el != nil {
		run.Italic = !isOff(el)
	}
	if rf := rPr.child("rFonts"); rf != nil {
		if f, ok := rf.attr("eastAsia"); ok {
			run.SourceFont = f
		} else if f, ok := rf.attr("ascii"); ok {
			run.SourceFont = f
		}
	}
	return run
}

// imageRefsOf lists every relationship id referenced by a drawing, picture or
// embedded object, in document order.
func imageRefsOf(el *xnode) []string {
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, blip := range el.findAll("blip") {
		if v, ok := blip.attr("embed"); ok {
			add(v)
		}
	}
	for _, idata := range el.findAll("imagedata") {
		if v, ok := idata.attr("id"); ok {
			add(v)
		}
	}
	return out
}

// ------------------------------------------------------------------ markup

// headingLevelOf reads a Word paragraph style name: "Heading 1" / "heading1" /
// "标题 1" / "标题1" all mean level 1; 0 means "not a heading style".
func headingLevelOf(style string) int {
	s := strings.ToLower(strings.TrimSpace(style))
	if s == "" {
		return 0
	}
	s = strings.ReplaceAll(s, "heading", "heading ")
	s = strings.ReplaceAll(s, "标题", "heading ")
	s = strings.Join(strings.Fields(s), " ")
	if !strings.HasPrefix(s, "heading") {
		return 0
	}
	rest := strings.TrimSpace(strings.TrimPrefix(s, "heading"))
	if rest == "" {
		// Bare "heading" is level 1 in Word's own naming scheme.
		return 1
	}
	lv, err := strconv.Atoi(rest)
	if err != nil || lv < 1 {
		return 0
	}
	if lv > 9 {
		lv = 9
	}
	return lv
}

// outlineLevelOf reads w:pPr/w:outlineLvl. Word stores it 0-based while
// Node.Level is 1-based; outlineLvl 9 means "body text", not a heading.
func outlineLevelOf(para *xnode) int {
	el := para.child("pPr").child("outlineLvl")
	if el == nil {
		return 0
	}
	v, ok := el.attr("val")
	if !ok {
		return 0
	}
	lv, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || lv < 0 || lv >= 9 {
		return 0
	}
	return lv + 1
}

// isListParagraph reports whether the paragraph is a list entry per its own
// markup. Two spellings occur in real files: an inline w:numPr on the
// paragraph, and a paragraph style that is itself a list style. Word applies
// numbering through the style's own w:numPr, so "List Number" /
// "List Paragraph" / "列表段落" / "项目符号" paragraphs are list items even
// though they carry no numPr of their own.
func isListParagraph(para *xnode, style string) bool {
	if para.child("pPr").child("numPr") != nil {
		return true
	}
	return isListStyle(style)
}

// isListStyle recognises Word's built-in list style names, in English and in
// Chinese, without matching anything else.
func isListStyle(style string) bool {
	s := strings.ToLower(strings.TrimSpace(style))
	if s == "" {
		return false
	}
	s = strings.Join(strings.Fields(s), "")
	for _, marker := range []string{
		"listnumber", "listbullet", "listparagraph", "list", "列表", "项目符号", "编号",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}
