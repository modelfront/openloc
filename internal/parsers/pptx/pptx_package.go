package pptx

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"openloc/internal/parsers/common"
	"path"
	"strings"
)

// pptxPackage is an opened .pptx: the zip entries, the parts read so far and
// the relationships between them. Part names follow OPC rules: they compare
// case-insensitively, and a relationship target is percent-decoded and
// resolved against the directory of the part that names it.
type pptxPackage struct {
	content []byte
	zr      *zip.Reader
	// files maps a lowercased entry name to its entry.
	files  map[string]*zip.File
	budget *common.UnzipBudget
	docs   map[string]*common.XMLDoc
	// nodesLeft and attrsLeft are how many DOM nodes the parts still to
	// read may build, and how many attributes those nodes may keep.
	nodesLeft int
	attrsLeft int
	rels      map[string][]pptxRel
	// relByID indexes each part's relationships by id.
	relByID map[string]map[string]pptxRel
}

// pptxRel is one relationship of a part. target is the entry name it
// resolves to, or "" for an external target.
type pptxRel struct {
	id     string
	typ    string
	target string
}

// Relationship types, matched by suffix so the transitional and the Strict
// OOXML namespaces both work.
const (
	pptxRelOfficeDocument = "/officeDocument"
	pptxRelSlide          = "/slide"
	pptxRelNotesSlide     = "/notesSlide"
	pptxRelDiagramData    = "/diagramData"
	pptxRelDiagramDrawing = "/diagramDrawing"
)

// openPPTX refuses legacy and encrypted files and opens the zip. A legacy
// .ppt and an encrypted .pptx are both OLE compound files; the stream names
// inside tell them apart.
func openPPTX(content []byte) (*pptxPackage, error) {
	if bytes.HasPrefix(content, common.CFBSignature) && common.CFBHasStream(content, "PowerPoint Document") {
		return nil, common.ErrPPTXLegacy
	}
	zr, err := common.OpenOOXMLZip(content, pptxErrors)
	if err != nil {
		return nil, err
	}
	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		key := strings.ToLower(f.Name)
		if _, dup := files[key]; dup {
			return nil, fmt.Errorf("%w: duplicate part %s", common.ErrPPTXMalformed, f.Name)
		}
		// Export copies every entry raw, so one whose local header is broken
		// is refused here rather than failing every download later.
		if _, err := f.OpenRaw(); err != nil {
			return nil, fmt.Errorf("%w: part %s: %v", common.ErrPPTXMalformed, f.Name, err)
		}
		files[key] = f
	}
	return &pptxPackage{
		content:   content,
		zr:        zr,
		files:     files,
		budget:    common.NewUnzipBudget(maxPPTXUnzipped, common.ErrPPTXTooLarge),
		nodesLeft: maxPPTXElements,
		attrsLeft: maxPPTXAttrs,
		docs:      map[string]*common.XMLDoc{},
		rels:      map[string][]pptxRel{},
		relByID:   map[string]map[string]pptxRel{},
	}, nil
}

// A deck's XML is read into memory as a DOM, so two budgets bound it. The
// parts read (slides, notes, SmartArt, relationships) may unzip to
// maxPPTXUnzipped bytes in total; media is never read. And they may build
// maxPPTXElements DOM nodes: a small zip can unzip to millions of tiny
// elements within the byte budget. The heaviest real decks seen — drawn with
// vector shapes — use about 2,000 nodes per slide once shape geometry is
// skipped.
const (
	maxPPTXUnzipped = 64 << 20
	maxPPTXElements = 500_000
	// maxPPTXTagBytes caps one start tag. The longest seen in real decks is
	// about 1 KB; long alt text fits many times over.
	maxPPTXTagBytes = 64 << 10
	// maxPPTXAttrs caps the attributes the built nodes keep in total. Real
	// decks keep about one per node, so this matches the node budget's
	// headroom; tags just under maxPPTXTagBytes cannot add up past it.
	maxPPTXAttrs = 1_000_000
)

// pptxSkip are elements whose content the parser never reads: shape
// geometry, fills and styles, which in a vector-drawn deck hold most of the
// elements.
var pptxSkip = map[string]bool{"spPr": true, "grpSpPr": true, "style": true, "blipFill": true}

// parsePPTXPart parses one XML part. Tests replace it with a parser that
// panics, to prove every entry point recovers.
var parsePPTXPart = common.ParseXMLDocWith

// entry returns the zip entry for a part name, matched case-insensitively.
func (p *pptxPackage) entry(name string) (*zip.File, bool) {
	f, ok := p.files[strings.ToLower(strings.TrimPrefix(name, "/"))]
	return f, ok
}

// doc reads and parses a part once. The part's declared size is taken from
// the budget before anything is decompressed.
func (p *pptxPackage) doc(name string) (*common.XMLDoc, error) {
	f, ok := p.entry(name)
	if !ok {
		return nil, fmt.Errorf("%w: missing part %s", common.ErrPPTXMalformed, name)
	}
	if d, ok := p.docs[f.Name]; ok {
		return d, nil
	}
	if err := p.budget.Reserve(f); err != nil {
		return nil, err
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: part %s: %v", common.ErrPPTXMalformed, f.Name, err)
	}
	b, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: part %s: %v", common.ErrPPTXMalformed, f.Name, err)
	}
	if p.nodesLeft <= 0 || p.attrsLeft <= 0 {
		return nil, fmt.Errorf("%w: more than %d elements or %d attributes", common.ErrPPTXTooLarge, maxPPTXElements, maxPPTXAttrs)
	}
	if !common.StartTagsWithin(b, maxPPTXTagBytes) {
		return nil, fmt.Errorf("%w: part %s has a start tag over %d bytes", common.ErrPPTXTooLarge, f.Name, maxPPTXTagBytes)
	}
	d, err := parsePPTXPart(b, common.XMLParseOptions{Skip: pptxSkip, MaxNodes: p.nodesLeft, MaxAttrs: p.attrsLeft})
	if errors.Is(err, common.ErrTooManyElements) {
		return nil, fmt.Errorf("%w: more than %d elements or %d attributes", common.ErrPPTXTooLarge, maxPPTXElements, maxPPTXAttrs)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: part %s: %v", common.ErrPPTXMalformed, f.Name, err)
	}
	p.nodesLeft -= d.Nodes
	p.attrsLeft -= d.Attrs
	p.docs[f.Name] = d
	return d, nil
}

// relsOf returns the relationships of a part, in the order its .rels part
// lists them; "" names the package itself. A part without a .rels part has
// none.
func (p *pptxPackage) relsOf(part string) ([]pptxRel, error) {
	if rels, ok := p.rels[part]; ok {
		return rels, nil
	}
	relsPart := "_rels/.rels"
	if part != "" {
		relsPart = path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
	}
	var rels []pptxRel
	byID := map[string]pptxRel{}
	if f, ok := p.entry(relsPart); ok {
		d, err := p.doc(relsPart)
		if err != nil {
			return nil, err
		}
		for _, n := range d.Root.Children {
			if n.Name != "Relationship" {
				continue
			}
			rel := pptxRel{id: n.Attr("Id"), typ: n.Attr("Type")}
			if n.Attr("TargetMode") != "External" {
				rel.target = resolvePartName(part, n.Attr("Target"))
			}
			rels = append(rels, rel)
			if _, dup := byID[rel.id]; !dup {
				byID[rel.id] = rel
			}
		}
		// Nothing reads a .rels part again; drop its DOM.
		delete(p.docs, f.Name)
	}
	p.rels[part] = rels
	p.relByID[part] = byID
	return rels, nil
}

// resolvePartName resolves a relationship target against the part that
// names it.
func resolvePartName(source, target string) string {
	if t, err := url.PathUnescape(target); err == nil {
		target = t
	}
	if strings.HasPrefix(target, "/") {
		return path.Clean(target)[1:]
	}
	return strings.TrimPrefix(path.Join(path.Dir(source), target), "/")
}

// related returns the entry name of part's relationship with the given id,
// requiring the type suffix typ and an existing target.
func (p *pptxPackage) related(part, id, typ string) (string, error) {
	if _, err := p.relsOf(part); err != nil {
		return "", err
	}
	r, ok := p.relByID[part][id]
	if !ok || !strings.HasSuffix(r.typ, typ) || r.target == "" {
		return "", fmt.Errorf("%w: %s has no %s relationship %q", common.ErrPPTXMalformed, part, strings.TrimPrefix(typ, "/"), id)
	}
	f, ok := p.entry(r.target)
	if !ok {
		return "", fmt.Errorf("%w: %s points at missing part %s", common.ErrPPTXMalformed, part, r.target)
	}
	return f.Name, nil
}

// relatedByType returns the entry names of every relationship of part whose
// type ends in typ, in .rels order.
func (p *pptxPackage) relatedByType(part, typ string) ([]string, error) {
	rels, err := p.relsOf(part)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, r := range rels {
		if !strings.HasSuffix(r.typ, typ) || r.target == "" {
			continue
		}
		f, ok := p.entry(r.target)
		if !ok {
			return nil, fmt.Errorf("%w: %s points at missing part %s", common.ErrPPTXMalformed, part, r.target)
		}
		names = append(names, f.Name)
	}
	return names, nil
}

// slideParts returns the slide parts in the order PowerPoint shows them:
// the presentation's slide id list, not the part names.
func (p *pptxPackage) slideParts() ([]string, error) {
	docs, err := p.relatedByType("", pptxRelOfficeDocument)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("%w: no presentation part", common.ErrPPTXMalformed)
	}
	pres := docs[0]
	d, err := p.doc(pres)
	if err != nil {
		return nil, err
	}
	// A Word or Excel file renamed to .pptx has a main part too.
	if d.Root.Name != "presentation" {
		return nil, fmt.Errorf("%w: main part %s is not a presentation", common.ErrPPTXMalformed, pres)
	}
	var slides []string
	if lst := xmlChild(d.Root, "sldIdLst"); lst != nil {
		for _, id := range lst.Children {
			if id.Name != "sldId" {
				continue
			}
			slide, err := p.related(pres, relAttr(id, "id"), pptxRelSlide)
			if err != nil {
				return nil, err
			}
			slides = append(slides, slide)
		}
	}
	// The walk never reads the presentation part again; drop its DOM.
	delete(p.docs, pres)
	return slides, nil
}

// rewrite returns the package with the changed parts replaced. Every other
// entry is copied raw, so media is never decompressed and the bytes stay as
// they were. With nothing changed the original comes back as it is.
func (p *pptxPackage) rewrite(changed map[string][]byte) ([]byte, error) {
	if len(changed) == 0 {
		return p.content, nil
	}
	var buf bytes.Buffer
	buf.Grow(len(p.content))
	w := zip.NewWriter(&buf)
	for _, f := range p.zr.File {
		data, ok := changed[f.Name]
		if !ok {
			if err := w.Copy(f); err != nil {
				return nil, fmt.Errorf("%w: copy %s: %v", common.ErrPPTXMalformed, f.Name, err)
			}
			continue
		}
		fw, err := w.CreateHeader(&zip.FileHeader{
			Name:          f.Name,
			Comment:       f.Comment,
			Method:        zip.Deflate,
			Modified:      f.Modified,
			ModifiedTime:  f.ModifiedTime,
			ModifiedDate:  f.ModifiedDate,
			ExternalAttrs: f.ExternalAttrs,
		})
		if err != nil {
			return nil, fmt.Errorf("pptx: write %s: %w", f.Name, err)
		}
		if _, err := fw.Write(data); err != nil {
			return nil, fmt.Errorf("pptx: write %s: %w", f.Name, err)
		}
	}
	if err := w.SetComment(p.zr.Comment); err != nil {
		return nil, fmt.Errorf("pptx: zip comment: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("pptx: close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// xmlChild returns n's first child element with the given local name.
func xmlChild(n *common.XMLNode, name string) *common.XMLNode {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// xmlPath follows a chain of first children by local name.
func xmlPath(n *common.XMLNode, names ...string) *common.XMLNode {
	for _, name := range names {
		n = xmlChild(n, name)
	}
	return n
}

// relAttr returns the namespaced (r:) attribute with the given local name.
// Several OOXML elements carry both a plain id and an r:id, so the namespace
// is what picks the relationship id.
func relAttr(n *common.XMLNode, local string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == local && a.Name.Space != "" {
			return a.Value
		}
	}
	return ""
}
