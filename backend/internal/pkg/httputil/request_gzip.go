package httputil

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"mime"
	"net/http"
	"strings"
)

const (
	minGzipRequestBytes = 128 << 10
	maxGzipRequestBytes = 32 << 20
)

type requestGzipOriginalBytesKey struct{}

// GzipJSONRequest compresses a replayable JSON request in place. Callers must
// explicitly opt in only for upstreams known to accept Content-Encoding: gzip.
// It never changes JSON content or retries a request. Small, oversized, already
// encoded, signed/digested, or poorly compressible bodies are left untouched.
// Failures leave the original body readable so this optimization is optional.
func GzipJSONRequest(req *http.Request) bool {
	if req == nil || req.Method != http.MethodPost || req.Body == nil || req.GetBody == nil ||
		req.ContentLength < minGzipRequestBytes || req.ContentLength > maxGzipRequestBytes ||
		len(req.TransferEncoding) != 0 || len(req.Trailer) != 0 || req.Context().Err() != nil {
		return false
	}
	for _, name := range []string{"Content-Encoding", "Content-MD5", "Digest", "Content-Digest", "Signature", "Signature-Input"} {
		if req.Header.Get(name) != "" {
			return false
		}
	}
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return false
	}

	source, err := req.GetBody()
	if err != nil || source == nil {
		return false
	}
	defer source.Close()
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return false
	}
	// Bound work even if a custom GetBody does not match ContentLength.
	n, copyErr := io.Copy(writer, io.LimitReader(source, req.ContentLength+1))
	closeErr := writer.Close()
	if copyErr != nil || closeErr != nil || n != req.ContentLength || req.Context().Err() != nil ||
		int64(compressed.Len()) > req.ContentLength*95/100 {
		return false
	}

	originalBytes := req.ContentLength
	payload := compressed.Bytes()
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(payload))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	}
	req.ContentLength = int64(len(payload))
	req.Header = req.Header.Clone()
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Del("Content-Length")
	*req = *req.WithContext(context.WithValue(req.Context(), requestGzipOriginalBytesKey{}, originalBytes))
	return true
}

// RequestBodyBytesBeforeGzip returns the original size for diagnostics without
// retaining request content. Unknown lengths remain -1.
func RequestBodyBytesBeforeGzip(req *http.Request) int64 {
	if req == nil {
		return -1
	}
	if size, ok := req.Context().Value(requestGzipOriginalBytesKey{}).(int64); ok {
		return size
	}
	// Outbound requests use ContentLength=0 for an unknown non-empty Body too.
	if req.ContentLength == 0 && req.Body != nil && req.Body != http.NoBody {
		return -1
	}
	return req.ContentLength
}
