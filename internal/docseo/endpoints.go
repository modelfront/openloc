package docseo

import (
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"openloc/internal/publicapi"
)

type endpoint struct {
	id, method, path string
	order            int
	op               *openapi3.Operation
	params           openapi3.Parameters
}

type endpointGroup struct {
	tag       string
	endpoints []endpoint
}

// endpointGroups lists every operation not marked x-hidden, grouped by its
// first tag in the spec's tag order and sorted by x-order, as the app does.
func endpointGroups(spec *publicapi.Spec) []endpointGroup {
	doc := spec.Doc
	var groups []endpointGroup
	index := map[string]int{}
	add := func(tag string) int {
		if i, ok := index[tag]; ok {
			return i
		}
		index[tag] = len(groups)
		groups = append(groups, endpointGroup{tag: tag})
		return index[tag]
	}
	for _, t := range doc.Tags {
		add(t.Name)
	}
	for _, path := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Value(path)
		for method, op := range item.Operations() {
			if publicapi.XHidden(op) {
				continue
			}
			tag := "Other"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			i := add(tag)
			groups[i].endpoints = append(groups[i].endpoints, endpoint{
				id:     op.OperationID,
				method: strings.ToUpper(method),
				path:   path,
				order:  publicapi.XOrder(op),
				op:     op,
				params: append(append(openapi3.Parameters{}, item.Parameters...), op.Parameters...),
			})
		}
	}
	out := groups[:0]
	for _, g := range groups {
		if len(g.endpoints) == 0 {
			continue
		}
		sort.SliceStable(g.endpoints, func(i, j int) bool {
			a, b := g.endpoints[i], g.endpoints[j]
			if a.order != b.order {
				return a.order < b.order
			}
			return a.id < b.id
		})
		out = append(out, g)
	}
	return out
}

func (site *Site) endpointsPage() Page {
	var b htmlBuilder
	b.tag("h1", endpointsTitle)
	b.tag("p", endpointsSummary)
	b.endpointList(site.endpoints)
	return Page{
		Title:       title(endpointsTitle),
		Description: endpointsSummary,
		Canonical:   site.canonical("/" + endpointsSection),
		Body:        b.html(),
	}
}

func (site *Site) endpointPage(id string) (Page, bool) {
	for _, g := range site.endpoints {
		for _, e := range g.endpoints {
			if e.id != id {
				continue
			}
			var b htmlBuilder
			b.tag("h1", e.op.Summary)
			b.raw("<p><code>")
			b.text(e.method + " /api/v1" + e.path)
			b.raw("</code></p>\n")
			if err := b.markdown(e.op.Description, site.href); err != nil {
				return Page{}, false
			}
			if roles := publicapi.XRoles(e.op); len(roles) > 0 {
				b.tag("h2", "Who may call it")
				b.tag("p", strings.Join(roles, ", "))
			}
			if err := b.parameters(e.params, site.href); err != nil {
				return Page{}, false
			}
			if err := b.responses(e.op.Responses, site.href); err != nil {
				return Page{}, false
			}
			return Page{
				Title:       title(e.op.Summary, site.Label),
				Description: e.method + " /api/v1" + e.path + ": " + e.op.Summary,
				Canonical:   site.canonical("/" + endpointsSection + "/" + e.id),
				Body:        b.html(),
			}, true
		}
	}
	return Page{}, false
}

func (b *htmlBuilder) endpointList(groups []endpointGroup) {
	for _, g := range groups {
		b.tag("h3", g.tag)
		b.raw("<ul>\n")
		for _, e := range g.endpoints {
			b.raw(`<li><a href="`)
			b.text("/" + endpointsSection + "/" + e.id)
			b.raw(`">`)
			b.text(e.op.Summary)
			b.raw("</a> <code>")
			b.text(e.method + " /api/v1" + e.path)
			b.raw("</code></li>\n")
		}
		b.raw("</ul>\n")
	}
}

func (b *htmlBuilder) parameters(params openapi3.Parameters, href func(string) string) error {
	if len(params) == 0 {
		return nil
	}
	b.tag("h2", "Parameters")
	b.raw("<table>\n<thead><tr><th>Name</th><th>In</th><th>Required</th><th>Description</th></tr></thead>\n<tbody>\n")
	for _, ref := range params {
		p := ref.Value
		if p == nil {
			continue
		}
		required := "no"
		if p.Required {
			required = "yes"
		}
		b.raw("<tr><td><code>")
		b.text(p.Name)
		b.raw("</code></td><td>")
		b.text(p.In)
		b.raw("</td><td>")
		b.text(required)
		b.raw("</td><td>")
		if err := b.markdown(p.Description, href); err != nil {
			return err
		}
		b.raw("</td></tr>\n")
	}
	b.raw("</tbody>\n</table>\n")
	return nil
}

func (b *htmlBuilder) responses(rs *openapi3.Responses, href func(string) string) error {
	if rs == nil || rs.Len() == 0 {
		return nil
	}
	m := rs.Map()
	codes := make([]string, 0, len(m))
	for code := range m {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	b.tag("h2", "Responses")
	b.raw("<dl>\n")
	for _, code := range codes {
		b.tag("dt", code)
		b.raw("<dd>")
		if r := m[code].Value; r != nil && r.Description != nil {
			if err := b.markdown(*r.Description, href); err != nil {
				return err
			}
		}
		b.raw("</dd>\n")
	}
	b.raw("</dl>\n")
	return nil
}
