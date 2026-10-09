package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// Diagrams are drawn in the browser by Mermaid, from a CDN. For where it
// cannot be reached, each comes with a picture of itself, drawn when the wiki
// is rendered by headless Chrome running a copy of Mermaid kept here, and
// embedded in the page as a PNG; the page shows the picture until Mermaid
// draws the diagram over it. Pictures are kept in DEST/diagrams, named by a
// hash of their source, and drawn only for diagrams new to the wiki.

// mermaidJS is Mermaid 11.4.1's dist/mermaid.min.js, gzipped, under the MIT
// license in mermaid/LICENSE. It must be the version page.html imports.
//
//go:embed mermaid/mermaid.min.js.gz
var mermaidJS []byte

// mermaidConfig is how diagrams are drawn, in the browser and in pictures.
const mermaidConfig = `{
    startOnLoad: false,
    theme: "base",
    securityLevel: "strict",
    themeVariables: {
      fontFamily: '"Times New Roman", Times, serif',
      fontSize: "15px",
      primaryColor: "#fff",
      primaryBorderColor: "#000",
      primaryTextColor: "#000",
      lineColor: "#000",
      secondaryColor: "#fff",
      tertiaryColor: "#fff",
      edgeLabelBackground: "#fff",
      clusterBkg: "#fff",
      clusterBorder: "#000",
      noteBkgColor: "#fff",
      noteBorderColor: "#000",
      actorBkg: "#fff",
      actorBorder: "#000",
      signalColor: "#000",
    },
  }`

// pictureScale is how many pixels of a picture go to a pixel of the page.
const pictureScale = 2

// A picture is a diagram drawn as a PNG.
type picture struct {
	png   []byte
	width int // in pixels of the page
}

const diagramDir = "diagrams"

// diagramKey names a diagram's picture: a hash of its source and of how it
// is drawn.
func diagramKey(source string) string {
	h := sha256.Sum256([]byte(mermaidConfig + "\x00" + source))
	return hex.EncodeToString(h[:])[:16]
}

// pictures returns the pictures of the diagrams in sources, by key: those in
// DEST/diagrams, and the others drawn and saved there. Diagrams that cannot
// be drawn, as when they do not parse, are left out, and remembered so that
// they are not tried again; when Chrome cannot be found or fails, the reason
// is returned with what pictures there are. Pictures of diagrams no longer in
// the wiki are removed.
func pictures(dest string, sources []string) (map[string]picture, error) {
	dir := filepath.Join(dest, diagramDir)
	pics := map[string]picture{}
	keep := map[string]bool{}
	var missing []string
	for _, src := range sources {
		key := diagramKey(src)
		if keep[key] {
			continue
		}
		keep[key] = true
		if data, err := os.ReadFile(filepath.Join(dir, key+".png")); err == nil {
			if pic, err := newPicture(data); err == nil {
				pics[key] = pic
				continue
			}
		}
		if _, err := os.Stat(filepath.Join(dir, key+".err")); err == nil {
			continue
		}
		missing = append(missing, src)
	}
	var drawErr error
	if len(missing) > 0 {
		var drawn []drawing
		drawn, drawErr = drawDiagrams(missing)
		if len(drawn) > 0 {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return pics, err
			}
		}
		for i, d := range drawn {
			key := diagramKey(missing[i])
			if d.Error != "" {
				writeFile(filepath.Join(dir, key+".err"), []byte(d.Error+"\n"))
				continue
			}
			data, err := decodeDataURL(d.PNG)
			if err != nil {
				continue
			}
			pic, err := newPicture(data)
			if err != nil {
				continue
			}
			if err := writeFile(filepath.Join(dir, key+".png"), data); err != nil {
				return pics, err
			}
			pics[key] = pic
		}
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if key := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".png"), ".err"); !keep[key] {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	return pics, drawErr
}

func decodeDataURL(url string) ([]byte, error) {
	data, ok := strings.CutPrefix(url, "data:image/png;base64,")
	if !ok {
		return nil, errors.New("not a PNG data: URL")
	}
	return base64.StdEncoding.DecodeString(data)
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func newPicture(data []byte) (picture, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return picture{}, err
	}
	return picture{png: data, width: (cfg.Width + pictureScale - 1) / pictureScale}, nil
}

// A drawing is what Chrome made of a diagram: a PNG as a data: URL, or why
// there is none.
type drawing struct {
	PNG   string `json:"png"`
	Error string `json:"error"`
}

// drawDiagrams draws diagrams; tests replace it.
var drawDiagrams = draw

// draw has headless Chrome draw diagrams, all in one page.
func draw(sources []string) ([]drawing, error) {
	chrome := findChrome()
	if chrome == "" {
		return nil, errors.New("no Chrome to draw them with; set $WIKI_CHROME to one")
	}
	tmp, err := os.MkdirTemp("", "wiki-diagrams-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	zr, err := gzip.NewReader(bytes.NewReader(mermaidJS))
	if err != nil {
		return nil, err
	}
	var js bytes.Buffer
	if _, err := js.ReadFrom(zr); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "mermaid.min.js"), js.Bytes(), 0o644); err != nil {
		return nil, err
	}
	list, err := json.Marshal(sources) // which escapes <, so the list cannot end the script
	if err != nil {
		return nil, err
	}
	page := strings.NewReplacer("DIAGRAMS", string(list), "CONFIG", mermaidConfig, "SCALE", fmt.Sprint(pictureScale)).Replace(drawingPage)
	if err := os.WriteFile(filepath.Join(tmp, "draw.html"), []byte(page), 0o644); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome,
		"--headless", "--disable-gpu", "--no-first-run", "--disable-extensions",
		"--user-data-dir="+filepath.Join(tmp, "profile"),
		"--virtual-time-budget=60000",
		"--dump-dom", "file://"+filepath.ToSlash(filepath.Join(tmp, "draw.html")))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	m := drawnOut.FindSubmatch(stdout.Bytes())
	if m == nil {
		if runErr == nil {
			runErr = errors.New("it drew nothing")
		}
		return nil, fmt.Errorf("chrome: %v: %s", runErr, lastLine(stderr.String()))
	}
	var drawn []drawing
	if err := json.Unmarshal([]byte(html.UnescapeString(string(m[1]))), &drawn); err != nil {
		return nil, fmt.Errorf("chrome: %v", err)
	}
	if len(drawn) != len(sources) {
		return nil, fmt.Errorf("chrome drew %d of %d diagrams", len(drawn), len(sources))
	}
	return drawn, nil
}

var drawnOut = regexp.MustCompile(`(?s)<pre id="drawn">(.*?)</pre>`)

// drawingPage draws each diagram as Mermaid would in the page, then as a PNG,
// and leaves the PNGs as JSON in pre#drawn for --dump-dom. Labels are drawn
// as SVG text rather than HTML, which would taint the canvas.
const drawingPage = `<!doctype html>
<html><head><meta charset="utf-8"></head><body>
<script src="mermaid.min.js"></script>
<script>
const diagrams = DIAGRAMS;
const scale = SCALE;
mermaid.initialize(Object.assign(CONFIG, {htmlLabels: false, flowchart: {htmlLabels: false}}));

async function picture(svg) {
  const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
  const root = doc.documentElement;
  const box = (root.getAttribute("viewBox") || "").split(/[\s,]+/).map(Number);
  if (box.length === 4 && box[2] > 0 && box[3] > 0) {
    root.setAttribute("width", box[2]);
    root.setAttribute("height", box[3]);
    root.style.maxWidth = "";
  }
  const img = new Image();
  img.src = "data:image/svg+xml;base64," + btoa(unescape(encodeURIComponent(new XMLSerializer().serializeToString(root))));
  await img.decode();
  const canvas = document.createElement("canvas");
  canvas.width = Math.ceil(img.naturalWidth * scale);
  canvas.height = Math.ceil(img.naturalHeight * scale);
  const g = canvas.getContext("2d");
  g.fillStyle = "#fff";
  g.fillRect(0, 0, canvas.width, canvas.height);
  g.drawImage(img, 0, 0, canvas.width, canvas.height);
  return canvas.toDataURL("image/png");
}

(async () => {
  const drawn = [];
  for (let i = 0; i < diagrams.length; i++) {
    try {
      const { svg } = await mermaid.render("diagram-" + i, diagrams[i]);
      drawn.push({ png: await picture(svg) });
    } catch (e) {
      drawn.push({ error: String((e && e.message) || e) });
    }
  }
  const pre = document.createElement("pre");
  pre.id = "drawn";
  pre.textContent = JSON.stringify(drawn);
  document.body.appendChild(pre);
})();
</script>
</body></html>
`

// findChrome looks for Chrome or Chromium: $WIKI_CHROME, else on $PATH, else
// where macOS keeps it.
func findChrome() string {
	if c := os.Getenv("WIKI_CHROME"); c != "" {
		return c
	}
	for _, name := range []string{"chrome", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	if runtime.GOOS == "darwin" {
		for _, p := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// mermaidSources returns the sources of a page's diagrams.
func mermaidSources(doc ast.Node, source []byte) []string {
	var sources []string
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if b, ok := n.(*ast.FencedCodeBlock); ok && entering && string(b.Language(source)) == "mermaid" {
			sources = append(sources, codeText(b, source))
		}
		return ast.WalkContinue, nil
	})
	return sources
}

func codeText(b *ast.FencedCodeBlock, source []byte) string {
	var s strings.Builder
	lines := b.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		s.Write(seg.Value(source))
	}
	return s.String()
}

// A diagramRenderer writes a diagram with a picture as the picture, followed
// by its source, hidden, for Mermaid to draw over both; diagrams without one
// are written as code, as any other.
type diagramRenderer struct {
	pics map[string]picture
}

func (r *diagramRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.render)
}

func (r *diagramRenderer) render(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	b := n.(*ast.FencedCodeBlock)
	lang := string(b.Language(source))
	if !entering {
		return ast.WalkContinue, nil
	}
	code := codeText(b, source)
	pic, ok := r.pics[diagramKey(code)]
	if lang == "mermaid" && ok {
		fmt.Fprintf(w, `<figure class="diagram"><img src="data:image/png;base64,%s" width="%d" alt="Diagram"><pre hidden><code class="language-mermaid">%s</code></pre></figure>`+"\n",
			base64Encode(pic.png), pic.width, html.EscapeString(code))
		return ast.WalkSkipChildren, nil
	}
	w.WriteString("<pre><code")
	if lang != "" {
		fmt.Fprintf(w, ` class="language-%s"`, html.EscapeString(lang))
	}
	fmt.Fprintf(w, ">%s</code></pre>\n", html.EscapeString(code))
	return ast.WalkSkipChildren, nil
}
