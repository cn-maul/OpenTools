package parse

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
)

// xnode is a minimal, namespace-aware XML element tree built straight from the
// token stream. The parser walks this tree instead of unmarshalling into typed
// structs for two reasons:
//
//   - Character data is kept exactly as encoding/xml produced it. The decoder
//     resolves XML entity and numeric character references (required: that is
//     decoding, not rewriting) and this tree does nothing else — no trimming,
//     no whitespace collapsing, no re-encoding.
//   - Elements are matched by local name, so a document that declares its
//     namespaces with different prefixes or none at all still parses.
type xnode struct {
	name     xml.Name
	attrs    []xml.Attr
	text     []byte
	children []*xnode
}

var errBadXML = errors.New("parse: XML 结构无效")

// parseXMLTree decodes one XML document into an xnode tree.
func parseXMLTree(data []byte) (*xnode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var stack []*xnode
	var root *xnode
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xnode{name: t.Name, attrs: t.Attr}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errBadXML
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.text = append(top.text, t...)
			}
		}
	}
	if root == nil {
		return nil, errBadXML
	}
	return root, nil
}

func (n *xnode) local() string {
	if n == nil {
		return ""
	}
	return n.name.Local
}

// rawText is the element's own character data, verbatim.
func (n *xnode) rawText() string {
	if n == nil {
		return ""
	}
	return string(n.text)
}

// child returns the first direct child with the given local name.
func (n *xnode) child(local string) *xnode {
	if n == nil {
		return nil
	}
	for _, c := range n.children {
		if c.name.Local == local {
			return c
		}
	}
	return nil
}

// kids returns every direct child with the given local name, in order.
func (n *xnode) kids(local string) []*xnode {
	if n == nil {
		return nil
	}
	var out []*xnode
	for _, c := range n.children {
		if c.name.Local == local {
			out = append(out, c)
		}
	}
	return out
}

// attr returns the value of the first attribute whose local name matches,
// regardless of namespace prefix (w:val, r:embed, r:id, ...).
func (n *xnode) attr(local string) (string, bool) {
	if n == nil {
		return "", false
	}
	for _, a := range n.attrs {
		if a.Name.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// find returns the first descendant (or the element itself) with the local
// name, in document order.
func (n *xnode) find(local string) *xnode {
	if n == nil {
		return nil
	}
	if n.name.Local == local {
		return n
	}
	for _, c := range n.children {
		if got := c.find(local); got != nil {
			return got
		}
	}
	return nil
}

// findAll returns every descendant (excluding the element itself) with the
// given local name, in document order.
func (n *xnode) findAll(local string) []*xnode {
	var out []*xnode
	var rec func(*xnode)
	rec = func(e *xnode) {
		for _, c := range e.children {
			if c.name.Local == local {
				out = append(out, c)
			}
			rec(c)
		}
	}
	if n != nil {
		rec(n)
	}
	return out
}

// isOff reports whether a toggle element is explicitly switched off, as in
// <w:b w:val="0"/> or <w:tblHeader w:val="false"/>.
func isOff(el *xnode) bool {
	v, ok := el.attr("val")
	if !ok {
		return false
	}
	switch v {
	case "0", "false", "off":
		return true
	}
	return false
}

// on reports whether a toggle child element is present and not switched off.
func on(parent *xnode, local string) bool {
	el := parent.child(local)
	if el == nil {
		return false
	}
	return !isOff(el)
}
