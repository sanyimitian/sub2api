package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

const benchmarkPublicMaxBytes = 256 * 1024

type benchmarkPublicVisitor struct {
	limiter *rate.Limiter
	seen    time.Time
}

// Always-on, bounded per-process protection also applies when Redis is unavailable.
// The normal site-wide public-IP limiter is applied separately by the route.
type benchmarkPublicGuard struct {
	mu       sync.Mutex
	visitors map[string]*benchmarkPublicVisitor
	global   *rate.Limiter
	slots    chan struct{}
}

func newBenchmarkPublicGuard() *benchmarkPublicGuard {
	return &benchmarkPublicGuard{visitors: make(map[string]*benchmarkPublicVisitor), global: rate.NewLimiter(2, 20), slots: make(chan struct{}, 4)}
}

func (g *benchmarkPublicGuard) allow(ip string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	visitor := g.visitors[ip]
	if visitor == nil {
		if len(g.visitors) >= 4096 {
			for key, old := range g.visitors {
				if now.Sub(old.seen) > 5*time.Minute {
					delete(g.visitors, key)
				}
			}
			if len(g.visitors) >= 4096 {
				return false
			}
		}
		visitor = &benchmarkPublicVisitor{limiter: rate.NewLimiter(1, 10)}
		g.visitors[ip] = visitor
	}
	visitor.seen = now
	return visitor.limiter.AllowN(now, 1) && g.global.AllowN(now, 1)
}

func (h *ModelBenchmarkHandler) PublicResults(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	if c.Request.Method != http.MethodGet {
		c.Header("Allow", "GET")
		response.Error(c, http.StatusMethodNotAllowed, "Only GET is supported")
		return
	}
	if !h.publicGuard.allow(middleware.SecurityClientIP(c), time.Now()) {
		c.Header("Retry-After", "60")
		response.Error(c, http.StatusTooManyRequests, "Too many public result requests")
		return
	}
	// This endpoint has no client-controlled target, filter, pagination or JSONP.
	// Reject bodies without reading them so slow clients cannot occupy worker slots.
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || c.Request.ContentLength != 0 || len(c.Request.TransferEncoding) != 0 {
		response.BadRequest(c, "Public results do not accept query parameters or a request body")
		return
	}
	select {
	case h.publicGuard.slots <- struct{}{}:
		defer func() { <-h.publicGuard.slots }()
	default:
		c.Header("Retry-After", "5")
		response.Error(c, http.StatusTooManyRequests, "Public results are busy; retry shortly")
		return
	}
	u, err := url.Parse(h.baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || h.token == "" {
		response.Error(c, http.StatusServiceUnavailable, "Public results are unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+"/public-results", nil)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Public results are unavailable")
		return
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Accept", "application/json")
	res, err := h.client.Do(req)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Public results are unavailable")
		return
	}
	defer res.Body.Close()
	// Anonymous callers never receive internal worker errors or redirect targets.
	if res.StatusCode != http.StatusOK {
		response.Error(c, http.StatusServiceUnavailable, "Public results are unavailable")
		return
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, benchmarkPublicMaxBytes+1))
	if err != nil || len(data) > benchmarkPublicMaxBytes {
		response.Error(c, http.StatusBadGateway, "Invalid public results response")
		return
	}
	feed, err := decodeBenchmarkPublicFeed(data)
	if err != nil {
		response.Error(c, http.StatusBadGateway, "Invalid public results response")
		return
	}
	c.JSON(http.StatusOK, response.Response{Code: 0, Message: "success", Data: feed})
}

// Typed projection is a second allowlist at the anonymous application boundary.
// Unknown fields (including future worker credentials, targets or answers) drop out.
type benchmarkPublicCounts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Passed    int `json:"passed"`
}

type benchmarkPublicUsage struct {
	Input  int64 `json:"input_tokens"`
	Output int64 `json:"output_tokens"`
	Total  int64 `json:"total_tokens"`
}

type benchmarkPublicResult struct {
	ID               string                           `json:"id"`
	ScheduleID       string                           `json:"schedule_id"`
	ScheduleName     string                           `json:"schedule_name"`
	Model            string                           `json:"model"`
	BaseURL          string                           `json:"base_url,omitempty"`
	Effort           string                           `json:"effort"`
	Status           string                           `json:"status"`
	CreatedAt        time.Time                        `json:"created_at"`
	FinishedAt       time.Time                        `json:"finished_at"`
	Total            int                              `json:"total"`
	Completed        int                              `json:"completed"`
	Passed           int                              `json:"passed"`
	PassRate         *float64                         `json:"pass_rate"`
	Categories       map[string]benchmarkPublicCounts `json:"categories"`
	SuiteDay         string                           `json:"suite_day"`
	SuiteRevision    int                              `json:"suite_revision"`
	SuiteFingerprint string                           `json:"suite_fingerprint"`
	Elapsed          float64                          `json:"elapsed_seconds"`
	Usage            benchmarkPublicUsage             `json:"usage"`
	APIErrors        int                              `json:"api_errors"`
	GradingErrors    int                              `json:"grading_errors"`
}

type benchmarkPublicFeed struct {
	Version     int                     `json:"version"`
	GeneratedAt time.Time               `json:"generated_at"`
	Limit       int                     `json:"limit"`
	Items       []benchmarkPublicResult `json:"items"`
}

func benchmarkPublicText(value string, maximum int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximum && strings.IndexFunc(value, unicode.IsControl) < 0
}

func decodeBenchmarkPublicFeed(data []byte) (*benchmarkPublicFeed, error) {
	var feed benchmarkPublicFeed
	invalid := fmt.Errorf("invalid public results")
	if json.Unmarshal(data, &feed) != nil || feed.Version != 2 || feed.Limit != 20 || feed.GeneratedAt.IsZero() || feed.Items == nil || len(feed.Items) > 20 {
		return nil, invalid
	}
	ids := make(map[string]bool)
	for i := range feed.Items {
		r := &feed.Items[i]
		if r.BaseURL != "" {
			u, err := url.Parse(r.BaseURL)
			if err != nil || !benchmarkPublicText(r.BaseURL, 2048) || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return nil, invalid
			}
		}
		if !benchmarkID.MatchString(r.ID) || !benchmarkID.MatchString(r.ScheduleID) || ids[r.ID] || !benchmarkPublicText(r.ScheduleName, 100) || !benchmarkPublicText(r.Model, 200) {
			return nil, invalid
		}
		ids[r.ID] = true
		if r.Effort != "low" && r.Effort != "medium" && r.Effort != "high" && r.Effort != "xhigh" {
			return nil, invalid
		}
		if r.Status != "completed" && r.Status != "failed" && r.Status != "cancelled" && r.Status != "interrupted" {
			return nil, invalid
		}
		if r.CreatedAt.IsZero() || r.FinishedAt.IsZero() || r.SuiteRevision < 1 || len(r.SuiteFingerprint) != 64 || strings.Trim(r.SuiteFingerprint, "0123456789abcdef") != "" {
			return nil, invalid
		}
		if _, err := time.Parse("2006-01-02", r.SuiteDay); err != nil {
			return nil, invalid
		}
		if r.Elapsed < 0 || r.Elapsed > 31536000 || r.Total < 1 || r.Total > 100 || r.Completed < 0 || r.Completed > r.Total || r.Passed < 0 || r.Passed > r.Completed || len(r.Categories) != 3 {
			return nil, invalid
		}
		var totals benchmarkPublicCounts
		for _, category := range []string{"coding", "math", "reasoning"} {
			counts, ok := r.Categories[category]
			if !ok || counts.Total < 0 || counts.Total > 100 || counts.Completed < 0 || counts.Completed > counts.Total || counts.Passed < 0 || counts.Passed > counts.Completed {
				return nil, invalid
			}
			totals.Total += counts.Total
			totals.Completed += counts.Completed
			totals.Passed += counts.Passed
		}
		if totals.Total != r.Total || totals.Completed != r.Completed || totals.Passed != r.Passed {
			return nil, invalid
		}
		r.PassRate = nil
		if r.Status == "completed" {
			if r.Completed != r.Total {
				return nil, invalid
			}
			score := float64(r.Passed) / float64(r.Total) * 100
			r.PassRate = &score
		}
		if r.Usage.Input < 0 || r.Usage.Input > 1e12 || r.Usage.Output < 0 || r.Usage.Output > 1e12 || r.Usage.Total < 0 || r.Usage.Total > 1e12 || r.APIErrors < 0 || r.APIErrors > r.Total || r.GradingErrors < 0 || r.GradingErrors > r.Total {
			return nil, invalid
		}
	}
	sort.SliceStable(feed.Items, func(i, j int) bool { return feed.Items[i].FinishedAt.After(feed.Items[j].FinishedAt) })
	return &feed, nil
}
