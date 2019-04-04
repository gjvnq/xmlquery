package xmlquery

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/net/html/charset"
)

// A NodeType is the type of a Node.
type NodeType uint

const (
	// DocumentNode is a document object that, as the root of the document tree,
	// provides access to the entire XML document.
	DocumentNode NodeType = iota
	// DeclarationNode is the document type declaration, indicated by the following
	// tag (for example, <!DOCTYPE...> ).
	DeclarationNode
	// ElementNode is an element (for example, <item> ).
	ElementNode
	// TextNode is the text content of a node.
	TextNode
	// CommentNode a comment (for example, <!-- my comment --> ).
	CommentNode
	// AttributeNode is an attribute of element.
	AttributeNode
)

// A Node consists of a NodeType and some Data (tag name for
// element nodes, content for text) and are part of a tree of Nodes.
type Node struct {
	Parent, FirstChild, LastChild, PrevSibling, NextSibling *Node

	Type         NodeType
	Data         string
	Prefix       string
	NamespaceURI string
	Attr         []xml.Attr

	// Application specific field that is never encoded to XML
	Info interface{}

	level int // node level in the tree
}

func xml_name2string(name xml.Name) string {
	if name.Space == "" {
		return name.Local
	}
	return name.Space + ":" + name.Local
}

func (n *Node) NthChild() int {
	if n.Parent == nil {
		return 0
	}
	ans := 0
	for child := n.Parent.FirstChild; child != nil; child = child.NextSibling {
		if child == n {
			return ans
		}
		ans++
	}
	return ans
}

func (n *Node) NthChildOfElem() int {
	if n.Parent == nil {
		return 0
	}
	check_data := n.Type == ElementNode
	ans := 0
	for child := n.Parent.FirstChild; child != nil; child = child.NextSibling {
		if child == n {
			return ans
		}
		if n.Type == child.Type {
			if !check_data {
				ans++
			} else if n.Data == child.Data {
				ans++
			}
		}
	}
	return ans
}

func (n *Node) String() string {
	switch n.Type {
	case ElementNode:
		ans := "Node{<" + n.Data
		for _, attr := range n.Attr {
			ans += " "
			ans += xml_name2string(attr.Name)
			ans += fmt.Sprintf("=%q", attr.Value)
		}
		ans += ">}"
		return ans
	case TextNode:
		return fmt.Sprintf("Node{%q}", n.Data)
	}
	return "Node{}"
}

// InnerText returns the text between the start and end tags of the object.
func (n *Node) InnerText() string {
	var output func(*bytes.Buffer, *Node)
	output = func(buf *bytes.Buffer, n *Node) {
		switch n.Type {
		case TextNode:
			buf.WriteString(n.Data)
			return
		case CommentNode:
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			output(buf, child)
		}
	}

	var buf bytes.Buffer
	output(&buf, n)
	return buf.String()
}

func outputXML(buf io.Writer, n *Node, depth int, pretty bool) {
	if n.Type == TextNode && pretty {
		space := regexp.MustCompile(`[\s\p{Zs}]+`)
		pretty_str := space.ReplaceAllString(n.Data, " ")
		if len(strings.TrimSpace(pretty_str)) != 0 {
			for i := 0; i < depth; i++ {
				buf.Write([]byte("\t"))
			}
			xml.EscapeText(buf, []byte(pretty_str))
			buf.Write([]byte("\n"))
			return
		}
	}

	if n.Type == TextNode {
		space := regexp.MustCompile(`[\s\p{Zs}]+`)
		pretty_str := space.ReplaceAllString(n.Data, " ")
		xml.EscapeText(buf, []byte(pretty_str))
		return
	}
	if pretty {
		for i := 0; i < depth; i++ {
			buf.Write([]byte("\t"))
		}
	}
	if n.Type == CommentNode {
		buf.Write([]byte("<!--"))
		buf.Write([]byte(n.Data))
		buf.Write([]byte("-->"))
		if pretty {
			buf.Write([]byte("\n"))
		}
		return
	}
	if n.Type == DeclarationNode {
		buf.Write([]byte("<?" + n.Data))
	} else {
		if n.Prefix == "" {
			buf.Write([]byte("<" + n.Data))
		} else {
			buf.Write([]byte("<" + n.Prefix + ":" + n.Data))
		}
	}

	for _, attr := range n.Attr {
		if attr.Name.Space != "" {
			buf.Write([]byte(fmt.Sprintf(` %s:%s="%s"`, attr.Name.Space, attr.Name.Local, attr.Value)))
		} else {
			buf.Write([]byte(fmt.Sprintf(` %s="%s"`, attr.Name.Local, attr.Value)))
		}
	}
	if n.Type == DeclarationNode {
		buf.Write([]byte("?>"))
	} else if n.FirstChild == nil {
		buf.Write([]byte("/>"))
		if pretty {
			buf.Write([]byte("\n"))
		}
		return
	} else {
		buf.Write([]byte(">"))
		if pretty {
			buf.Write([]byte("\n"))
		}
	}
	depth++
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		outputXML(buf, child, depth, pretty)
	}
	depth--
	if pretty {
		for i := 0; i < depth; i++ {
			buf.Write([]byte("\t"))
		}
	}
	if n.Type != DeclarationNode {
		if n.Prefix == "" {
			buf.Write([]byte(fmt.Sprintf("</%s>", n.Data)))
		} else {
			buf.Write([]byte(fmt.Sprintf("</%s:%s>", n.Prefix, n.Data)))
		}
	}
	if pretty {
		buf.Write([]byte("\n"))
	}
}

// Dereference this node from others so GC can delete them. Also fixes pointers of other nodes.
func (n *Node) DeleteMe() {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		child.DeleteMe()
		child.Parent = nil
	}
	if n.Parent != nil {
		if n == n.Parent.FirstChild {
			n.Parent.FirstChild = n.NextSibling
		}
		if n == n.Parent.LastChild {
			n.Parent.LastChild = n.PrevSibling
		}
	}
	if n.PrevSibling != nil {
		n.PrevSibling.NextSibling = n.NextSibling
	}
	if n.NextSibling != nil {
		n.NextSibling.PrevSibling = n.PrevSibling
	}
	n.Attr = nil
	n.Info = nil
	n.FirstChild = nil
	n.LastChild = nil
	n.NextSibling = nil
	n.PrevSibling = nil
	n.Parent = nil
}

// OutputXML returns the text that including tags name.
func (n *Node) OutputXML(self bool) string {
	var buf bytes.Buffer
	if self {
		outputXML(&buf, n, 0, false)
	} else {
		for n := n.FirstChild; n != nil; n = n.NextSibling {
			outputXML(&buf, n, 0, false)
		}
	}

	return buf.String()
}

// Same as OutputXML.
func (n *Node) OutputXMLToWriter(output io.Writer, pretty bool, self bool) {
	if self {
		outputXML(output, n, 0, pretty)
	} else {
		for n := n.FirstChild; n != nil; n = n.NextSibling {
			outputXML(output, n, 0, pretty)
		}
	}
}

// Returns true if the attribute existed and was altered; false if it was added.
func (n *Node) SetAttr(key, val string) bool {
	for i, attr := range n.Attr {
		if xml_name2string(attr.Name) == key {
			n.Attr[i].Value = val
			return true
		}
	}
	addAttr(n, key, val)
	return false
}

// Useful for the @class HTML attribute.
func (n *Node) AppendAttr(key, val string) {
	old := n.GetAttrWithDefault(key, "")
	if old != "" {
		old += " "
	}
	n.SetAttr(key, old+val)
}

// Returns true if the attribute existed and was deleted; false otherwise.
func (n *Node) DelAttr(key string) bool {
	index := -1
	for i, attr := range n.Attr {
		if xml_name2string(attr.Name) == key {
			index = i
			break
		}
	}
	if index >= 0 {
		n.Attr = append(n.Attr[:index], n.Attr[index+1:]...)
		return true
	}
	return false
}

func (n *Node) GetAttrWithDefault(key, empty string) string {
	ans, ok := n.GetAttr(key)
	if ok {
		return ans
	} else {
		return empty
	}
}

func (n *Node) GetAttr(key string) (string, bool) {
	for _, attr := range n.Attr {
		if xml_name2string(attr.Name) == key {
			return attr.Value, true
		}
	}
	return "", false
}

func addAttr(n *Node, key, val string) {
	var attr xml.Attr
	if i := strings.Index(key, ":"); i > 0 {
		attr = xml.Attr{
			Name:  xml.Name{Space: key[:i], Local: key[i+1:]},
			Value: val,
		}
	} else {
		attr = xml.Attr{
			Name:  xml.Name{Local: key},
			Value: val,
		}
	}

	n.Attr = append(n.Attr, attr)
}

func (n *Node) AddChild(child *Node) {
	addChild(n, child)
}

// Inserts a node between this and the old parent.
func (n *Node) Reparent(new_parent *Node) {
	if new_parent == nil {
		return
	}
	old_parent := n.Parent
	if old_parent != nil {
		if old_parent.FirstChild == n {
			old_parent.FirstChild = new_parent
		}
		if old_parent.LastChild == n {
			old_parent.LastChild = new_parent
		}
	}
	new_parent.Parent = old_parent
	new_parent.NextSibling = n.NextSibling
	new_parent.PrevSibling = n.PrevSibling
	if n.NextSibling != nil {
		n.NextSibling.PrevSibling = new_parent
	}
	if n.PrevSibling != nil {
		n.PrevSibling.NextSibling = new_parent
	}
	addChild(new_parent, n)

	n.Parent = new_parent
	n.NextSibling = nil
	n.PrevSibling = nil
}

func addChild(parent, n *Node) {
	n.Parent = parent
	if parent.FirstChild == nil {
		parent.FirstChild = n
	} else {
		parent.LastChild.NextSibling = n
		n.PrevSibling = parent.LastChild
	}

	parent.LastChild = n
}

func (n *Node) AddSibling(sibling *Node) {
	addSibling(sibling, n)
}

func addSibling(sibling, n *Node) {
	for t := sibling.NextSibling; t != nil; t = t.NextSibling {
		sibling = t
	}
	n.Parent = sibling.Parent
	sibling.NextSibling = n
	n.PrevSibling = sibling
	if sibling.Parent != nil {
		sibling.Parent.LastChild = n
	}
}

func (n *Node) AddBefore(sibling *Node) {
	if n.Parent != nil && n.Parent.FirstChild == n {
		n.Parent.FirstChild = sibling
	}
	sibling.NextSibling = n
	if n.PrevSibling != nil {
		n.PrevSibling.NextSibling = sibling
	}
	sibling.PrevSibling = n.PrevSibling
	n.PrevSibling = sibling
}

func (n *Node) AddAfter(sibling *Node) {
	if n.Parent != nil && n.Parent.LastChild == n {
		n.Parent.LastChild = sibling
	}
	sibling.PrevSibling = n
	if n.NextSibling != nil {
		n.NextSibling.PrevSibling = sibling
	}
	sibling.NextSibling = n.NextSibling
	n.NextSibling = sibling
}

// LoadURL loads the XML document from the specified URL.
func LoadURL(url string) (*Node, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parse(resp.Body)
}

func parse(r io.Reader) (*Node, error) {
	var (
		decoder      = xml.NewDecoder(r)
		doc          = &Node{Type: DocumentNode}
		space2prefix = make(map[string]string)
		level        = 0
	)
	// http://www.w3.org/XML/1998/namespace is bound by definition to the prefix xml.
	space2prefix["http://www.w3.org/XML/1998/namespace"] = "xml"
	decoder.CharsetReader = charset.NewReaderLabel
	prev := doc
	for {
		tok, err := decoder.Token()
		switch {
		case err == io.EOF:
			goto quit
		case err != nil:
			return nil, err
		}

		switch tok := tok.(type) {
		case xml.StartElement:
			if level == 0 {
				// mising XML declaration
				node := &Node{Type: DeclarationNode, Data: "xml", level: 1}
				addChild(prev, node)
				level = 1
				prev = node
			}
			// https://www.w3.org/TR/xml-names/#scoping-defaulting
			for _, att := range tok.Attr {
				if att.Name.Local == "xmlns" {
					space2prefix[att.Value] = ""
				} else if att.Name.Space == "xmlns" {
					space2prefix[att.Value] = att.Name.Local
				}
			}

			if tok.Name.Space != "" {
				if _, found := space2prefix[tok.Name.Space]; !found {
					return nil, errors.New("xmlquery: invalid XML document, namespace is missing")
				}
			}

			for i := 0; i < len(tok.Attr); i++ {
				att := &tok.Attr[i]
				if prefix, ok := space2prefix[att.Name.Space]; ok {
					att.Name.Space = prefix
				}
			}

			node := &Node{
				Type:         ElementNode,
				Data:         tok.Name.Local,
				Prefix:       space2prefix[tok.Name.Space],
				NamespaceURI: tok.Name.Space,
				Attr:         tok.Attr,
				level:        level,
			}
			//fmt.Println(fmt.Sprintf("start > %s : %d", node.Data, level))
			if level == prev.level {
				addSibling(prev, node)
			} else if level > prev.level {
				addChild(prev, node)
			} else if level < prev.level {
				for i := prev.level - level; i > 1; i-- {
					prev = prev.Parent
				}
				addSibling(prev.Parent, node)
			}
			prev = node
			level++
		case xml.EndElement:
			level--
		case xml.CharData:
			node := &Node{Type: TextNode, Data: string(tok), level: level}
			if level == prev.level {
				addSibling(prev, node)
			} else if level > prev.level {
				addChild(prev, node)
			}
		case xml.Comment:
			node := &Node{Type: CommentNode, Data: string(tok), level: level}
			if level == prev.level {
				addSibling(prev, node)
			} else if level > prev.level {
				addChild(prev, node)
			} else if level < prev.level {
				for i := prev.level - level; i > 1; i-- {
					prev = prev.Parent
				}
				addSibling(prev.Parent, node)
			}
		case xml.ProcInst: // Processing Instruction
			if prev.Type != DeclarationNode {
				level++
			}
			node := &Node{Type: DeclarationNode, Data: tok.Target, level: level}
			pairs := strings.Split(string(tok.Inst), " ")
			for _, pair := range pairs {
				pair = strings.TrimSpace(pair)
				if i := strings.Index(pair, "="); i > 0 {
					addAttr(node, pair[:i], strings.Trim(pair[i+1:], `"`))
				}
			}
			if level == prev.level {
				addSibling(prev, node)
			} else if level > prev.level {
				addChild(prev, node)
			}
			prev = node
		case xml.Directive:
		}

	}
quit:
	return doc, nil
}

// Parse returns the parse tree for the XML from the given Reader.
func Parse(r io.Reader) (*Node, error) {
	return parse(r)
}
