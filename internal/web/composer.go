package web

// Composer asset endpoints (KLOX-66 D3): image uploads stored in the
// workspace media table and served publicly at /m/{id}-{token}, plus
// the per-publication standard-message library.
import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// MediaMaxBytes caps one uploaded image — large enough for a retina
// newsletter banner, small enough to keep email bodies sane.
const MediaMaxBytes = 2 << 20 // 2 MiB

// mediaTypes maps an allowed upload MIME type to its magic prefix.
var mediaTypes = map[string][]byte{
	"image/png":  {0x89, 'P', 'N', 'G'},
	"image/jpeg": {0xFF, 0xD8},
	"image/gif":  {'G', 'I', 'F'},
	"image/webp": {'R', 'I', 'F', 'F'},
}

// apiMediaUpload accepts a JSON upload ({content_type, data_b64, name})
// so the request stays inside the API's JSON-only contract — no
// multipart. Bytes are validated against the declared type's magic
// prefix, never trusted from the header alone.
func (s *Server) apiMediaUpload(w http.ResponseWriter, r *http.Request) {
	iss, err := s.issue(r)
	if err != nil {
		apiErr(w, 404, "not_found", "issue not found")
		return
	}
	s.saveMediaUpload(w, r, iss.PublicationID)
}

// apiPubMediaUpload is the same upload scoped to a publication instead
// of an issue — the template editor (KLOX-72) attaches banner art
// before any edition exists. The media row is publication-scoped
// either way.
func (s *Server) apiPubMediaUpload(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		apiErr(w, 404, "not_found", "publication not found")
		return
	}
	s.saveMediaUpload(w, r, pub.ID)
}

func (s *Server) saveMediaUpload(w http.ResponseWriter, r *http.Request, pubID int64) {
	var in struct {
		ContentType string `json:"content_type"`
		DataB64     string `json:"data_b64"`
		Name        string `json:"name"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	magic, ok := mediaTypes[in.ContentType]
	if !ok {
		apiErr(w, 400, "validation", "image must be png, jpeg, gif or webp")
		return
	}
	data, err := base64.StdEncoding.DecodeString(in.DataB64)
	if err != nil {
		apiErr(w, 400, "validation", "data_b64 is not valid base64")
		return
	}
	if len(data) == 0 || len(data) > MediaMaxBytes {
		apiErr(w, 413, "too_large", "image must be under 2 MB")
		return
	}
	if len(data) < len(magic) || !bytes.HasPrefix(data, magic) {
		apiErr(w, 400, "validation", "file content does not match its declared type")
		return
	}
	data = stripImageMetadata(in.ContentType, data)
	tok := make([]byte, 8)
	rand.Read(tok)
	token := hex.EncodeToString(tok)
	id, err := db(r).SaveMedia(r.Context(), pubID, token, in.ContentType, data)
	if apiDBErr(w, r, err, "media") {
		return
	}
	apiJSON(w, 201, map[string]any{"media": map[string]any{
		"id": id, "url": fmt.Sprintf("/m/%d-%s", id, token),
		"content_type": in.ContentType, "bytes": len(data),
	}})
}

// apiMediaGet serves an uploaded image. The URL carries an unguessable
// token — the image is meant to be public: email clients and reader
// pages fetch it with no workspace cookie. The bytes for a given URL
// never change, so the browser caches immutably; `private` (not
// `public`) keeps shared caches — email image proxies, CDNs — from
// retaining media whose publication was deleted (D69).
func (s *Server) apiMediaGet(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("id")
	i := strings.IndexByte(raw, '-')
	if i < 0 {
		apiErr(w, 404, "not_found", "media not found")
		return
	}
	id, err := strconv.ParseInt(raw[:i], 10, 64)
	if err != nil {
		apiErr(w, 404, "not_found", "media not found")
		return
	}
	ct, data, err := s.d.Pool.GetMedia(r.Context(), id, raw[i+1:])
	if err == sql.ErrNoRows {
		apiErr(w, 404, "not_found", "media not found")
		return
	}
	if err != nil {
		apiErr(w, 500, "internal", "could not read media")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Write(data)
}

// messageMax is the library size cap per publication.
const messageMax = 50

// apiMessages lists the publication's standard-message library.
func (s *Server) apiMessages(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		apiErr(w, 404, "not_found", "publication not found")
		return
	}
	msgs, err := db(r).StandardMessages(r.Context(), pub.ID)
	if apiDBErr(w, r, err, "messages") {
		return
	}
	apiJSON(w, 200, map[string]any{"messages": msgs})
}

// apiMessageCreate adds a library entry — author-authored text, never
// model output.
func (s *Server) apiMessageCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if _, err := apiDecode(r, &in); err != nil {
		apiErr(w, 400, "bad_json", "request body is not valid JSON")
		return
	}
	in.Name, in.Body = strings.TrimSpace(in.Name), strings.TrimSpace(in.Body)
	pub, err := s.pub(r)
	if err != nil {
		apiErr(w, 404, "not_found", "publication not found")
		return
	}
	if in.Name == "" || len(in.Name) > 80 || in.Body == "" || len(in.Body) > 8000 {
		apiErr(w, 400, "validation", "message needs a name (≤80) and body (≤8000)")
		return
	}
	if msgs, err := db(r).StandardMessages(r.Context(), pub.ID); err == nil && len(msgs) >= messageMax {
		apiErr(w, 409, "blocked", "the message library is full (50)")
		return
	}
	id, err := db(r).CreateStandardMessage(r.Context(), pub.ID, in.Name, in.Body)
	if apiDBErr(w, r, err, "messages") {
		return
	}
	apiJSON(w, 201, map[string]any{"message": store.StandardMessage{ID: id, Name: in.Name, Body: in.Body}})
}

// apiMessageDelete removes a library entry.
func (s *Server) apiMessageDelete(w http.ResponseWriter, r *http.Request) {
	pub, err := s.pub(r)
	if err != nil {
		apiErr(w, 404, "not_found", "publication not found")
		return
	}
	mid, err := strconv.ParseInt(r.PathValue("mid"), 10, 64)
	if err != nil {
		apiErr(w, 400, "validation", "message id must be a number")
		return
	}
	err = db(r).DeleteStandardMessage(r.Context(), pub.ID, mid)
	if err == sql.ErrNoRows {
		apiErr(w, 404, "not_found", "message not found")
		return
	}
	if apiDBErr(w, r, err, "messages") {
		return
	}
	w.WriteHeader(204)
}
