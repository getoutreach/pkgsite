// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package frontend

import (
	"bytes"
	"context"
	"math"

	"github.com/google/safehtml"
	"github.com/google/safehtml/template"
	"github.com/google/safehtml/uncheckedconversions"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	goldmarkHtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"golang.org/x/pkgsite/internal"
	"golang.org/x/pkgsite/internal/derrors"
)

// Heading holds data about a heading within a readme used in the
// sidebar template to render the readme outline.
type Heading struct {
	// Level is the original level of the heading.
	Level int
	// Text is the content from the readme contained within a heading.
	Text string
	// ID corresponds to the ID attribute for a heading element
	// and is also used in an href to the corresponding section
	// within the readme outline. All ids are prefixed with readme-
	// to avoid name collisions.
	ID string
}

// Readme sanitizes readmeContents and returns a safehtml.HTML. If the readme filepath
// indicates that this is a markdown file, it will render the markdown contents and
// generate an outline from the parsed readmeContent's ast. Headings are prefixed with
// "readme-" and heading levels are adjusted to start at h3 in order to nest them
// properly within the rest of the page. The readme's original styling is preserved
// in the html by giving headings a css class styled identical to their original
// heading level.
//
// This function is exported for use in an external tool that uses this package to
// compare readme files to see how changes in processing will affect them.
func Readme(ctx context.Context, u *internal.Unit) (_ safehtml.HTML, _ []*Heading, err error) {
	defer derrors.Wrap(&err, "Readme(%q, %q, %q)", u.Path, u.ModulePath, u.Version)
	if u.Readme == nil || u.Readme.Contents == "" {
		return safehtml.HTML{}, nil, nil
	}
	if !isMarkdown(u.Readme.Filepath) {
		t := template.Must(template.New("").Parse(`<pre class="readme">{{.}}</pre>`))
		h, err := t.ExecuteToHTML(u.Readme.Contents)
		if err != nil {
			return safehtml.HTML{}, nil, err
		}
		return h, nil, nil
	}

	// Sets priority value so that we always use our custom transformer
	// instead of the default ones. The default values are in:
	// https://github.com/yuin/goldmark/blob/7b90f04af43131db79ec320be0bd4744079b346f/parser/parser.go#L567
	const ASTTransformerPriority = 10000
	gdMarkdown := goldmark.New(
		goldmark.WithParserOptions(
			// WithHeadingAttribute allows us to include other attributes in
			// heading tags. This is useful for our aria-level implementation of
			// increasing heading rankings.
			parser.WithHeadingAttribute(),
			// Generates an id in every heading tag. This is used in github in
			// order to generate a link with a hash that a user would scroll to
			// <h1 id="goldmark">goldmark</h1> => github.com/yuin/goldmark#goldmark
			parser.WithAutoHeadingID(),
			// Include custom ASTTransformer using the readme and module info to
			// use translateRelativeLink and translateHTML to modify the AST
			// before it is rendered.
			parser.WithASTTransformers(util.Prioritized(&ASTTransformer{
				info:   u.SourceInfo,
				readme: u.Readme,
			}, ASTTransformerPriority)),
		),
		// These extensions lets users write HTML code in the README. This is
		// fine since we process the contents using bluemonday after.
		goldmark.WithRendererOptions(goldmarkHtml.WithUnsafe(), goldmarkHtml.WithXHTML()),
		goldmark.WithExtensions(
			extension.GFM, // Support Github Flavored Markdown.
			emoji.Emoji,   // Support Github markdown emoji markup.
		),
	)
	gdMarkdown.Renderer().AddOptions(
		renderer.WithNodeRenderers(
			util.Prioritized(NewHTMLRenderer(u.SourceInfo, u.Readme), 100),
		),
	)
	contents := []byte(u.Readme.Contents)
	gdParser := gdMarkdown.Parser()
	reader := text.NewReader(contents)
	doc := gdParser.Parse(reader)
	gdRenderer := gdMarkdown.Renderer()

	var b bytes.Buffer
	if err := gdRenderer.Render(&b, contents, doc); err != nil {
		return safehtml.HTML{}, nil, nil
	}
	htmlContent := sanitizeHTML(&b)
	outline := readmeOutline(doc, contents)
	return htmlContent, outline, nil
}

// sanitizeHTML sanitizes HTML from a bytes.Buffer so that it is safe.
func sanitizeHTML(b *bytes.Buffer) safehtml.HTML {
	p := bluemonday.UGCPolicy()

	p.AllowAttrs("width", "align").OnElements("img")
	p.AllowAttrs("width", "align").OnElements("div")
	p.AllowAttrs("width", "align").OnElements("p")
	// Allow accessible headings (i.e <div role="heading" aria-level="7">).
	p.AllowAttrs("width", "align", "role", "aria-level").OnElements("div")
	for _, h := range []string{"h1", "h2", "h3", "h4", "h5", "h6"} {
		// Needed to preserve github styles heading font-sizes
		p.AllowAttrs("class").OnElements(h)
	}

	s := string(p.SanitizeBytes(b.Bytes()))
	return uncheckedconversions.HTMLFromStringKnownToSatisfyTypeContract(s)
}

// readmeOutline collects the headings from a readme into an outline
// of the document. It keeps only the top two levels of nesting from
// any set of headings. See tests for heading levels in TestReadme
// for behavior.
func readmeOutline(doc ast.Node, contents []byte) []*Heading {
	var headings []*Heading
	// l1 and l2 are used to keep track of the top two heading levels.
	l1, l2 := math.MaxInt8, math.MaxInt8

	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if n.Kind() == ast.KindHeading && entering {
			heading := n.(*ast.Heading)
			text := n.Text(contents)
			section := Heading{
				Level: heading.Level,
				Text:  string(text),
			}
			if id, ok := heading.AttributeString("id"); ok {
				section.ID = string(id.([]byte))
			}
			headings = append(headings, &section)
			if heading.Level < l1 {
				l2, l1 = l1, heading.Level
			} else if heading.Level < l2 && heading.Level != l1 {
				l2 = heading.Level
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})

	var filtered []*Heading
	for _, h := range headings {
		if h.Level <= l2 {
			filtered = append(filtered, h)
		}
	}
	return filtered
}
