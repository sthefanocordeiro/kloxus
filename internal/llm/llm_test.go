package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestKeyStableAndNormalized(t *testing.T) {
	r1 := Request{Model: "m", Template: "t", User: "hello   world\n"}
	r2 := Request{Model: "m", Template: "t", User: "hello world"}
	if Key(r1) != Key(r2) {
		t.Error("whitespace normalization broke the key")
	}
	r3 := Request{Model: "m", Template: "t", User: "different"}
	if Key(r1) == Key(r3) {
		t.Error("different inputs share a key")
	}
}

func TestReplayHitAndMiss(t *testing.T) {
	req := Request{Model: "m", Template: "t", User: "u"}
	fsys := fstest.MapFS{
		"llm/" + Key(req) + ".json": {Data: []byte(`{"model":"m","text":"hi","input_tokens":10,"output_tokens":5}`)},
	}
	dump := t.TempDir()
	rp := Replay{FS: fsys, Dir: "llm", DumpDir: dump}

	resp, err := rp.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("hit: %v", err)
	}
	if resp.Text != "hi" || resp.InputTokens != 10 {
		t.Fatalf("resp = %+v", resp)
	}

	miss := Request{Model: "m", Template: "t", User: "other"}
	_, err = rp.Complete(context.Background(), miss)
	if !errors.Is(err, ErrMissingFixture) {
		t.Fatalf("miss error = %v", err)
	}
	if !strings.Contains(err.Error(), "missing LLM fixture") {
		t.Errorf("error should name the key: %v", err)
	}
	dumpFile := filepath.Join(dump, Key(miss)+".request.json")
	if _, err := os.Stat(dumpFile); err != nil {
		t.Errorf("expected request dump at %s: %v", dumpFile, err)
	}
}

func TestRecorderWritesFixture(t *testing.T) {
	dir := t.TempDir()
	rec := Recorder{
		Dir: dir,
		Inner: Func(func(_ context.Context, req Request) (Response, error) {
			return Response{Text: "ok", InputTokens: 3, OutputTokens: 2}, nil
		}),
	}
	req := Request{Model: "m", Template: "t", User: "u"}
	if _, err := rec.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, Key(req)+".json"))
	if err != nil {
		t.Fatalf("fixture not written: %v", err)
	}
	if !strings.Contains(string(data), `"text": "ok"`) {
		t.Errorf("fixture content = %s", data)
	}
}
