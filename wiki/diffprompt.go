package main

import (
	"fmt"
	"strings"
)

// The prompts for the wiki of a change. Its pages are always the same three,
// written at once from the change without a plan: an overview of the change,
// a walkthrough of it for its reviewers, and the background they need.

// changePages are the pages of a change's wiki, resting on files.
func changePages(files []string) []page {
	return []page{
		{
			Slug:        "overview",
			Title:       "Overview",
			Description: "What the change does and why, its approach and scope, and what a reviewer should know first.",
			Files:       files,
		},
		{
			Slug:        "walkthrough",
			Title:       "Walkthrough",
			Description: "A walk through the whole change for its reviewers, from foundations and new abstractions, through behavior and integration, to edge cases and tests.",
			Files:       files,
		},
		{
			Slug:        "background",
			Title:       "Background",
			Description: "The code the change works in, as it was before the change: what a reviewer new to it needs to know to judge the change.",
			Files:       files,
		},
	}
}

// pageGuides are the instructions for writing the fixed pages.
var pageGuides = map[string]string{
	"overview": `This page is the overview of the change, the first a reader sees. Say what the change does and why — the problem it solves or the feature it adds, as the commit messages tell it and as the code bears out — and how: the approach, and the main pieces that make it up. Give the background a reader needs to follow it, briefly, linking to the pages that explain more. Summarize the scope in a table of the areas or files changed and what changed in each. Then say what a reviewer should know before starting: behavior that changes for users or callers, compatibility, migrations, risks, and anything the change leaves undone. End by pointing to the [Walkthrough](walkthrough.md), and to the [Background](background.md) for readers new to this code. Typically 500 to 1500 words.
`,
	"walkthrough": `This page is the walkthrough: a reviewer reads it front to back, once, to understand the whole change well enough to review it. Write it as one linear narrative, not file by file in the patch's order, and under these headings, in this order, leaving out any with nothing under it:

1. Foundations — the new and changed types, data structures, interfaces, schemas and abstractions the rest is built on, each with what it is for.
2. Behavior — the logic that changes what the code does, step by step.
3. Integration — how the new behavior is wired in: call sites, configuration, flags, plumbing, and what now reaches it.
4. Edge cases — errors, boundary conditions, concurrency, compatibility, and whatever else is subtle: what the change does about each, and what it may miss.
5. Tests — what is tested and how. Summarize routine tests in a sentence or a table; show only tests that are themselves decisive or surprising.

Put the decisive changed code inline, in fenced blocks marked "diff" holding the hunks of the patch trimmed to the lines that matter, each with prose saying what it does and why. That code is the heart of the page: a reviewer should be able to judge the change from it without opening the patch. Give mechanical changes — renames, moved code, imports, formatting, generated files — a line each, not code. Say plainly what deserves a reviewer's attention: risks, assumptions, changes of behavior the commit messages do not mention, open questions. Account for every file of the change, if only in a closing list of the routine ones. Link to the [Background](background.md) where a reader needs to know how the code worked before, rather than explaining it here. A diagram helps only where the change alters how parts interact; most walkthroughs need none. Make the page as long as the change needs.
`,
	"background": `This page is the background: the code the change works in, as it was before the change, for a reviewer new to it. Find the subsystems, abstractions, data structures and flows the change touches or depends on, and explain them well enough to judge the change: what each is for, how it works, and how the parts fit together, with diagrams where they help. Explain the code before the change, reading it as of the change's base; where the change comes in, say so in a sentence and link to the [Walkthrough](walkthrough.md), but do not walk through the change itself. Leave out what the change does not bear on. Typically 800 to 2500 words, fewer for a small change in simple code.
`,
}

// maxPatch is how much of a change's patch a prompt quotes; past it the agent
// is left to read the rest itself.
const maxPatch = 200_000

func diffPagePrompt(c *change, outline string, p *page, previous string, changed []string, draft bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You are writing one page of a wiki about a change to the code base in the current directory — a diff under review — in the style of DeepWiki (deepwiki.com). The wiki's outline, as JSON:

%s

`, outline)
	describeChange(&b, c)
	b.WriteString(`
Read the change and the code around it thoroughly before writing, and follow the code wherever it leads. Everything on the page must be true of the code; never guess at what a name means or does. Write for a reviewer of the change who may be new to this part of the code base.

`)
	b.WriteString(pageFormat)
	b.WriteString(`- Quote changed code as fenced blocks marked "diff", trimmed to the lines that matter. Cite lines as they are after the change.
- Prefer clear, direct prose to lists of fragments.
`)
	b.WriteString(answerFormat)
	writePage(&b, p)
	if guide := pageGuides[p.Slug]; guide != "" {
		b.WriteString("\n" + guide)
	}
	if draft {
		b.WriteString("\n" + draftLength)
	}
	writePrevious(&b, previous, changed, "the change may have been revised since", "true of the change as it is now")
	return b.String()
}

// describeChange writes out a change for a prompt: its commits, its files,
// how to read its code, and its patch.
func describeChange(b *strings.Builder, c *change) {
	fmt.Fprintf(b, "The change is %s in %s, from %s to %s. ", pluralize(len(c.Commits), "commit"), vcsName(c.VCS), shortRevision(c.baseRev()), shortRevision(c.Head))
	b.WriteString("Its commit messages:\n\n")
	for _, cm := range c.Commits {
		fmt.Fprintf(b, "<commit hash=%q author=%q date=%q>\n%s\n</commit>\n\n", cm.Hash, cm.Author, cm.Date, cm.Message)
	}
	b.WriteString("The files it changes, with the lines added and removed:\n\n")
	for _, p := range c.parts {
		fmt.Fprintf(b, "%s +%d -%d\n", p.Path, p.Added, p.Removed)
	}
	b.WriteString("\n")

	show, diff := c.readCommands()
	if c.atHead {
		fmt.Fprintf(b, "The working copy is checked out at the change's last commit, so the files you read with your tools are as the change leaves them. For the code as it was before the change, read the patch, or run %s.", fmt.Sprintf(show, c.baseRev()))
	} else {
		fmt.Fprintf(b, "The working copy is NOT checked out at the change, so the files you read with your tools may differ from it. Read a file as the change leaves it with %s, and as it was before with %s; read from disk only what the change does not touch.", fmt.Sprintf(show, c.Head), fmt.Sprintf(show, c.baseRev()))
	}
	fmt.Fprintf(b, " For the patch of a file, run %s.\n\n", diff)

	var quoted, omitted []string
	size := 0
	for _, p := range c.parts {
		if size+len(p.Text) > maxPatch {
			omitted = append(omitted, p.Path)
			continue
		}
		size += len(p.Text)
		quoted = append(quoted, p.Text)
	}
	if len(quoted) > 0 {
		b.WriteString("The patch:\n\n")
		fmt.Fprintf(b, "<patch>\n%s</patch>\n", strings.Join(quoted, ""))
	}
	if len(omitted) > 0 {
		b.WriteString("\nThe patch of these files is left out for its length; read it yourself:\n\n")
		writeList(b, omitted, maxListed)
	}
}

// baseRev names the revision the change is diffed against.
func (c *change) baseRev() string {
	switch {
	case c.Base != "":
		return c.Base
	case c.VCS == "git":
		return gitEmptyTree
	}
	return "null"
}

// readCommands returns how to read a file at a revision, with a %s for the
// revision, and how to read a file's part of the patch.
func (c *change) readCommands() (show, diff string) {
	if c.VCS == "sl" {
		return "`sl cat -r %s PATH`", fmt.Sprintf("`sl diff --git -r %s -r %s PATH`", c.baseRev(), c.Head)
	}
	return "`git show %s:PATH`", fmt.Sprintf("`git diff %s %s -- PATH`", c.baseRev(), c.Head)
}

func vcsName(vcs string) string {
	if vcs == "sl" {
		return "Sapling"
	}
	return vcs
}

func pluralize(n int, noun string) string {
	if n == 1 {
		return "one " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
