package service

import (
	"net/http"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
)

// Request compression is an account capability, not a default assumption about
// OpenAI-compatible endpoints. OAuth/plugin transports keep their own wire format.
func prepareOpenAIRequestGzip(request *http.Request, account *Account) {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey ||
		account.Extra["openai_request_gzip"] != true {
		return
	}
	pkghttputil.GzipJSONRequest(request)
}
