package parser

// XMLOptions configures how the generic XML parser reads a vocabulary. The
// zero value is not the default: use DefaultXMLOptions, or leave XML.Options
// nil.
type XMLOptions struct {
	// InlineElements are inline markup wherever they appear: they become tag
	// chips inside a segment rather than splitting it. Elements not named
	// here are still inline inside mixed content.
	InlineElements []string `json:"inline_elements"`
	// NoTranslate marks an element and its subtree as not for translation
	// when one of its attributes has the given value.
	NoTranslate []XMLAttrValue `json:"no_translate"`
	// RefElements and RefAttributes mark a cross-reference: an element with
	// one of these names, or with one of these attributes set to a non-empty
	// value, is one placeholder chip whose text stays out of the segment.
	RefElements   []string `json:"ref_elements"`
	RefAttributes []string `json:"ref_attributes"`
	// SkipElements are left out of translation like a NoTranslate match: no
	// segment, and their bytes are kept as is on export.
	SkipElements []string `json:"skip_elements"`
}

// XMLAttrValue is one attribute name and value.
type XMLAttrValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DefaultXMLOptions returns the built-in rules: HTML-like inline markup,
// translate="no" and translatable="false", and the ST4 <meta-ref> and
// linkid cross-references.
func DefaultXMLOptions() XMLOptions {
	return XMLOptions{
		InlineElements: []string{
			"b", "i", "em", "strong", "u", "sup", "sub", "br",
			"span", "a", "code", "tt", "small", "big",
		},
		NoTranslate: []XMLAttrValue{
			{Name: "translate", Value: "no"},
			{Name: "translatable", Value: "false"},
		},
		RefElements:   []string{"meta-ref"},
		RefAttributes: []string{"linkid"},
		SkipElements:  []string{},
	}
}

// xmlRules is XMLOptions as lookup sets, built once per document.
type xmlRules struct {
	inline        map[string]bool
	noTranslate   []XMLAttrValue
	refElements   map[string]bool
	refAttributes []string
	skip          map[string]bool
}

var defaultXMLRules = DefaultXMLOptions().rules()

// xmlRulesFor returns the rules for opts; nil is the defaults.
func xmlRulesFor(opts *XMLOptions) *xmlRules {
	if opts == nil {
		return defaultXMLRules
	}
	return opts.rules()
}

func (o XMLOptions) rules() *xmlRules {
	return &xmlRules{
		inline:        stringSet(o.InlineElements),
		noTranslate:   namedAttrs(o.NoTranslate),
		refElements:   stringSet(o.RefElements),
		refAttributes: o.RefAttributes,
		skip:          stringSet(o.SkipElements),
	}
}

// namedAttrs drops entries without an attribute name, which would otherwise
// match every element (a missing attribute reads as "").
func namedAttrs(attrs []XMLAttrValue) []XMLAttrValue {
	out := make([]XMLAttrValue, 0, len(attrs))
	for _, a := range attrs {
		if a.Name != "" {
			out = append(out, a)
		}
	}
	return out
}

func stringSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// orDefault lets a document parsed without options use the defaults.
func (r *xmlRules) orDefault() *xmlRules {
	if r == nil {
		return defaultXMLRules
	}
	return r
}

// isTranslatable reports whether n is open to translation: not a skipped
// element and no no-translate marker.
func (r *xmlRules) isTranslatable(n *xmlNode) bool {
	r = r.orDefault()
	if r.skip[n.name] {
		return false
	}
	for _, m := range r.noTranslate {
		if n.attr(m.Name) == m.Value {
			return false
		}
	}
	return true
}

// isInline reports whether n is inline markup by name.
func (r *xmlRules) isInline(n *xmlNode) bool {
	return r.orDefault().inline[n.name]
}

// isReferencePlaceholder reports whether n is a cross-reference whose text is
// generated from another node rather than authored here.
//
// ST4's `<meta-ref linkid="datanode_…">Eco</meta-ref>` renders the value of the
// node it points at — a node that is itself a translatable segment in the same
// file. The text sitting inside the reference is a cached rendering, so
// translating it would duplicate the term (and invite the two copies to
// disagree). The CMS re-resolves it on publish.
//
// Such an element becomes a single non-editable placeholder chip: its content
// stays out of the segment, out of the word count, and out of TM lookup, which
// is what lets our source text line up with a TM built by a tool that models it
// the same way (`<ph>` in TMX).
//
// Distinct from isTranslatable: `translate="no"` skips the subtree entirely and
// emits no chip. A reference still needs one — the translator must see that
// something belongs there, and export needs an anchor to restore it.
func (r *xmlRules) isReferencePlaceholder(n *xmlNode) bool {
	r = r.orDefault()
	// Attribute first: it generalises past any single vocabulary's element name.
	for _, a := range r.refAttributes {
		if n.attr(a) != "" {
			return true
		}
	}
	return r.refElements[n.name]
}
