package decode

import (
	"io"
	"strings"
	"testing"
)

func read(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReaderHonorsContentTypeCharset(t *testing.T) {
	// "atenção" as ISO-8859-1 bytes — the HTTP header is the only hint.
	body := []byte("<p>aten\xe7\xe3o</p>")
	if got := read(t, Reader(body, "text/html; charset=iso-8859-1")); got != "<p>atenção</p>" {
		t.Fatalf("got %q", got)
	}
}

func TestReaderMetaPrescan(t *testing.T) {
	// Windows-1252 body, silent HTTP header — the <meta charset>
	// prescan must carry the label.
	body := []byte("<html><head><meta charset=\"windows-1252\"><body><p>a\xe7\xe3o</p>")
	if got := read(t, Reader(body, "text/html")); !strings.Contains(got, "ação") {
		t.Fatalf("got %q", got)
	}
}

func TestReaderUTF8Passthrough(t *testing.T) {
	body := []byte("<p>atenção</p>")
	if got := read(t, Reader(body, "text/html; charset=utf-8")); got != "<p>atenção</p>" {
		t.Fatalf("utf-8 body altered: %q", got)
	}
}

func TestFeedReaderPrologDeclWins(t *testing.T) {
	// The prolog declares ISO-8859-1 — bytes must pass through raw so
	// the feed parser's own CharsetReader decodes once, not twice.
	body := []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss><t>a\xe7</t></rss>")
	if got := read(t, FeedReader(body, "text/xml; charset=utf-8")); got != string(body) {
		t.Fatalf("declared feed pre-decoded: %q", got)
	}
}

func TestFeedReaderHTTPCharsetWhenPrologSilent(t *testing.T) {
	body := []byte("<rss><channel><title>Servi\xe7o</title></channel></rss>")
	if got := read(t, FeedReader(body, "text/xml; charset=iso-8859-1")); !strings.Contains(got, "Serviço") {
		t.Fatalf("got %q", got)
	}
}

func TestFeedReaderBOM(t *testing.T) {
	// UTF-16LE BOM + no other hints.
	body := append([]byte{0xFF, 0xFE}, []byte("<\x00r\x00s\x00s\x00>\x00")...)
	// the decoded BOM survives as U+FEFF — parsers tolerate it.
	if got := read(t, FeedReader(body, "text/xml")); !strings.HasSuffix(got, "<rss>") {
		t.Fatalf("got %q", got)
	}
}
