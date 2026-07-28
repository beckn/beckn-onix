package decode

// decode_test.go — covers the codec registry: plain passthrough, gzip
// round-trip, decompression-bomb rejection at the cap, corrupt/unknown-encoding
// permanent errors, additive registration, and encoding inference from URLs.

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/crawler/catalog"
)

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecode_PlainPassthrough(t *testing.T) {
	in := []byte(`{"a":1}`)
	for _, enc := range []string{"", "json", "JSON"} {
		out, err := Decode(enc, in, 1<<20)
		if err != nil {
			t.Fatalf("enc %q: %v", enc, err)
		}
		if !bytes.Equal(out, in) {
			t.Fatalf("enc %q: got %s", enc, out)
		}
	}
}

func TestDecode_GzipRoundTrip(t *testing.T) {
	in := []byte(`{"catalogId":"p/c","resources":[]}`)
	out, err := Decode("gzip", gz(t, in), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("got %s, want %s", out, in)
	}
}

func TestDecode_GzipBombRejectedAtCap(t *testing.T) {
	big := make([]byte, 1<<20) // 1 MiB of zeros -> tiny gzip, inflates past a small cap
	_, err := Decode("gzip", gz(t, big), 4096)
	if err == nil {
		t.Fatal("expected oversize rejection")
	}
	if !catalog.IsPermanent(err) {
		t.Fatalf("bomb rejection must be permanent, got %v", err)
	}
}

func TestDecode_CorruptGzipPermanent(t *testing.T) {
	if _, err := Decode("gzip", []byte("this is not gzip"), 1<<20); err == nil || !catalog.IsPermanent(err) {
		t.Fatalf("corrupt gzip must be a permanent error, got %v", err)
	}
}

func TestDecode_UnknownEncodingPermanent(t *testing.T) {
	if _, err := Decode("zstd", []byte("x"), 1<<20); err == nil || !catalog.IsPermanent(err) {
		t.Fatalf("unknown encoding must be a permanent error, got %v", err)
	}
}

// A new format is additive: register one decoder and it works end-to-end with
// no change to fetch/verify/parse/push.
func TestDecode_NewCodecIsAdditive(t *testing.T) {
	codecs["fake"] = func(b []byte) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bytes.ToUpper(b))), nil
	}
	defer delete(codecs, "fake")
	out, err := Decode("fake", []byte("abc"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ABC" {
		t.Fatalf("got %s, want ABC", out)
	}
}

func TestEncodingFor(t *testing.T) {
	if got := EncodingFor("gzip", "https://x/c.json"); got != "gzip" {
		t.Fatalf("explicit encoding should win, got %q", got)
	}
	if got := EncodingFor("", "https://x/c.json.gzip"); got != "gzip" {
		t.Fatalf(".json.gzip suffix should infer gzip, got %q", got)
	}
	if got := EncodingFor("", "https://x/c.json"); got != "" {
		t.Fatalf("plain .json should infer no encoding, got %q", got)
	}
}
