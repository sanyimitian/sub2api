package httputil

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gzipTestRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/v1/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	return req
}

func TestGzipJSONRequestPreservesBodyAndRedirectReplay(t *testing.T) {
	original := []byte(`{"input":"` + strings.Repeat("unchanged context ", 20000) + `"}`)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Content-Encoding") != "gzip" || r.ContentLength <= 0 || r.ContentLength >= int64(len(original)) {
			t.Errorf("incorrect compressed request headers: encoding=%q length=%d", r.Header.Get("Content-Encoding"), r.ContentLength)
		}
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		decoded, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil || !bytes.Equal(decoded, original) {
			t.Errorf("request content changed: %v", err)
		}
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	req := gzipTestRequest(t, original)
	req.URL, _ = req.URL.Parse(server.URL + "/start")
	borrowedHeader := req.Header
	if !GzipJSONRequest(req) {
		t.Fatal("expected compression")
	}
	if borrowedHeader.Get("Content-Encoding") != "" {
		t.Fatal("compression mutated shared headers")
	}
	if RequestBodyBytesBeforeGzip(req) != int64(len(original)) {
		t.Fatal("original size not preserved for diagnostics")
	}
	if GzipJSONRequest(req) {
		t.Fatal("already compressed request was compressed again")
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || requests != 2 {
		t.Fatalf("redirect did not replay the complete compressed body: status=%d requests=%d", resp.StatusCode, requests)
	}
}

func TestGzipJSONRequestLeavesUnsupportedRequestsReadable(t *testing.T) {
	body := []byte(strings.Repeat("a", minGzipRequestBytes))
	cases := map[string]func(*http.Request){
		"small":        func(r *http.Request) { r.ContentLength = minGzipRequestBytes - 1 },
		"oversized":    func(r *http.Request) { r.ContentLength = maxGzipRequestBytes + 1 },
		"unknown size": func(r *http.Request) { r.ContentLength = -1 },
		"no replay":    func(r *http.Request) { r.GetBody = nil },
		"not JSON":     func(r *http.Request) { r.Header.Set("Content-Type", "multipart/form-data") },
		"encoded":      func(r *http.Request) { r.Header.Set("Content-Encoding", "zstd") },
		"digest":       func(r *http.Request) { r.Header.Set("Content-Digest", "sha-256=:signed:") },
		"signature":    func(r *http.Request) { r.Header.Set("Signature-Input", "signed") },
		"chunked":      func(r *http.Request) { r.TransferEncoding = []string{"chunked"} },
		"trailers":     func(r *http.Request) { r.Trailer = http.Header{"Digest": []string{"signed"}} },
		"GET":          func(r *http.Request) { r.Method = http.MethodGet },
		"replay error": func(r *http.Request) {
			r.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("cannot replay") }
		},
		"length mismatch": func(r *http.Request) {
			r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("short")), nil }
		},
		"canceled": func(r *http.Request) {
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			*r = *r.WithContext(ctx)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			req := gzipTestRequest(t, body)
			change(req)
			if GzipJSONRequest(req) {
				t.Fatal("unexpected compression")
			}
			got, err := io.ReadAll(req.Body)
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("fallback consumed the original body: %v", err)
			}
		})
	}
}

func TestGzipJSONRequestSkipsIncompressibleBody(t *testing.T) {
	body := make([]byte, minGzipRequestBytes)
	if _, err := rand.Read(body); err != nil {
		t.Fatal(err)
	}
	req := gzipTestRequest(t, body)
	if GzipJSONRequest(req) || req.Header.Get("Content-Encoding") != "" {
		t.Fatal("compression with no size benefit must be skipped")
	}
	got, _ := io.ReadAll(req.Body)
	if !bytes.Equal(got, body) {
		t.Fatal("fallback body changed")
	}
}

func TestGzipJSONRequestActualSizeBoundaries(t *testing.T) {
	for _, size := range []int{minGzipRequestBytes - 1, minGzipRequestBytes, maxGzipRequestBytes, maxGzipRequestBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			original := []byte(`{"input":"` + strings.Repeat("x", size-len(`{"input":""}`)) + `"}`)
			req := gzipTestRequest(t, original)
			defer func() { _ = req.Body.Close() }()
			wantCompressed := size >= minGzipRequestBytes && size <= maxGzipRequestBytes
			if got := GzipJSONRequest(req); got != wantCompressed {
				t.Fatalf("size %d: compressed = %v, want %v", size, got, wantCompressed)
			}
			var reader io.Reader = req.Body
			if wantCompressed {
				decoded, err := gzip.NewReader(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer decoded.Close()
				reader = decoded
			}
			got, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("size %d: request content changed: %v", size, err)
			}
		})
	}
}

type gzipReplayReadError struct{}

func (gzipReplayReadError) Read(p []byte) (int, error) {
	return copy(p, "partial replay data"), errors.New("replay read failed")
}

func (gzipReplayReadError) Close() error { return nil }

func TestGzipJSONRequestReadFailurePreservesOriginalRequest(t *testing.T) {
	original := []byte(`{"input":"` + strings.Repeat("context ", 20000) + `"}`)
	req := gzipTestRequest(t, original)
	req.GetBody = func() (io.ReadCloser, error) { return gzipReplayReadError{}, nil }
	if GzipJSONRequest(req) {
		t.Fatal("failed replay must not replace the original request")
	}
	defer req.Body.Close()
	got, err := io.ReadAll(req.Body)
	if err != nil || !bytes.Equal(got, original) || req.ContentLength != int64(len(original)) || req.Header.Get("Content-Encoding") != "" {
		t.Fatalf("read failure modified the original request: %v", err)
	}
}

func TestRequestBodyBytesBeforeGzipDistinguishesUnknownAndEmpty(t *testing.T) {
	streamed, _ := http.NewRequest(http.MethodPost, "https://example.invalid", io.NopCloser(strings.NewReader("stream")))
	if RequestBodyBytesBeforeGzip(streamed) != -1 {
		t.Fatal("unknown streamed length was reported as empty")
	}
	empty, _ := http.NewRequest(http.MethodPost, "https://example.invalid", bytes.NewReader(nil))
	if RequestBodyBytesBeforeGzip(empty) != 0 {
		t.Fatal("known empty request lost its zero length")
	}
}

func BenchmarkGzipJSONRequest(b *testing.B) {
	body := []byte(`{"input":"` + strings.Repeat("function handle(request) { return request.payload; } ", 40000) + `"}`)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		req, _ := http.NewRequest(http.MethodPost, "https://example.invalid/v1/responses", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if !GzipJSONRequest(req) {
			b.Fatal("expected compression")
		}
		_ = req.Body.Close()
	}
}
