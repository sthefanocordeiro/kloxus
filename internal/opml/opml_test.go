package opml

import (
	"os"
	"strings"
	"testing"
)

func TestParseNestedAndCategories(t *testing.T) {
	x := `<opml version="1.0"><body>
	<outline text="Tech"><outline type="rss" text="A" xmlUrl="http://a.example/feed/" htmlUrl="https://a.example"/>
	<outline type="rss" title="B" text="b" xmlUrl="https://b.example/rss"/></outline>
	<outline type="rss" text="Top" xmlUrl="https://c.example/x"/></body></opml>`
	fs, err := Parse(strings.NewReader(x))
	if err != nil || len(fs) != 3 {
		t.Fatalf("got %v %v", fs, err)
	}
	if fs[0].Category != "Tech" || fs[1].Name != "B" || fs[2].Category != "" {
		t.Fatalf("bad parse: %+v", fs)
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", "<html/>", "<opml><body></body></opml>"} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
}

func TestKeyCollapsesSchemeWwwSlash(t *testing.T) {
	a, b := Key("http://www.Example.com/feed/"), Key("https://example.com/feed")
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
	if Key("https://example.com/other") == a {
		t.Fatal("distinct feeds collapsed")
	}
}

func TestParseRealFeedlyExport(t *testing.T) {
	f, err := os.Open("testdata/feedly.opml")
	if err != nil {
		t.Skip("no sample")
	}
	defer f.Close()
	fs, err := Parse(f)
	if err != nil || len(fs) != 45 {
		t.Fatalf("got %d feeds, err %v", len(fs), err)
	}
}
