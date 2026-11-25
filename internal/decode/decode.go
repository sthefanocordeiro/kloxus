// Package decode turns fetched HTTP bodies into UTF-8 readers: the
// BOM, the Content-Type charset label, and — for HTML — the <meta
// charset> prescan all resolve through x/net/html/charset, so a
// Latin-1 or Windows-1252 source never reaches stored items or the UI
// as mojibake (KLOX-79).
package decode

import (
	"bytes"
	"io"
	"regexp"

	"golang.org/x/net/html/charset"
)

// Reader returns body as a UTF-8 reader for HTML/XML/text content,
// honoring the HTTP Content-Type header (e.g. "text/html;
// charset=iso-8859-1"), a leading BOM, and the document's own charset
// hint (the HTML <meta> prescan). Anything undecidable or already
// UTF-8 passes through unchanged.
func Reader(body []byte, contentType string) io.Reader {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return bytes.NewReader(body)
	}
	return r
}

// xmlEncoding matches an encoding declaration inside the XML prolog.
var xmlEncoding = regexp.MustCompile(`<\?xml[^?]*encoding\s*=`)

// FeedReader is Reader for RSS/Atom bodies. When the XML prolog
// already declares an encoding the bytes pass through untouched — the
// feed parser's own CharsetReader applies the declared label, and a
// pre-decode here would double-decode. Only when the prolog is silent
// do the HTTP charset / BOM / UTF-8 fallback apply.
func FeedReader(body []byte, contentType string) io.Reader {
	head := body
	if len(head) > 1024 {
		head = head[:1024]
	}
	if xmlEncoding.Match(head) {
		return bytes.NewReader(body)
	}
	return Reader(body, contentType)
}
