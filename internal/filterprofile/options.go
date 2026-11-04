package filterprofile

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"

	"openloc/internal/httputil"
	"openloc/internal/parser"
)

// The filter formats: a file format, with .pot under "po". Android string
// resources, XLIFF, Markdown, DOCX, PPTX, PDF and Apple .strings have no
// filter profiles.
const (
	FormatHTML = "html"
	FormatJSON = "json_i18n"
	FormatPO   = "po"
	FormatTXT  = "txt"
	FormatXLSX = "xlsx"
	FormatXML  = "xml"
)

// Formats lists the filter formats in display order.
var Formats = []string{FormatHTML, FormatXLSX, FormatJSON, FormatPO, FormatTXT, FormatXML}

// FormatOf returns the filter format of a file format; false when the file
// format has no filter profiles.
func FormatOf(fileFormat string) (string, bool) {
	if fileFormat == "pot" {
		return FormatPO, true
	}
	if slices.Contains(Formats, fileFormat) {
		return fileFormat, true
	}
	return "", false
}

// inlineHTMLFormats are the filter formats whose strings may hold HTML.
var inlineHTMLFormats = []string{FormatJSON, FormatPO, FormatTXT, FormatXLSX}

// Options are a filter profile's import options. Only the fields of the
// profile's format are set: InlineHTML for JSON, PO, TXT and Excel, XLSX for
// Excel, HTML for HTML and XML for generic XML.
type Options struct {
	// InlineHTML: tags in strings become protected tags; false reads them as
	// plain text.
	InlineHTML *bool               `json:"inline_html,omitempty"`
	XLSX       *XLSXRules          `json:"xlsx,omitempty"`
	HTML       *parser.HTMLOptions `json:"html,omitempty"`
	XML        *parser.XMLOptions  `json:"xml,omitempty"`
}

// XLSXRules choose what an Excel workbook without language codes in row 1
// translates, by name and letter, so one profile fits every workbook of the
// same shape. No rules: the upload asks.
type XLSXRules struct {
	// Sheets are the sheet names to translate; none means every visible sheet.
	Sheets []string `json:"sheets"`
	// HeaderRow: row 1 names the columns and is not translated.
	HeaderRow bool `json:"header_row"`
	// Columns are column letters ("B"); none means every text cell.
	Columns []string `json:"columns"`
	// TargetColumns write each target language into its own column next to
	// the one source column in Columns, keeping the source text; none writes
	// each translation back into its source cell.
	TargetColumns []parser.XLSXTargetColumn `json:"target_columns"`
}

// Empty reports rules that choose nothing, so the upload asks.
func (r XLSXRules) Empty() bool {
	return len(r.Sheets) == 0 && !r.HeaderRow && len(r.Columns) == 0 && len(r.TargetColumns) == 0
}

// Caps on the lists a profile holds.
const (
	maxSheets     = 100
	maxColumns    = 200
	maxSheetName  = 31 // Excel's own limit
	maxXLSXColumn = 16384
	// maxNames caps each HTML or XML name list (elements, attributes).
	maxNames   = 200
	maxNameLen = 100
)

// normalized returns o for format: the format's fields, defaults filled in,
// lists trimmed and de-duplicated. It is what a profile stores and returns.
func normalized(format string, o Options) Options {
	var out Options
	if slices.Contains(inlineHTMLFormats, format) {
		on := true
		if o.InlineHTML != nil {
			on = *o.InlineHTML
		}
		out.InlineHTML = &on
	}
	if format == FormatXLSX {
		r := XLSXRules{Sheets: []string{}, Columns: []string{}, TargetColumns: []parser.XLSXTargetColumn{}}
		if o.XLSX != nil {
			r.HeaderRow = o.XLSX.HeaderRow
			r.Sheets = uniqueTrimmed(o.XLSX.Sheets, false)
			r.Columns = uniqueTrimmed(o.XLSX.Columns, true)
			r.TargetColumns = parser.NormalizeXLSXTargets(o.XLSX.TargetColumns)
		}
		out.XLSX = &r
	}
	if format == FormatHTML {
		h := parser.DefaultHTMLOptions()
		if o.HTML != nil {
			h = *o.HTML
		}
		attrs := make(map[string][]string, len(h.Attributes))
		for elem, names := range h.Attributes {
			elem = strings.ToLower(strings.TrimSpace(elem))
			if elem == "" {
				continue
			}
			if names := uniqueLower(append(attrs[elem], names...)); len(names) > 0 {
				attrs[elem] = names
			}
		}
		h.Attributes = attrs
		out.HTML = &h
	}
	if format == FormatXML {
		x := parser.DefaultXMLOptions()
		if o.XML != nil {
			x = *o.XML
		}
		x.InlineElements = uniqueTrimmed(x.InlineElements, false)
		x.RefElements = uniqueTrimmed(x.RefElements, false)
		x.RefAttributes = uniqueTrimmed(x.RefAttributes, false)
		x.SkipElements = uniqueTrimmed(x.SkipElements, false)
		marks := []parser.XMLAttrValue{}
		seen := map[parser.XMLAttrValue]bool{}
		for _, m := range x.NoTranslate {
			m.Name = strings.TrimSpace(m.Name)
			if m.Name != "" && !seen[m] {
				seen[m] = true
				marks = append(marks, m)
			}
		}
		x.NoTranslate = marks
		out.XML = &x
	}
	return out
}

// validate checks normalized options.
func validate(o Options) error {
	if err := validateHTML(o.HTML); err != nil {
		return err
	}
	if err := validateXML(o.XML); err != nil {
		return err
	}
	if o.XLSX == nil {
		return nil
	}
	if len(o.XLSX.Sheets) > maxSheets {
		return httputil.NewClientError(fmt.Sprintf("A filter profile can list at most %d sheets.", maxSheets))
	}
	for _, s := range o.XLSX.Sheets {
		if s == "" || len([]rune(s)) > maxSheetName {
			return httputil.NewClientError(fmt.Sprintf("%q is not a sheet name: 1 to %d characters.", s, maxSheetName))
		}
	}
	if len(o.XLSX.Columns) > maxColumns {
		return httputil.NewClientError(fmt.Sprintf("A filter profile can list at most %d columns.", maxColumns))
	}
	for _, c := range o.XLSX.Columns {
		if n, err := excelize.ColumnNameToNumber(c); err != nil || n < 1 || n > maxXLSXColumn {
			return httputil.NewClientError(fmt.Sprintf("%q is not a column.", c))
		}
	}
	if err := parser.ValidateXLSXTargets(o.XLSX.Columns, o.XLSX.TargetColumns); err != nil {
		return httputil.NewClientError(err.Error())
	}
	return nil
}

// uniqueTrimmed trims each value, drops empties and repeats, and upper-cases
// when upper is set.
func uniqueTrimmed(in []string, upper bool) []string {
	out := []string{}
	seen := make(map[string]bool, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if upper {
			v = strings.ToUpper(v)
		}
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// uniqueLower is uniqueTrimmed for HTML names, which are case-insensitive.
func uniqueLower(in []string) []string {
	return uniqueTrimmed(lowerAll(in), false)
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strings.ToLower(v)
	}
	return out
}

// checkSizes refuses lists longer than the caps before anything walks them:
// normalizing and validating are per-item work on a body of up to 1 MB.
func checkSizes(o Options) error {
	lists := [][]string{}
	if o.XLSX != nil {
		if len(o.XLSX.Sheets) > maxSheets {
			return httputil.NewClientError(fmt.Sprintf("A filter profile can list at most %d sheets.", maxSheets))
		}
		if len(o.XLSX.Columns) > maxColumns {
			return httputil.NewClientError(fmt.Sprintf("A filter profile can list at most %d columns.", maxColumns))
		}
		if len(o.XLSX.TargetColumns) > parser.MaxXLSXTargetColumns {
			return httputil.NewClientError(fmt.Sprintf("At most %d languages can have their own column.", parser.MaxXLSXTargetColumns))
		}
	}
	if o.HTML != nil {
		if len(o.HTML.Attributes) > maxNames {
			return httputil.NewClientError(fmt.Sprintf("A filter profile can name at most %d elements.", maxNames))
		}
		for _, attrs := range o.HTML.Attributes {
			lists = append(lists, attrs)
		}
	}
	if o.XML != nil {
		lists = append(lists, o.XML.InlineElements, o.XML.RefElements, o.XML.RefAttributes, o.XML.SkipElements)
		if len(o.XML.NoTranslate) > maxNames {
			return httputil.NewClientError(fmt.Sprintf("A filter profile can list at most %d do-not-translate markers.", maxNames))
		}
	}
	for _, l := range lists {
		if len(l) > maxNames {
			return httputil.NewClientError(fmt.Sprintf("A filter profile can list at most %d names in a list.", maxNames))
		}
	}
	return nil
}

func validateHTML(h *parser.HTMLOptions) error {
	if h == nil {
		return nil
	}
	if len(h.Attributes) > maxNames {
		return httputil.NewClientError(fmt.Sprintf("A filter profile can name at most %d elements.", maxNames))
	}
	for elem, attrs := range h.Attributes {
		if err := checkNames("element", []string{elem}); err != nil {
			return err
		}
		if err := checkNames("attribute", attrs); err != nil {
			return err
		}
	}
	if err := h.Validate(); err != nil {
		return httputil.NewClientError("Only text attributes can be translated: alt, title, placeholder, abbr, label, summary, aria-label and other text aria-* attributes, and value on an input. Attributes of meta, link, base, html and head cannot.")
	}
	return nil
}

func validateXML(x *parser.XMLOptions) error {
	if x == nil {
		return nil
	}
	for kind, names := range map[string][]string{
		"element":   slices.Concat(x.InlineElements, x.RefElements, x.SkipElements),
		"attribute": x.RefAttributes,
	} {
		if err := checkNames(kind, names); err != nil {
			return err
		}
	}
	if len(x.NoTranslate) > maxNames {
		return httputil.NewClientError(fmt.Sprintf("A filter profile can list at most %d do-not-translate markers.", maxNames))
	}
	for _, m := range x.NoTranslate {
		if err := checkNames("attribute", []string{m.Name}); err != nil {
			return err
		}
		// An empty value matches every element without the attribute.
		if m.Value == "" {
			return httputil.NewClientError(fmt.Sprintf("The do-not-translate marker %q needs a value, such as %s=no.", m.Name, m.Name))
		}
		if len(m.Value) > maxNameLen {
			return httputil.NewClientError(fmt.Sprintf("A do-not-translate value can be at most %d characters.", maxNameLen))
		}
	}
	return nil
}

// checkNames refuses a list longer than maxNames or a name that is not an
// XML-style name (letters, digits, '-', '_', '.', ':'; not starting with a
// digit, '-' or '.').
func checkNames(kind string, names []string) error {
	if len(names) > maxNames {
		return httputil.NewClientError(fmt.Sprintf("A filter profile can list at most %d %s names.", maxNames, kind))
	}
	for _, n := range names {
		if !validName(n) {
			return httputil.NewClientError(fmt.Sprintf("%q is not an %s name.", n, kind))
		}
	}
	return nil
}

func validName(n string) bool {
	if n == "" || len(n) > maxNameLen {
		return false
	}
	for i, r := range n {
		switch {
		case unicode.IsLetter(r) || r == '_' || r == ':':
		case i > 0 && (unicode.IsDigit(r) || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return true
}
