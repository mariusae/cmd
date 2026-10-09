package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The answers are asked for as JSON in a strict schema: every property
// required and no others, which is the form both Claude and Codex accept.

var planSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["pages"],
  "properties": {
    "pages": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["slug", "title", "parent", "description", "files"],
        "properties": {
          "slug": {"type": "string"},
          "title": {"type": "string"},
          "parent": {"type": "string"},
          "description": {"type": "string"},
          "files": {"type": "array", "items": {"type": "string"}}
        }
      }
    }
  }
}`)

var pageSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["markdown", "sources"],
  "properties": {
    "markdown": {"type": "string"},
    "sources": {"type": "array", "items": {"type": "string"}}
  }
}`)

// maxListed is how many file paths a planning prompt lists; past it the agent
// is left to look for itself.
const maxListed = 3000

func planPrompt(t *tree, old *state, changed []string) string {
	var b strings.Builder
	b.WriteString(`You are planning a wiki for the code base in the current directory, in the style of DeepWiki (deepwiki.com): a set of pages that together explain to an engineer new to the project what it is, how it is put together, and how its parts work.

Explore the code base with your tools before deciding anything: read the README and other documentation, the build files, the entry points, and enough of the source of each part to understand what it does and how the parts depend on each other.

Then outline the wiki. Guidelines:

- The first page has the slug "overview": what the project is for, its main features, its architecture at a high level, and a map of the code base.
- Then pages for the major subsystems, components, and concepts, ordered so that a reader going front to back meets foundations before what is built on them. Include, where they matter, pages on core abstractions and data models, important flows (a request, a build, a command), configuration, the build and test setup, and extension points.
- Use one level of nesting where it helps: a page may name another top-level page as its parent; otherwise parent is "". A child must come after its parent.
- Size the wiki to the code base: about 5 pages for a small tool, 10 to 20 for a typical project, up to about 35 for a large one. Every page should have substance; do not make a page for something a paragraph elsewhere would cover.
- slug: short, lowercase, hyphenated, unique (e.g. "request-lifecycle").
- title: a short, specific title (e.g. "Request Lifecycle").
- description: two to four sentences saying exactly what the page covers and what it leaves to other pages; the page's writer will see only this and the outline.
- files: the 3 to 15 source files (paths relative to the current directory) most relevant to the page, which its writer should read first. Directories are allowed when a whole directory is the subject.

`)
	if old != nil {
		fmt.Fprintf(&b, `This wiki has been written before, and the code base has changed since. Revise the existing outline rather than starting over: keep the slugs, titles and descriptions of pages that are still right exactly as they are, so they are not rewritten for nothing; change a page's title or description only when what it covers has changed; add pages for new subsystems and drop pages for removed ones. Update the files lists to match the code as it now is.

The existing outline:

%s

The files added, removed or changed since it was written:

`, mustJSON(outlineOf(old.Pages, true)))
		writeList(&b, changed, maxListed)
		b.WriteString("\n")
	}
	b.WriteString("The files of the code base:\n\n")
	writeList(&b, t.sortedPaths(), maxListed)
	b.WriteString("\nAnswer with the outline as JSON, the pages in reading order.\n")
	return b.String()
}

func pagePrompt(name, outline string, p *page, previous string, changed []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You are writing one page of a wiki for the code base in the current directory (%s), in the style of DeepWiki (deepwiki.com). The wiki's outline, as JSON:

%s

The page to write:

  slug: %s
  title: %s
  covers: %s
`, name, outline, p.Slug, p.Title, p.Description)
	if len(p.Files) > 0 {
		fmt.Fprintf(&b, "  start from: %s\n", strings.Join(p.Files, ", "))
	}
	b.WriteString(`
Read the relevant source thoroughly before writing — start from the files above and follow the code wherever it leads. Everything on the page must be true of the code as it is; never guess at what a name means or does. Write for an engineer who is new to the code base and wants to understand it well enough to work on it.

Write the page in GitHub-flavored Markdown:

- Begin with "# " and the title. Then a short paragraph or two saying what the subject is for and where it fits, linking to related pages. Do not add a list of relevant source files; one is added for you.
- Organize the rest under "## " and "### " headings: architecture and responsibilities, key types and functions, how data and control flow, important algorithms, configuration, error handling, edge cases — whatever fits the subject.
- Include one to three Mermaid diagrams where a picture explains more than prose — component relationships (flowchart TD or LR), interactions over time (sequenceDiagram), type structure (classDiagram), lifecycles (stateDiagram-v2). Put them in fenced code blocks marked "mermaid". Keep their syntax simple and valid: quote any node label containing spaces or punctuation, as A["Parser (lexer)"], and do not use HTML or Markdown inside labels.
- Use tables to summarize things that come in sets: components and their roles, functions and what they do, configuration options, commands.
- Quote code sparingly: short excerpts that show a key signature, type, or idiom, in fenced blocks with a language.
- Link other pages of the wiki as [Title](slug.md), using the slugs of the outline.
- Cite the source. End each "## " section with a line of the form
  Sources: [path/to/file.go:12-48](path/to/file.go#L12-L48), [other.go:7](other.go#L7)
  giving the files and line ranges the section rests on, with paths relative to the current directory and line numbers you have checked. Cite specific code in the prose the same way where it helps the reader find it.
- Prefer clear, direct prose to lists of fragments. Aim for a thorough page: typically 800 to 2500 words, more for a central subject.

Answer with JSON: "markdown", the whole page; and "sources", every file you relied on, relative to the current directory.
`)
	if previous != "" {
		b.WriteString("\nThis page was written before, and the code may have changed since. ")
		if len(changed) > 0 {
			b.WriteString("Files it rests on that have changed:\n\n")
			writeList(&b, changed, 200)
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, `Revise the page so it is true of the code as it is now, and fits its description above: check its claims against the source, correct what is wrong, add what is missing, and keep what is still right as it is. The page as it was:

<previous-page>
%s
</previous-page>
`, strings.TrimSpace(previous))
	}
	return b.String()
}

type outlineEntry struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Parent      string   `json:"parent"`
	Description string   `json:"description"`
	Files       []string `json:"files,omitempty"`
}

func outlineOf(pages []page, withFiles bool) []outlineEntry {
	entries := make([]outlineEntry, len(pages))
	for i, p := range pages {
		entries[i] = outlineEntry{Slug: p.Slug, Title: p.Title, Parent: p.Parent, Description: p.Description}
		if withFiles {
			entries[i].Files = p.Files
		}
	}
	return entries
}

func mustJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(data)
}

func writeList(b *strings.Builder, items []string, max int) {
	for i, s := range items {
		if i == max {
			fmt.Fprintf(b, "... and %d more; look for them with your tools\n", len(items)-max)
			break
		}
		b.WriteString(s)
		b.WriteByte('\n')
	}
}
