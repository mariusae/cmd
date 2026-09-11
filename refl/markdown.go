package main

// Markdown block structure, ported from Reflect's block-context rules
// (packages/core/src/indexing/block-context.ts, and its CLI port in
// apps/cli/src/block_context.rs). A match does not yield its physical line but
// the whole unit of meaning it sits in:
//
//   - Paragraph: the whole paragraph, however it wraps.
//   - Heading: the heading plus every following sibling block up to the next
//     heading of any level.
//   - Title heading: just the heading line. The note's title is its first H1,
//     so the section rule would otherwise inline the entire note.
//   - Top-level list item: the item and everything nested under it.
//   - Nested list item: the parent item's own line, plus each branch under
//     that parent that also matches; branches that do not are dropped. Only
//     one ancestor level is climbed, exactly as Reflect does.
//
// The result is Markdown sliced from the source (full lines, dedented to the
// block's own indentation) so nested structure survives, and is never
// truncated.

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmextension "github.com/yuin/goldmark/extension"
	gmast "github.com/yuin/goldmark/extension/ast"
	gmtext "github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

var markdownParser = goldmark.New(goldmark.WithExtensions(gmextension.GFM)).Parser()

// A span is a half-open character range in a note's body.
type span struct {
	from, to int
}

// blockSource is a note parsed once for repeated block-context extraction. A
// note contributes one context per match, and re-parsing per match would make
// the cost scale with match count instead of note count.
type blockSource struct {
	source            string
	body              string
	bodyOffset        int
	doc               ast.Node
	spans             map[ast.Node]span
	frontmatterTitled bool
}

// A blockContext is one extracted block: its Markdown and the 1-based line of
// the source it starts on.
type blockContext struct {
	text string
	line int
}

// prepareBlocks carves the frontmatter off and parses the body.
func prepareBlocks(source string) *blockSource {
	split := splitFrontmatter(source)
	s := &blockSource{
		source:            source,
		body:              split.body,
		bodyOffset:        split.bodyOffset,
		spans:             map[ast.Node]span{},
		frontmatterTitled: parseFrontmatter(split.raw).title != "",
	}
	s.doc = markdownParser.Parse(gmtext.NewReader([]byte(s.body)))
	s.measure(s.doc)
	return s
}

// measure records every block node's source range, bottom up: a node covers
// its own raw lines and those of its children. Goldmark reports the ranges of
// a block's content, so the ranges of blocks written with a marker are then
// widened back over it — Reflect dedents a block by the text before it on its
// first line, and that text must be the indentation, not the bullet.
func (s *blockSource) measure(node ast.Node) (span, bool) {
	measured := span{from: -1, to: -1}
	if lines := node.Lines(); lines != nil && lines.Len() > 0 {
		measured.from = lines.At(0).Start
		measured.to = lines.At(lines.Len() - 1).Stop
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Type() != ast.TypeBlock {
			continue
		}
		childSpan, ok := s.measure(child)
		if !ok {
			continue
		}
		if measured.from < 0 || childSpan.from < measured.from {
			measured.from = childSpan.from
		}
		if childSpan.to > measured.to {
			measured.to = childSpan.to
		}
	}
	if measured.from < 0 {
		return span{}, false
	}
	measured = s.widenOverMarkers(node, measured)
	if measured.to < measured.from {
		measured.to = measured.from
	}
	s.spans[node] = measured
	return measured, true
}

func (s *blockSource) widenOverMarkers(node ast.Node, measured span) span {
	start := lineStartAt(s.body, measured.from)
	switch node.Kind() {
	case ast.KindHeading:
		marked := headingMarkStart(s.body, start, measured.from)
		if marked == measured.from {
			// No `#` marks: a Setext heading, whose underline goldmark leaves
			// outside the block but which reads as part of it.
			measured.to = setextUnderlineEnd(s.body, measured.to)
		}
		measured.from = marked
	case ast.KindListItem:
		measured.from = listMarkStart(s.body, start, measured.from)
	case ast.KindBlockquote:
		measured.from = scanBackOver(s.body, start, measured.from, ">\t ")
	case gmast.KindTable:
		measured.from = scanBackOver(s.body, start, measured.from, "|\t ")
	case ast.KindFencedCodeBlock:
		measured = widenOverFences(s.body, measured)
	}
	return measured
}

// headingMarkStart moves an ATX heading's start back over its `#` marks. A
// Setext heading has none, and keeps the start of its text.
func headingMarkStart(body string, lineStart, from int) int {
	at := scanBackOver(body, lineStart, from, "\t ")
	marks := at
	for marks > lineStart && body[marks-1] == '#' {
		marks--
	}
	if marks == at {
		return from
	}
	return marks
}

// listMarkStart moves a list item's start back over its bullet or number, so
// the item is dedented by its indentation and keeps its marker.
func listMarkStart(body string, lineStart, from int) int {
	at := scanBackOver(body, lineStart, from, "\t ")
	if at == lineStart {
		return from
	}
	switch mark := body[at-1]; {
	case mark == '-' || mark == '+' || mark == '*':
		return at - 1
	case mark == '.' || mark == ')':
		digits := at - 1
		for digits > lineStart && body[digits-1] >= '0' && body[digits-1] <= '9' {
			digits--
		}
		if digits == at-1 {
			return from
		}
		return digits
	}
	return from
}

// widenOverFences covers a fenced code block's delimiters, which goldmark
// reports outside the block's content.
func widenOverFences(body string, measured span) span {
	if start := lineStartAt(body, measured.from); start > 0 {
		opening := lineStartAt(body, start-1)
		if isFenceLine(body[opening : start-1]) {
			measured.from = opening + indentWidth(body[opening:start-1])
		}
	}
	if end := blockLineEnd(body, measured.to); end < len(body) {
		closing := lineEndAt(body, end+1)
		if isFenceLine(body[end+1 : closing]) {
			measured.to = closing
		}
	}
	return measured
}

var setextUnderline = regexp.MustCompile(`^ {0,3}(?:=+|-+)[ \t]*$`)

// setextUnderlineEnd covers the `===` or `---` under a Setext heading.
func setextUnderlineEnd(body string, to int) int {
	end := blockLineEnd(body, to)
	if end >= len(body) {
		return to
	}
	if next := lineEndAt(body, end+1); setextUnderline.MatchString(body[end+1 : next]) {
		return next
	}
	return to
}

// blockLineEnd is the end of the line a block's last character sits on. A
// block's own end may be reported past the newline that closed it.
func blockLineEnd(body string, to int) int {
	if to > 0 && to <= len(body) && body[to-1] == '\n' {
		to--
	}
	return lineEndAt(body, to)
}

func isFenceLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func indentWidth(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// scanBackOver moves from back over any of chars, stopping at lineStart.
func scanBackOver(body string, lineStart, from int, chars string) int {
	for from > lineStart && strings.IndexByte(chars, body[from-1]) >= 0 {
		from--
	}
	return from
}

func lineStartAt(body string, pos int) int {
	if pos > len(body) {
		pos = len(body)
	}
	if pos < 0 {
		pos = 0
	}
	return strings.LastIndexByte(body[:pos], '\n') + 1
}

func lineEndAt(body string, pos int) int {
	if pos > len(body) {
		pos = len(body)
	}
	if pos < 0 {
		pos = 0
	}
	if next := strings.IndexByte(body[pos:], '\n'); next >= 0 {
		return pos + next
	}
	return len(body)
}

// contextLines is a block in line form: origins[i] is the body offset of the
// first character of lines[i] after dedenting, so an offset within the block
// maps back to the source by addition.
type contextLines struct {
	lines   []string
	origins []int
}

func trimTrailing(context contextLines) contextLines {
	for len(context.lines) > 0 && strings.TrimSpace(context.lines[len(context.lines)-1]) == "" {
		context.lines = context.lines[:len(context.lines)-1]
		context.origins = context.origins[:len(context.origins)-1]
	}
	if last := len(context.lines) - 1; last >= 0 {
		context.lines[last] = strings.TrimRight(context.lines[last], " \t\r")
	}
	return context
}

// dedentedSlice returns the full lines covering [from, to), with prefix
// stripped from every line it leads. Deeper indentation stays relative, so a
// sliced list still reads as nested.
func dedentedSlice(body string, from, to int, prefix string) contextLines {
	start := lineStartAt(body, from)
	end := to
	if to > from && to <= len(body) && body[to-1] == '\n' {
		end = to - 1
	}
	end = lineEndAt(body, end)
	if end < start {
		end = start
	}
	var context contextLines
	lineStart := start
	for _, raw := range strings.Split(body[start:end], "\n") {
		if prefix != "" && strings.HasPrefix(raw, prefix) {
			context.lines = append(context.lines, raw[len(prefix):])
			context.origins = append(context.origins, lineStart+len(prefix))
		} else {
			context.lines = append(context.lines, raw)
			context.origins = append(context.origins, lineStart)
		}
		lineStart += len(raw) + 1
	}
	return trimTrailing(context)
}

// dedentedBlock slices a block dedented by its own first-line prefix: the text
// before from on its line, which is indentation, or `> ` inside a blockquote.
func dedentedBlock(body string, from, to int) contextLines {
	return dedentedSlice(body, from, to, body[lineStartAt(body, from):from])
}

func (s *blockSource) contains(node ast.Node, pos int) bool {
	measured, ok := s.spans[node]
	return ok && measured.from <= pos && pos < measured.to
}

func (s *blockSource) deepestAt(node ast.Node, pos int) ast.Node {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Type() == ast.TypeBlock && s.contains(child, pos) {
			return s.deepestAt(child, pos)
		}
	}
	return node
}

func selfOrAncestor(node ast.Node, matches func(ast.Node) bool) ast.Node {
	for current := node; current != nil; current = current.Parent() {
		if matches(current) {
			return current
		}
	}
	return nil
}

func isHeading(node ast.Node) bool { return node.Kind() == ast.KindHeading }

// isTextblock reports the leaf blocks that hold inline content. Goldmark uses
// TextBlock for a list item's own line, including a task item's.
func isTextblock(node ast.Node) bool {
	return node.Kind() == ast.KindParagraph || node.Kind() == ast.KindTextBlock
}

func isListItem(node ast.Node) bool { return node.Kind() == ast.KindListItem }

func isList(node ast.Node) bool { return node.Kind() == ast.KindList }

func isTable(node ast.Node) bool { return node.Kind() == gmast.KindTable }

// headingSectionEnd ends a heading's section at the next heading of any level,
// or at the end of the section's parent.
func (s *blockSource) headingSectionEnd(heading ast.Node) int {
	end := s.spans[heading].to
	for sibling := heading.NextSibling(); sibling != nil; sibling = sibling.NextSibling() {
		if isHeading(sibling) {
			break
		}
		if measured, ok := s.spans[sibling]; ok {
			end = measured.to
		}
	}
	return end
}

var headingHashes = regexp.MustCompile(`^#{1,6}[ \t]*`)

func (s *blockSource) headingHasText(heading ast.Node) bool {
	from := s.spans[heading].from
	line := s.body[from:lineEndAt(s.body, from)]
	line = headingHashes.ReplaceAllString(line, "")
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line), "#")) != ""
}

// isTitleHeading reports whether a heading is the note's title — the first
// non-empty top-level H1 — in which case only its own line is the context. A
// frontmatter `title:` owns the title, and then every H1 is an ordinary
// section heading.
func (s *blockSource) isTitleHeading(heading ast.Node) bool {
	if s.frontmatterTitled {
		return false
	}
	if headingNode, ok := heading.(*ast.Heading); !ok || headingNode.Level != 1 {
		return false
	}
	if heading.Parent() == nil || heading.Parent().Kind() != ast.KindDocument {
		return false
	}
	for child := heading.Parent().FirstChild(); child != nil; child = child.NextSibling() {
		node, ok := child.(*ast.Heading)
		if !ok || node.Level != 1 || !s.headingHasText(child) {
			continue
		}
		return child == heading
	}
	return false
}

// leadTextblock is an item's own line: its first block child, when that child
// holds inline content rather than opening a nested list.
func leadTextblock(item ast.Node) ast.Node {
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Type() != ast.TypeBlock {
			continue
		}
		if isTextblock(child) {
			return child
		}
		return nil
	}
	return nil
}

// branchMatches reports whether a branch under a parent item matches in its
// own text, which is what earns it a place in a nested item's context.
func (s *blockSource) branchMatches(branch ast.Node, match matcher) bool {
	measured, ok := s.spans[branch]
	if !ok || match == nil {
		return false
	}
	return match(s.body[measured.from:measured.to])
}

// A matcher says whether a piece of a note answers to what is being looked
// for: every term of a query, or any spelling of a backlink's target.
type matcher func(text string) bool

// termMatcher matches text holding every one of these terms.
func termMatcher(terms []string) matcher {
	if len(terms) == 0 {
		return nil
	}
	return func(text string) bool { return textMatches(text, terms) }
}

// listItemContext is Reflect's rule for a match inside a list: a top-level
// item yields its whole subtree, and a nested item yields the parent item's
// own line plus the branches under it that match too — always including the
// branch the match itself sits in.
func (s *blockSource) listItemContext(item ast.Node, match matcher, pos int) contextLines {
	own := s.spans[item]
	parent := selfOrAncestor(item.Parent(), isListItem)
	if parent == nil {
		return dedentedBlock(s.body, own.from, own.to)
	}
	lead := leadTextblock(parent)
	if lead == nil {
		return dedentedBlock(s.body, own.from, own.to)
	}
	parentSpan := s.spans[parent]
	indent := s.body[lineStartAt(s.body, parentSpan.from):parentSpan.from]
	pieces := []contextLines{dedentedBlock(s.body, parentSpan.from, s.spans[lead].to)}
	for sibling := lead.NextSibling(); sibling != nil; sibling = sibling.NextSibling() {
		if sibling.Type() != ast.TypeBlock {
			continue
		}
		branches := []ast.Node{sibling}
		if isList(sibling) {
			branches = nil
			for child := sibling.FirstChild(); child != nil; child = child.NextSibling() {
				if isListItem(child) {
					branches = append(branches, child)
				}
			}
		}
		for _, branch := range branches {
			if !s.branchMatches(branch, match) && !s.contains(branch, pos) {
				continue
			}
			measured := s.spans[branch]
			pieces = append(pieces, dedentedSlice(s.body, measured.from, measured.to, indent))
		}
	}
	var joined contextLines
	for _, piece := range pieces {
		joined.lines = append(joined.lines, piece.lines...)
		joined.origins = append(joined.origins, piece.origins...)
	}
	return joined
}

func (s *blockSource) contextAt(pos int, match matcher) contextLines {
	leaf := s.deepestAt(s.doc, pos)
	if heading := selfOrAncestor(leaf, isHeading); heading != nil {
		measured := s.spans[heading]
		end := measured.to
		if !s.isTitleHeading(heading) {
			end = s.headingSectionEnd(heading)
		}
		return dedentedBlock(s.body, measured.from, end)
	}
	if item := selfOrAncestor(leaf, isListItem); item != nil {
		return s.listItemContext(item, match, pos)
	}
	for _, matches := range []func(ast.Node) bool{isTextblock, isTable} {
		if block := selfOrAncestor(leaf, matches); block != nil {
			measured := s.spans[block]
			return dedentedBlock(s.body, measured.from, measured.to)
		}
	}
	if top := selfOrAncestor(leaf, func(node ast.Node) bool {
		return node.Parent() != nil && node.Parent().Kind() == ast.KindDocument
	}); top != nil {
		measured := s.spans[top]
		return dedentedBlock(s.body, measured.from, measured.to)
	}
	// The offset fell between blocks, or into something with no structure of
	// its own: the bare line is the best context there is.
	start := lineStartAt(s.body, pos)
	raw := s.body[start:lineEndAt(s.body, pos)]
	leading := indentWidth(raw)
	return contextLines{lines: []string{strings.TrimSpace(raw)}, origins: []int{start + leading}}
}

// blockContextsAt extracts the deduplicated block around each whole-file
// position, in the order the positions are given.
func (s *blockSource) blockContextsAt(positions []int, match matcher) []blockContext {
	seen := map[string]bool{}
	var contexts []blockContext
	for _, position := range positions {
		bodyPos := position - s.bodyOffset
		if bodyPos < 0 {
			bodyPos = 0
		}
		if bodyPos > len(s.body) {
			bodyPos = len(s.body)
		}
		context := s.contextAt(bodyPos, match)
		text := strings.Join(context.lines, "\n")
		if strings.TrimSpace(text) == "" || seen[text] {
			continue
		}
		seen[text] = true
		origin := bodyPos
		if len(context.origins) > 0 {
			origin = context.origins[0]
		}
		contexts = append(contexts, blockContext{
			text: text,
			line: lineNumberAt(s.source, origin+s.bodyOffset),
		})
	}
	return contexts
}

func lineNumberAt(source string, offset int) int {
	if offset > len(source) {
		offset = len(source)
	}
	if offset < 0 {
		offset = 0
	}
	return strings.Count(source[:offset], "\n") + 1
}

// ---- frontmatter ---------------------------------------------------------

var (
	frontmatterOpen  = regexp.MustCompile(`^---[ \t]*\r?\n`)
	frontmatterClose = regexp.MustCompile(`(?:^|\r?\n)---[ \t]*(?:\r?\n|$)`)
)

type frontmatterSplit struct {
	raw        string
	body       string
	bodyOffset int
}

// splitFrontmatter carves a leading YAML frontmatter block off source,
// preserving offsets. An unterminated fence is tolerated as body, so a note
// stays readable however it was edited.
func splitFrontmatter(source string) frontmatterSplit {
	open := frontmatterOpen.FindString(source)
	if open == "" {
		return frontmatterSplit{body: source}
	}
	rest := source[len(open):]
	closing := frontmatterClose.FindStringIndex(rest)
	if closing == nil {
		return frontmatterSplit{body: source}
	}
	offset := len(open) + closing[1]
	return frontmatterSplit{raw: rest[:closing[0]], body: source[offset:], bodyOffset: offset}
}

// frontmatterFields are the frontmatter keys this tool acts on.
type frontmatterFields struct {
	title   string
	aliases []string
	private bool
}

// parseFrontmatter reads those fields. Parsing is tolerant: broken or
// non-mapping YAML degrades to no frontmatter, never to an unreadable note.
func parseFrontmatter(raw string) frontmatterFields {
	var fields frontmatterFields
	if strings.TrimSpace(raw) == "" {
		return fields
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(raw), &parsed); err != nil {
		return fields
	}
	if value, ok := parsed["title"].(string); ok {
		fields.title = strings.TrimSpace(value)
	}
	if values, ok := parsed["aliases"].([]any); ok {
		for _, value := range values {
			if alias, ok := value.(string); ok && strings.TrimSpace(alias) != "" {
				fields.aliases = append(fields.aliases, strings.TrimSpace(alias))
			}
		}
	}
	switch value := parsed["private"].(type) {
	case bool:
		fields.private = value
	case string:
		fields.private = strings.EqualFold(strings.TrimSpace(value), "true")
	}
	return fields
}
