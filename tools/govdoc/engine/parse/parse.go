// Package parse turns an uploaded document into the source model. It is the
// only stage permitted to write Node.Text, and it must copy text verbatim.
//
// # The verbatim rule
//
// Every character a reader sees in the source must appear in Node.Text
// exactly once, in the same order, as the same code points. Concretely:
//
//   - Text is never trimmed, so leading and trailing spaces survive:
//     w:t xml:space="preserve" is honoured by copying the element's
//     character data as-is (and spaces are kept even when the attribute is
//     absent, because the invariant is byte fidelity, not layout fidelity).
//   - Typography is never normalised: “ ” ‘ ’ … — and 全角空格 (U+3000)
//     pass through untouched.
//   - Adjacent runs inside one paragraph are concatenated in order; their
//     texts are never re-split, re-joined or dropped. Node.Runs keeps the
//     original run boundaries and reconstructs Node.Text exactly.
//   - Markup is not content: XML tags, Markdown "#", "-", ">", "|" markers
//     and list bullets are not part of the text. Line terminators of txt/md
//     files are structure, and are preserved verbatim *inside* a multi-line
//     block, so a CRLF source round-trips as CRLF.
//
// Only the standard library is used: docx containers are read with
// archive/zip and walked with encoding/xml.
package parse

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"opentools/tools/govdoc/engine/docmodel"
)

// ErrUnsupportedFormat is returned for a container this package cannot read.
var ErrUnsupportedFormat = errors.New("parse: 不支持的文件格式")

// ErrCorrupt is returned when a file has a supported extension but its
// content cannot be read (a .docx that is not a zip, a missing
// word/document.xml, malformed XML).
var ErrCorrupt = errors.New("parse: 文件内容损坏或不是有效的文档")

// ErrUnsupportedEncoding is returned for a text upload that is neither UTF-8
// nor BOM-marked UTF-16. GBK/GB18030 text is reported rather than silently
// mangled, because a lossy decode would break the verbatim invariant.
var ErrUnsupportedEncoding = errors.New("parse: 文本编码不受支持（仅支持 UTF-8 或带 BOM 的 UTF-16）")

// Parse decodes an uploaded document into the source model.
//
// Contract (see docs/CONTRACTS.md §2):
//   - name selects the parser by extension; data is the raw upload.
//   - Returned nodes are in source order with Order set from 0.
//   - Every node gets a stable id of the form "n0001".
//   - Text is copied verbatim: no trimming of interior whitespace, no
//     normalisation of quotes, no rejoining of split runs.
//   - Kind is classified from the source's own markup where possible
//     (Word styles, Markdown "#"), not guessed from punctuation alone.
func Parse(name string, data []byte) (*docmodel.Document, error) {
	switch ext := strings.ToLower(filepath.Ext(strings.TrimSpace(name))); ext {
	case ".docx":
		return parseDOCX(name, data)
	case ".txt":
		return parsePlain(name, data)
	case ".md", ".markdown":
		return parseMarkdown(name, data)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedFormat, name)
	}
}

// builder accumulates nodes in source order and stamps each one with its
// 0-based Order and its "n0001" id, so no parser has to think about numbering.
type builder struct {
	format string
	name   string
	nodes  []docmodel.Node
}

func (b *builder) add(n docmodel.Node) {
	n.Order = len(b.nodes)
	n.ID = fmt.Sprintf("n%04d", n.Order+1)
	b.nodes = append(b.nodes, n)
}

func (b *builder) doc() *docmodel.Document {
	docmodel.SortNodes(b.nodes)
	if b.nodes == nil {
		b.nodes = []docmodel.Node{}
	}
	return &docmodel.Document{SourceName: b.name, Format: b.format, Nodes: b.nodes}
}
