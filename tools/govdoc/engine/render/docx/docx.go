// Package docx writes a rendered 公文 to a .docx (OOXML) package.
//
// Every size, font, spacing and indent it emits must come from the supplied
// gbstandard.Spec — this package carries no typography constants of its own.
// The output is a real Word document so the user can keep editing it, which a
// 公文 must remain possible to do.
//
// # Structure
//
// The package is split into a hand-written OOXML emitter (ooxml.go, which
// turns gbstandard values into w: elements) and this file, which assembles the
// package parts and zips them. There is no third-party OOXML dependency: the
// parts are built with encoding/xml escaping and written with archive/zip, so
// the only thing deciding a font size is gbstandard.Spec.
//
// # Verbatim invariant
//
// A block's Text is emitted exactly once, inside its own <w:t>, and nothing in
// this package rewrites, trims or normalises it. TestRenderVerbatim proves the
// concatenation of a paragraph's w:t values equals the source text.
package docx

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"path"
	"sort"
	"strings"

	"opentools/tools/govdoc/engine/docmodel"
	"opentools/tools/govdoc/engine/gbstandard"
)

// ErrNotImplemented is retained for callers that still probe for the old
// placeholder; Render no longer returns it.
var ErrNotImplemented = errors.New("docx: 渲染尚未实现")

// relationship is one entry of a .rels part.
type relationship struct {
	id     string
	typ    string
	target string
}

// embeddedImage is one stored media part plus its display geometry.
type embeddedImage struct {
	partName string // "media/image1.png"
	relID    string
	name     string
	cx, cy   int    // EMU
	data     []byte // raw payload, written to the package
}

// render carries the per-document state: the spec being applied, the resolved
// relatonship list and the media collected while the body is serialized.
type render struct {
	spec gbstandard.Spec

	images    []embeddedImage
	imageByID map[string]embeddedImage
	imageExts map[string]string // extension → content type, for [Content_Types]

	rels      []relationship
	nextRelID int
	nextDraw  int
}

// Render produces the bytes of a .docx file.
//
// Contract:
//   - Page size and margins come from spec.Page (mm → twips/pt).
//   - Each OutBlock is emitted as a paragraph whose properties come from
//     spec.Styles[block.Role]; no font size may be hard-coded here.
//   - Indentation uses character units (w:firstLineChars) so "空二字" scales
//     with the font size as the standard intends, with point-valued twins for
//     readers that ignore the character-unit attributes.
//   - block.AutoNumber is prepended to the paragraph text as its own run; the
//     source text itself is reproduced unchanged.
//   - Tables render as real Word tables; images as embedded media; the
//     separator rules as paragraph borders.
//   - Page numbers are drawn as the standard requires (四号宋体, 一字线 on
//     each side).
func Render(d *docmodel.Doc, spec gbstandard.Spec) ([]byte, error) {
	if d == nil {
		return nil, errors.New("docx: 渲染文档为空")
	}

	r := &render{
		spec:      spec,
		imageByID: map[string]embeddedImage{},
		imageExts: map[string]string{},
	}

	// Relationship ids are allocated up front in a fixed order, so two renders
	// of the same document produce identical bytes.
	r.addRel(relStyles, "styles.xml")
	r.addRel(relSettings, "settings.xml")
	footerRel := r.addRel(relFooter, "footer1.xml")

	body, err := r.bodyXML(d)
	if err != nil {
		return nil, err
	}

	document := xmlDecl +
		`<w:document xmlns:w="` + nsW + `" xmlns:r="` + nsR + `" xmlns:wp="` + nsWP + `" xmlns:a="` + nsA + `">` +
		"<w:body>" + body + r.sectPrXML(footerRel) + "</w:body></w:document>"

	parts := []struct {
		name string
		data string
	}{
		{"[Content_Types].xml", contentTypesXML(r.imageExts)},
		{"_rels/.rels", packageRelsXML()},
		{"word/document.xml", document},
		{"word/_rels/document.xml.rels", documentRelsXML(r.rels)},
		{"word/styles.xml", r.stylesXML()},
		{"word/settings.xml", settingsXML()},
		{"word/footer1.xml", r.footerXML()},
		{"docProps/core.xml", corePropsXML(d.Meta.Title, "GovDoc")},
		{"docProps/app.xml", appPropsXML()},
	}
	// The media parts carry binary payloads, so zipParts writes them from
	// r.images rather than through the textual part table above.
	return r.zipParts(parts)
}

// zipParts writes the textual parts plus any collected media into a zip
// container, then re-opens it to prove the result is a readable package.
func (r *render) zipParts(parts []struct {
	name string
	data string
}) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, part := range parts {
		w, err := zw.Create(part.name)
		if err != nil {
			return nil, fmt.Errorf("docx: 写入 %s: %w", part.name, err)
		}
		if _, err := w.Write([]byte(part.data)); err != nil {
			return nil, fmt.Errorf("docx: 写入 %s: %w", part.name, err)
		}
	}
	for _, img := range r.images {
		w, err := zw.Create("word/" + img.partName)
		if err != nil {
			return nil, fmt.Errorf("docx: 写入 %s: %w", img.partName, err)
		}
		if _, err := w.Write(img.data); err != nil {
			return nil, fmt.Errorf("docx: 写入 %s: %w", img.partName, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("docx: 关闭压缩包: %w", err)
	}

	out := buf.Bytes()
	if err := checkPackage(out); err != nil {
		return nil, err
	}
	return out, nil
}

// checkPackage re-opens the produced bytes with archive/zip and verifies the
// mandatory parts are present, so a corrupt package fails here rather than in
// the user's Word.
func checkPackage(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("docx: 生成的包无法读取: %w", err)
	}
	have := make(map[string]bool, len(zr.File))
	for _, f := range zr.File {
		have[f.Name] = true
	}
	for _, want := range []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"word/document.xml",
		"word/_rels/document.xml.rels",
		"word/styles.xml",
		"word/settings.xml",
		"docProps/core.xml",
		"docProps/app.xml",
	} {
		if !have[want] {
			return fmt.Errorf("docx: 生成的包缺少 %s", want)
		}
	}
	return nil
}

// ---------------------------------------------------------------- helpers

func (r *render) addRel(typ, target string) string {
	r.nextRelID++
	id := fmt.Sprintf("rId%d", r.nextRelID)
	r.rels = append(r.rels, relationship{id: id, typ: typ, target: target})
	return id
}

func (r *render) nextDrawingID() int {
	r.nextDraw++
	return r.nextDraw
}

// bodyXML serializes every block in order.
func (r *render) bodyXML(d *docmodel.Doc) (string, error) {
	var b strings.Builder
	for _, blk := range d.Blocks {
		// MustStyle falls back to 正文 for a role the spec omits, so a
		// partially valid spec renders instead of panicking.
		st := r.spec.MustStyle(blk.Role)
		// OutBlock.SpaceBeforeLines is a document-level 空N行 offset on top of
		// the role's own style (GB/T 9704-2012 §4.3.2 moves 发文机关标志 with
		// 行文方向). Both are hundredths of a line, so they add directly.
		st.SpaceBeforeLines += blk.SpaceBeforeLines

		switch {
		case len(blk.Rows) > 0:
			tbl := r.tableXML(blk, st)
			if tbl == "" {
				// Rows present but every row empty: fall back to the text form
				// rather than emitting an invalid empty table.
				b.WriteString(r.paragraphXML(blk, st))
				continue
			}
			b.WriteString(tbl)
			// A table must be followed by a paragraph, otherwise Word merges it
			// with the next table or drops the trailing block.
			b.WriteString(r.emptyParagraph(st))
		case blk.Image != nil:
			// A block may carry both a picture and prose (an 附件 caption, for
			// instance). The text is emitted as its own paragraph first so it
			// still appears verbatim; only then is the drawing placed.
			if blk.Text != "" {
				b.WriteString(r.paragraphXML(blk, st))
			}
			para, err := r.imageParagraphXML(blk, st)
			if err != nil {
				return "", err
			}
			b.WriteString(para)
		default:
			b.WriteString(r.paragraphXML(blk, st))
		}
	}
	return b.String(), nil
}

// emptyParagraph is the spacer Word needs after a table. It has no text, so no
// first-line-indent correction applies.
func (r *render) emptyParagraph(st gbstandard.ParaStyle) string {
	return "<w:p>" + r.paraProps(st, false, "", "") + "</w:p>"
}

// addImage stores an image payload and returns its media part name, geometry
// and relationship id. Images are scaled to the 版心 width, preserving aspect
// ratio, and capped by the 版心 height; duplicates are stored once.
func (r *render) addImage(ref *docmodel.ImageRef) (embeddedImage, error) {
	if ref == nil || len(ref.Data) == 0 {
		return embeddedImage{}, errors.New("docx: 图片节点缺少图像数据")
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(ref.Data))
	if err != nil {
		return embeddedImage{}, fmt.Errorf("docx: 无法解析图片 %q: %w", ref.Name, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return embeddedImage{}, fmt.Errorf("docx: 图片 %q 尺寸无效", ref.Name)
	}

	ext, contentType, ok := imageType(format, ref.MimeType)
	if !ok {
		return embeddedImage{}, fmt.Errorf("docx: 不支持的图片格式 %q (%s)", format, ref.MimeType)
	}

	cx, cy := r.fitToTextColumn(cfg.Width, cfg.Height)

	// Images with identical bytes are stored once, whatever their node names.
	key := fmt.Sprintf("%s|%d|%d|%x", ext, cfg.Width, cfg.Height, sha256.Sum256(ref.Data))
	if img, ok := r.imageByID[key]; ok {
		return img, nil
	}

	r.imageExts[ext] = contentType
	name := fmt.Sprintf("image%d.%s", len(r.images)+1, ext)
	partName := "media/" + name
	relID := r.addRel(relImage, partName)

	img := embeddedImage{
		partName: partName,
		relID:    relID,
		name:     imageName(ref.Name, name),
		cx:       cx,
		cy:       cy,
		data:     ref.Data,
	}
	r.images = append(r.images, img)
	r.imageByID[key] = img
	return img, nil
}

// fitToTextColumn scales a pixel image to the 版心 width (标准 156mm) and caps
// the result at the 版心 height, keeping the aspect ratio. Both bounds come
// from spec.Page, so an unusual page setup moves the picture with it.
func (r *render) fitToTextColumn(w, h int) (int, int) {
	maxCX := int(math.Round(r.spec.Page.TextWidthMM() * 36000))
	maxCY := int(math.Round(r.spec.Page.TextHeightMM() * 36000))
	if maxCX <= 0 || maxCY <= 0 {
		return 0, 0
	}
	// Render at 96dpi, which is what Word assumes for an unscaled bitmap.
	cx := w * 9525
	cy := h * 9525
	if cy > maxCY {
		cx = cx * maxCY / cy
		cy = maxCY
	}
	if cx > maxCX {
		cy = cy * maxCX / cx
		cx = maxCX
	}
	if cx <= 0 {
		cx = 1
	}
	if cy <= 0 {
		cy = 1
	}
	return cx, cy
}

// imageType maps a decoded format (or a declared MIME type) to the file
// extension and content type OOXML expects.
func imageType(format, mime string) (ext, contentType string, ok bool) {
	switch format {
	case "png":
		return "png", "image/png", true
	case "jpeg":
		return "jpeg", "image/jpeg", true
	case "gif":
		return "gif", "image/gif", true
	}
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return "png", "image/png", true
	case "image/jpeg", "image/jpg":
		return "jpeg", "image/jpeg", true
	case "image/gif":
		return "gif", "image/gif", true
	}
	return "", "", false
}

// imageName derives a non-empty, Word-safe picture name.
func imageName(name, fallback string) string {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if base == "" || base == "." || base == "/" {
		return fallback
	}
	return base
}

// sortStrings keeps the deterministic ordering of [Content_Types] defaults.
func sortStrings(s []string) { sort.Strings(s) }
