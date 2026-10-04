package site

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"path"
	"strings"

	"github.com/yuin/goldmark/ast"
)

// MermaidURL is the only external resource the site loads, and only on
// pages with diagrams. The integrity hash pins the exact file.
const (
	MermaidURL       = "https://cdn.jsdelivr.net/npm/mermaid@11.17.2/dist/mermaid.min.js"
	mermaidIntegrity = "sha384-EOXBFmc3gx5mb+vn0vPvvGqACToJD24hhacX5Yx+8NUUQrHIle/Qi5Bg9o3zKwW2"
)

var (
	//go:embed assets/style.css
	styleCSS []byte
	//go:embed assets/site.js
	siteJS []byte
	//go:embed assets/page.html
	pageHTML string

	pageTemplate = template.Must(template.New("page").Parse(pageHTML))
)

// navGroup is one sidebar section. Items hold pages rather than links
// because hrefs are relative to the page being rendered.
type navGroup struct {
	Label string
	Note  string
	Items []navEntry
}

type navEntry struct {
	Label    string
	Page     *page
	Children []navEntry
}

type navData struct {
	Home    *page
	Groups  []navGroup
	Archive *page
}

// buildNav mirrors the "By audience" section of docs/README.md: each bold
// paragraph starts a group and the page links in the list that follows are
// its entries. Pages in the same directory as a linked README (the ADRs)
// nest under it, pages linked nowhere go to "More pages", and archived pages
// are reachable only from the Archive page.
func (b *builder) buildNav() (navData, error) {
	home := b.bySrc[homeSource]
	nav := navData{Home: home, Archive: b.bySrc[archiveDir+"/README.md"]}
	inNav := map[*page]bool{home: true, nav.Archive: true}

	var section ast.Node
	for n := home.doc.FirstChild(); n != nil; n = n.NextSibling() {
		if h, ok := n.(*ast.Heading); ok && h.Level == 2 && plainText(h, home.source) == navHeading {
			section = n.NextSibling()
			break
		}
	}
	for n := section; n != nil; n = n.NextSibling() {
		if h, ok := n.(*ast.Heading); ok && h.Level <= 2 {
			break
		}
		switch n := n.(type) {
		case *ast.Paragraph:
			if em, ok := n.FirstChild().(*ast.Emphasis); ok && em.Level == 2 {
				label, note, _ := strings.Cut(plainText(em, home.source), " (")
				nav.Groups = append(nav.Groups, navGroup{Label: label, Note: strings.TrimSuffix(note, ")")})
			}
		case *ast.List:
			if len(nav.Groups) == 0 {
				continue
			}
			group := &nav.Groups[len(nav.Groups)-1]
			_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
				if link, ok := c.(*ast.Link); ok && entering {
					if target := home.links[link]; target != nil && !inNav[target] {
						group.Items = append(group.Items, navEntry{Label: plainText(link, home.source), Page: target})
						inNav[target] = true
					}
				}
				return ast.WalkContinue, nil
			})
		}
	}
	if len(nav.Groups) == 0 {
		return nav, fmt.Errorf("%s: no audience groups found under %q", homeSource, navHeading)
	}

	for g := range nav.Groups {
		for i, entry := range nav.Groups[g].Items {
			if path.Base(entry.Page.src) != "README.md" || strings.HasPrefix(entry.Page.src, archiveDir+"/") {
				continue
			}
			dir := path.Dir(entry.Page.src)
			for _, p := range b.pages {
				if path.Dir(p.src) == dir && !inNav[p] {
					nav.Groups[g].Items[i].Children = append(nav.Groups[g].Items[i].Children, navEntry{Label: p.title, Page: p})
					inNav[p] = true
				}
			}
		}
	}

	more := navGroup{Label: "More pages"}
	for _, p := range b.pages {
		if !inNav[p] && !strings.HasPrefix(p.src, archiveDir+"/") {
			more.Items = append(more.Items, navEntry{Label: p.title, Page: p})
		}
	}
	if len(more.Items) > 0 {
		nav.Groups = append(nav.Groups, more)
	}
	return nav, nil
}

type link struct {
	Label    string
	Href     string
	Current  bool
	Children []link
}

type group struct {
	Label string
	Note  string
	Links []link
}

type pageView struct {
	Title        string
	Root         string
	Home         link
	Groups       []group
	Archive      *link
	Archived     bool
	TOC          []heading
	Body         template.HTML
	SourcePath   string
	SourceURL    string
	Diagram      bool
	MermaidURL   string
	MermaidHash  string
	Assets       map[string]string
	IsHome       bool
	GeneratorCmd string
}

func (b *builder) layout(p *page, nav navData, body []byte) ([]byte, error) {
	toLink := func(label string, target *page) link {
		return link{Label: label, Href: relPath(p.out, target.out), Current: target == p}
	}
	view := pageView{
		Title:        p.title,
		Root:         rootPrefix(p.out),
		Home:         toLink("Home", nav.Home),
		Archived:     p.archived,
		Body:         template.HTML(body),
		SourcePath:   p.src,
		SourceURL:    b.repoLink("blob", p.src),
		Diagram:      p.diagram,
		MermaidURL:   MermaidURL,
		MermaidHash:  mermaidIntegrity,
		IsHome:       p == nav.Home,
		GeneratorCmd: "make docs-site",
		Assets:       b.assets,
	}
	for _, g := range nav.Groups {
		vg := group{Label: g.Label, Note: g.Note}
		for _, e := range g.Items {
			l := toLink(e.Label, e.Page)
			for _, c := range e.Children {
				l.Children = append(l.Children, toLink(c.Label, c.Page))
			}
			vg.Links = append(vg.Links, l)
		}
		view.Groups = append(view.Groups, vg)
	}
	if nav.Archive != nil {
		l := toLink("Archive", nav.Archive)
		l.Current = p == nav.Archive || p.archived
		view.Archive = &l
	}
	for _, h := range p.headings {
		if h.Level == 2 || h.Level == 3 {
			view.TOC = append(view.TOC, h)
		}
	}
	if len(view.TOC) < 2 {
		view.TOC = nil
	}
	var out bytes.Buffer
	if err := pageTemplate.Execute(&out, view); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// searchIndex is a script rather than JSON so search also works when the
// site is opened from the filesystem, where fetch() is blocked.
func (b *builder) searchIndex() ([]byte, error) {
	type entry struct {
		Title    string    `json:"t"`
		URL      string    `json:"u"`
		Archived bool      `json:"a,omitempty"`
		Headings []heading `json:"h"`
		Text     string    `json:"x"`
	}
	entries := make([]entry, 0, len(b.pages))
	for _, p := range b.pages {
		var hs []heading
		for _, h := range p.headings {
			if h.Level > 1 {
				hs = append(hs, h)
			}
		}
		entries = append(entries, entry{Title: p.title, URL: p.out, Archived: p.archived, Headings: hs, Text: p.text})
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString("// Generated by make docs-site. Do not edit.\nwindow.HEARTBEAT_SEARCH = ")
	out.Write(data)
	out.WriteString(";\n")
	return out.Bytes(), nil
}
