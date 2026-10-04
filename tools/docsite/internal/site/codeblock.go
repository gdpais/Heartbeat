package site

import (
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// codeBlockRenderer renders fenced code blocks. Mermaid blocks become diagram
// placeholders that assets/site.js renders with mermaid.js; until then (or if
// the CDN is unreachable) the diagram source stays readable as a code block.
type codeBlockRenderer struct{}

func (codeBlockRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, renderFencedCodeBlock)
}

func renderFencedCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	language := n.Language(source)
	switch {
	case string(language) == "mermaid":
		_, _ = w.WriteString(`<div class="diagram"><pre class="diagram-source"><code>`)
	case language != nil:
		_, _ = w.WriteString(`<pre><code class="language-`)
		_, _ = w.Write(util.EscapeHTML(language))
		_, _ = w.WriteString(`">`)
	default:
		_, _ = w.WriteString("<pre><code>")
	}
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		line := lines.At(i)
		_, _ = w.Write(util.EscapeHTML(line.Value(source)))
	}
	_, _ = w.WriteString("</code></pre>")
	if string(language) == "mermaid" {
		_, _ = w.WriteString("</div>")
	}
	_ = w.WriteByte('\n')
	return ast.WalkSkipChildren, nil
}
