package main

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPNG(t *testing.T, w, h int) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64Encode(b.Bytes())
}

func TestDiagramPictures(t *testing.T) {
	dest := t.TempDir()
	writeTree(t, dest, map[string]string{
		"overview.md": "# Overview\n\n```mermaid\nflowchart TD\n  A --> B\n```\n\n```mermaid\nnot a diagram\n```\n\n```go\nx := 1 < 2\n```\n",
	})
	s := &state{Name: "w", Source: dest, Pages: []page{{Slug: "overview", Title: "Overview"}}}
	var asked [][]string
	pic := testPNG(t, 300, 100)
	drawDiagrams = func(sources []string) ([]drawing, error) {
		asked = append(asked, sources)
		var drawn []drawing
		for _, src := range sources {
			if strings.HasPrefix(src, "flowchart") {
				drawn = append(drawn, drawing{PNG: pic})
			} else {
				drawn = append(drawn, drawing{Error: "Parse error"})
			}
		}
		return drawn, nil
	}
	defer func() { drawDiagrams = draw }()

	index, err := render(dest, s, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(index)
	page := string(data)
	for _, want := range []string{
		`<figure class="diagram"><img src="data:image/png;base64,`,
		`width="150" alt="Diagram"><pre hidden><code class="language-mermaid">flowchart TD
  A --&gt; B
</code></pre></figure>`,
		`<pre><code class="language-mermaid">not a diagram
</code></pre>`,
		`<pre><code class="language-go">x := 1 &lt; 2
</code></pre>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if len(asked) != 1 || len(asked[0]) != 2 {
		t.Fatalf("asked to draw %v", asked)
	}

	// Rendered again, nothing is drawn: the picture is kept, and so is the
	// failure.
	if _, err := render(dest, s, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 {
		t.Errorf("drew again: %v", asked[1:])
	}

	// A diagram gone from the wiki takes its picture with it; when drawing
	// fails, the page is rendered all the same, with the diagram as code.
	writeTree(t, dest, map[string]string{"overview.md": "# Overview\n\n```mermaid\nflowchart LR\n  C --> D\n```\n"})
	drawDiagrams = func([]string) ([]drawing, error) { return nil, errors.New("no Chrome") }
	var log bytes.Buffer
	if _, err := render(dest, s, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "no Chrome") {
		t.Errorf("log = %q", log.String())
	}
	if entries, _ := os.ReadDir(filepath.Join(dest, diagramDir)); len(entries) != 0 {
		t.Errorf("kept %d stale pictures", len(entries))
	}
	if data, _ := os.ReadFile(index); !strings.Contains(string(data), `<pre><code class="language-mermaid">flowchart LR`) {
		t.Error("undrawn diagram not left as code")
	}
}

// TestDraw draws with real Chrome, where there is one.
func TestDraw(t *testing.T) {
	if findChrome() == "" || testing.Short() {
		t.Skip("no Chrome")
	}
	drawn, err := draw([]string{"flowchart TD\n  A[\"a </script> b\"] --> B", "sequenceDiagram\n  A->>B: hi", "flowchart TD\n  this is ( not valid"})
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range drawn[:2] {
		data, err := decodeDataURL(d.PNG)
		if err != nil {
			t.Fatalf("diagram %d: %v (%s)", i, err, d.Error)
		}
		pic, err := newPicture(data)
		if err != nil || pic.width < 50 {
			t.Errorf("diagram %d: %+v %v", i, pic.width, err)
		}
	}
	if drawn[2].Error == "" || drawn[2].PNG != "" {
		t.Errorf("bad diagram drawn: %+v", drawn[2].Error)
	}
}
