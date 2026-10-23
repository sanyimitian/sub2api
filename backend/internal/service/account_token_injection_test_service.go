package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const tokenInjectionInstructions = "Reply with only OK."

// TokenInjectionSample reports provider usage, not a claim about hidden prompt text.
// InputTokens includes cached tokens, following the OpenAI protocol.
type TokenInjectionSample struct {
	Index         int             `json:"index"`
	Repeat        bool            `json:"repeat"`
	LocalTokens   int             `json:"local_tokens"`
	InputTokens   int             `json:"input_tokens"`
	CacheRead     int             `json:"cache_read_tokens"`
	CacheWrite    int             `json:"cache_write_tokens"`
	OutputTokens  int             `json:"output_tokens"`
	ExtraTokens   int             `json:"extra_tokens"`
	FirstTokenMS  *int64          `json:"first_token_ms"`
	DurationMS    int64           `json:"duration_ms"`
	ResponseModel string          `json:"response_model,omitempty"`
	RawUsage      json.RawMessage `json:"raw_usage"`
}

type TokenInjectionResult struct {
	AccountID      int64                  `json:"account_id"`
	RequestedModel string                 `json:"requested_model"`
	UpstreamModel  string                 `json:"upstream_model"`
	Protocol       string                 `json:"protocol"`
	Tokenizer      string                 `json:"tokenizer"`
	Status         string                 `json:"status"`
	EstimateTokens *int                   `json:"estimate_tokens"`
	Slope          float64                `json:"slope"`
	MaxResidual    float64                `json:"max_residual"`
	RepeatDelta    int                    `json:"repeat_delta"`
	Warnings       []string               `json:"warnings"`
	Samples        []TokenInjectionSample `json:"samples"`
}

type tokenInjectionTarget struct {
	account, credential         *Account
	model, protocol, url, token string
	oauth                       bool
}

// TestTokenInjection sends five bounded, sequential requests directly to the
// selected account. It does not schedule another account or recover account state.
func (s *AccountTestService) TestTokenInjection(c *gin.Context, accountID int64, model, protocol string) error {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.Flush()
	fail := func(code, message string) error {
		s.sendEvent(c, TestEvent{Type: "error", Code: code, Error: message})
		return errors.New(message)
	}
	if _, loaded := s.tokenInjectionTests.LoadOrStore(accountID, struct{}{}); loaded {
		return fail("already_running", "A token injection test is already running for this account")
	}
	defer s.tokenInjectionTests.Delete(accountID)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
	defer cancel()
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return fail("account_unavailable", "Account is unavailable")
	}
	target, err := s.prepareTokenInjectionTarget(ctx, account, model, protocol)
	if err != nil {
		return fail("invalid_target", err.Error())
	}
	codec, err := openAIInputTokensCodecForModel(target.model)
	if err != nil {
		return fail("tokenizer_unavailable", "Could not initialize the local tokenizer")
	}
	result := TokenInjectionResult{
		AccountID: accountID, RequestedModel: model, UpstreamModel: target.model,
		Protocol: target.protocol, Tokenizer: string(openAIInputTokensEncodingForModel(target.model)),
		Samples: make([]TokenInjectionSample, 0, 5), Warnings: []string{},
	}
	s.sendEvent(c, TestEvent{Type: "injection_start", Data: result})
	for i, size := range []int{0, 256, 1024, 4096, 4096} {
		if ctx.Err() != nil {
			return fail("cancelled", "Token injection test cancelled or timed out")
		}
		prompt := "Reply with only OK. Ignore the data between the tags.\n<data>" + strings.Repeat(" a", size) + "</data>"
		localTokens, countErr := codec.Count(prompt)
		if countErr != nil {
			return fail("tokenizer_unavailable", "Could not count the diagnostic input")
		}
		// OAuth requires instructions. Count the exact small string we send;
		// do not inject the normal connectivity test's large Codex base prompt.
		if target.oauth {
			n, countErr := codec.Count(tokenInjectionInstructions)
			if countErr != nil {
				return fail("tokenizer_unavailable", "Could not count the diagnostic instructions")
			}
			localTokens += n
		}
		s.sendEvent(c, TestEvent{Type: "injection_progress", Data: map[string]int{"index": i + 1, "total": 5}})
		sample, sampleErr := s.runTokenInjectionSample(ctx, target, prompt)
		if sampleErr != nil {
			return fail("sample_failed", sampleErr.Error())
		}
		sample.Index, sample.Repeat, sample.LocalTokens = i+1, i == 4, localTokens
		sample.ExtraTokens = sample.InputTokens - localTokens
		result.Samples = append(result.Samples, sample)
		s.sendEvent(c, TestEvent{Type: "injection_sample", Data: sample})
	}
	analyzeTokenInjection(&result)
	s.sendEvent(c, TestEvent{Type: "injection_complete", Success: true, Data: result})
	return nil
}

func (s *AccountTestService) prepareTokenInjectionTarget(ctx context.Context, account *Account, model, protocol string) (tokenInjectionTarget, error) {
	t := tokenInjectionTarget{account: account, credential: account}
	if account == nil || account.Platform != PlatformOpenAI || account.IsSyntheticUITest() {
		return t, errors.New("Token injection testing requires an OpenAI text account")
	}
	if account.IsCredentialShadow() {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return t, errors.New("Could not resolve account credentials")
		}
		t.credential = resolved
	}
	t.model = account.GetMappedModel(strings.TrimSpace(model))
	if t.model == "" || isOpenAIImageModel(t.model) || strings.Contains(strings.ToLower(t.model), "embedding") {
		return t, errors.New("Select a text generation model")
	}
	if protocol == "" || protocol == "auto" {
		protocol = "responses"
		if t.credential.Type == AccountTypeAPIKey && !openai_compat.ShouldUseResponsesAPI(account.Extra) {
			protocol = "chat"
		}
	}
	if protocol != "responses" && protocol != "chat" {
		return t, errors.New("Unsupported test protocol")
	}
	t.protocol = protocol
	switch {
	case t.credential.IsOAuth():
		if protocol != "responses" {
			return t, errors.New("OAuth accounts require the Responses protocol")
		}
		t.oauth, t.url = true, chatgptCodexAPIURL
		t.model = normalizeOpenAIModelForUpstream(t.credential, t.model)
		t.token = t.credential.GetOpenAIAccessToken()
		if t.token == "" && !t.credential.IsOpenAIAgentIdentity() {
			return t, errors.New("No access token available")
		}
	case t.credential.Type == AccountTypeAPIKey:
		t.token = t.credential.GetOpenAIProtocolAPIKey()
		if t.token == "" {
			return t, errors.New("No API key available")
		}
		base, err := s.validateUpstreamBaseURL(t.credential.GetOpenAIBaseURL())
		if err != nil {
			return t, errors.New("Account upstream URL is invalid or not allowed")
		}
		t.url = buildOpenAIResponsesURLForPlatform(t.credential.Platform, base)
		if protocol == "chat" {
			t.url = buildOpenAIChatCompletionsURL(base)
		}
	default:
		return t, errors.New("Unsupported account type")
	}
	return t, nil
}

func (s *AccountTestService) runTokenInjectionSample(ctx context.Context, target tokenInjectionTarget, prompt string) (TokenInjectionSample, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	payload := map[string]any{"model": target.model, "stream": true}
	if target.protocol == "chat" {
		payload["messages"] = []map[string]any{{"role": "user", "content": prompt}}
		payload["stream_options"] = map[string]bool{"include_usage": true}
		payload["max_completion_tokens"] = 128
	} else {
		payload["input"] = []map[string]any{{"role": "user", "content": prompt}}
		payload["store"] = false
		if target.oauth {
			payload["instructions"] = tokenInjectionInstructions
		} else {
			payload["max_output_tokens"] = 128
		}
	}
	// Avoid unsupported reasoning parameters on older/non-reasoning models.
	lowerModel := strings.ToLower(target.model)
	if strings.HasPrefix(lowerModel, "gpt-5") || strings.HasPrefix(lowerModel, "gpt-6") || strings.HasPrefix(lowerModel, "o3") || strings.HasPrefix(lowerModel, "o4") {
		if target.protocol == "chat" {
			payload["reasoning_effort"] = "low"
		} else {
			payload["reasoning"] = map[string]string{"effort": "low"}
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return TokenInjectionSample{}, errors.New("Could not build diagnostic request")
	}
	req, err := http.NewRequestWithContext(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI), http.MethodPost, target.url, bytes.NewReader(body))
	if err != nil {
		return TokenInjectionSample{}, errors.New("Could not create diagnostic request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "sub2api-token-injection-test/1.0")
	if target.credential.IsOpenAIAgentIdentity() {
		headers, authErr := buildAgentIdentityAuthenticationHeaders(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, target.credential)
		if authErr != nil {
			return TokenInjectionSample{}, errors.New("Could not authenticate the selected account")
		}
		for k, values := range headers {
			for _, value := range values {
				req.Header.Add(k, value)
			}
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+target.token)
	}
	if target.oauth {
		req.Host = "chatgpt.com"
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		identity := resolveCodexOutboundIdentity("")
		req.Header.Set("Originator", identity.originator)
		req.Header.Set("User-Agent", identity.userAgent)
		setOpenAIChatGPTAccountHeaders(req.Header, target.credential)
		enforceCodexIdentityHeadersWithUA(req.Header, target.credential.GetOpenAIUserAgent())
	}
	target.credential.ApplyHeaderOverrides(req.Header)
	proxyURL := ""
	if target.account.ProxyID != nil && target.account.Proxy != nil {
		proxyURL = target.account.Proxy.URL()
	}
	started := time.Now()
	resp, err := s.doOpenAIAccountTestUpstream(req, proxyURL, target.account, true)
	if err != nil {
		// Never return transport errors containing upstream URLs/proxy credentials.
		return TokenInjectionSample{}, errors.New("Upstream request failed or timed out; test stopped")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return TokenInjectionSample{}, fmt.Errorf("Upstream returned HTTP %d; test stopped", resp.StatusCode)
	}
	return readTokenInjectionSample(resp, started)
}

func readTokenInjectionSample(resp *http.Response, started time.Time) (TokenInjectionSample, error) {
	sample := TokenInjectionSample{}
	const maxBytes = 2 << 20
	limited := &io.LimitedReader{R: resp.Body, N: maxBytes + 1}
	reader := bufio.NewReader(limited)
	first, err := reader.Peek(1)
	if err != nil {
		return sample, errors.New("Upstream returned an empty response")
	}
	complete, hasUsage := false, false
	consume := func(raw []byte, streaming bool) error {
		if !gjson.ValidBytes(raw) {
			return errors.New("Upstream returned invalid JSON")
		}
		event := gjson.ParseBytes(raw)
		typ := event.Get("type").String()
		if event.Get("error").Exists() && event.Get("error").Type != gjson.Null || typ == "error" || typ == "response.failed" || typ == "response.incomplete" {
			return errors.New("Upstream did not complete the diagnostic response")
		}
		data := event
		if event.Get("response").IsObject() {
			data = event.Get("response")
		}
		if model := data.Get("model").String(); model != "" {
			sample.ResponseModel = model
		}
		visible := typ == "response.output_text.delta" && event.Get("delta").String() != ""
		for _, choice := range event.Get("choices").Array() {
			visible = visible || choice.Get("delta.content").String() != ""
			if finish := choice.Get("finish_reason"); finish.Exists() && finish.Type != gjson.Null && finish.String() != "" {
				if finish.String() != "stop" {
					return errors.New("Upstream stopped before completing the diagnostic response")
				}
				complete = true
			}
		}
		if streaming && visible && sample.FirstTokenMS == nil {
			ms := time.Since(started).Milliseconds()
			sample.FirstTokenMS = &ms
		}
		if typ == "response.completed" || !streaming && data.Get("status").String() == "completed" {
			complete = true
		}
		usage := data.Get("usage")
		// Ignore provisional Responses usage (e.g. response.created). Only
		// the terminal response can establish a complete input count.
		if usage.IsObject() && complete && (typ == "" || typ == "response.completed") {
			if err := assignTokenInjectionUsage(&sample, usage); err != nil {
				return err
			}
			hasUsage = true
		}
		return nil
	}
	if first[0] == '{' || strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		raw, readErr := io.ReadAll(reader)
		if readErr != nil || limited.N == 0 {
			return sample, errors.New("Could not read bounded upstream response")
		}
		if err := consume(raw, false); err != nil {
			return sample, err
		}
	} else {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		var dataLines []string
		flush := func() error {
			if len(dataLines) == 0 {
				return nil
			}
			data := strings.Join(dataLines, "\n")
			dataLines = nil
			if data == "[DONE]" {
				return nil
			}
			return consume([]byte(data), true)
		}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if err := flush(); err != nil {
					return sample, err
				}
				// Responses completes with usage in its terminal event. Chat
				// sends usage after finish_reason. Do not wait for a keep-alive
				// upstream to close an already completed stream.
				if complete && hasUsage {
					break
				}
			} else if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if scanner.Err() != nil || limited.N == 0 {
			return sample, errors.New("Upstream stream was interrupted or exceeded the diagnostic limit")
		}
		if err := flush(); err != nil {
			return sample, err
		}
	}
	if !complete || !hasUsage {
		return sample, errors.New("Upstream did not return complete input token usage; cannot estimate injection")
	}
	sample.DurationMS = time.Since(started).Milliseconds()
	return sample, nil
}

func assignTokenInjectionUsage(sample *TokenInjectionSample, usage gjson.Result) error {
	count := func(required bool, paths ...string) (int, error) {
		for _, path := range paths {
			v := usage.Get(path)
			if !v.Exists() {
				continue
			}
			if v.Type != gjson.Number || v.Num < 0 || v.Num > 1e9 || math.Trunc(v.Num) != v.Num {
				return 0, errors.New("Upstream returned invalid token usage")
			}
			return int(v.Num), nil
		}
		if required {
			return 0, errors.New("Upstream omitted input token usage")
		}
		return 0, nil
	}
	var err error
	if sample.InputTokens, err = count(true, "input_tokens", "prompt_tokens"); err != nil {
		return err
	}
	if sample.OutputTokens, err = count(false, "output_tokens", "completion_tokens"); err != nil {
		return err
	}
	if sample.CacheRead, err = count(false, "input_tokens_details.cached_tokens", "prompt_tokens_details.cached_tokens", "cache_read_input_tokens"); err != nil {
		return err
	}
	if sample.CacheWrite, err = count(false, "input_tokens_details.cache_write_tokens", "prompt_tokens_details.cache_write_tokens", "cache_creation_input_tokens"); err != nil {
		return err
	}
	if sample.CacheRead+sample.CacheWrite > sample.InputTokens {
		return errors.New("Upstream cache usage exceeds total input; incompatible token accounting")
	}
	if len(usage.Raw) > 16<<10 {
		return errors.New("Upstream usage exceeds the diagnostic limit")
	}
	sample.RawUsage = json.RawMessage(usage.Raw)
	return nil
}

func analyzeTokenInjection(result *TokenInjectionResult) {
	result.Status, result.EstimateTokens = "inconclusive", nil
	if len(result.Samples) != 5 {
		return
	}
	base := result.Samples[:4]
	var mx, my, covariance, variance float64
	for _, s := range base {
		mx += float64(s.LocalTokens) / 4
		my += float64(s.InputTokens) / 4
	}
	for _, s := range base {
		x := float64(s.LocalTokens) - mx
		covariance += x * (float64(s.InputTokens) - my)
		variance += x * x
	}
	if variance == 0 {
		return
	}
	result.Slope = covariance / variance
	intercept := my - result.Slope*mx
	for _, s := range base {
		result.MaxResidual = math.Max(result.MaxResidual, math.Abs(float64(s.InputTokens)-result.Slope*float64(s.LocalTokens)-intercept))
	}
	result.RepeatDelta = result.Samples[4].InputTokens - result.Samples[3].InputTokens
	if math.Abs(result.Slope-1) > 0.05 {
		result.Warnings = append(result.Warnings, "slope_mismatch")
	}
	if result.MaxResidual > 32 || math.Abs(float64(result.RepeatDelta)) > 32 {
		result.Warnings = append(result.Warnings, "unstable_usage")
	}
	models := map[string]bool{}
	for _, s := range result.Samples {
		if s.ResponseModel != "" {
			models[s.ResponseModel] = true
		}
	}
	if len(models) > 1 {
		result.Warnings = append(result.Warnings, "model_changed")
	}
	if intercept < -32 {
		result.Warnings = append(result.Warnings, "negative_overhead")
	}
	if len(result.Warnings) == 0 {
		estimate := int(math.Round(math.Max(0, intercept)))
		result.EstimateTokens, result.Status = &estimate, "stable"
	}
}
