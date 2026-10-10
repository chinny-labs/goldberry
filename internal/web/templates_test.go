package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// A <details> start tag closes an open <p>, so a tip inside a paragraph
// would spill out of it. Tips go in divs, spans, labels or headings.
func TestNoTipInsideParagraph(t *testing.T) {
	open := regexp.MustCompile(`<p[\s>]`)
	files, _ := fs.Glob(templateFS, "templates/*.html")
	for _, name := range files {
		b, _ := fs.ReadFile(templateFS, name)
		s := string(b)
		for _, loc := range open.FindAllStringIndex(s, -1) {
			end := strings.Index(s[loc[0]:], "</p>")
			if end < 0 {
				continue
			}
			if strings.Contains(s[loc[0]:loc[0]+end], `template "tip"`) {
				t.Errorf("%s: a tip inside <p> near %q", name, s[loc[0]:min(loc[0]+60, len(s))])
			}
		}
	}
}
