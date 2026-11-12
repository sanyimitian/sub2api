package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// Account snapshots are supplied by the authenticated site's backend. The main
// site always replaces SiteName with the name attached to the verified token.
type benchmarkIdentity struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	SiteName string `json:"site_name"`
}

func validBenchmarkIdentity(identity benchmarkIdentity) bool {
	for _, field := range []struct {
		value string
		limit int
	}{{identity.Email, 254}, {identity.Username, 200}, {identity.SiteName, 100}} {
		if !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) > field.limit || strings.IndexFunc(field.value, unicode.IsControl) >= 0 {
			return false
		}
	}
	return true
}

func rewriteBenchmarkIdentity(c *gin.Context, update func(*benchmarkIdentity) error, trustBody bool) error {
	data := []byte(`{}`)
	var err error
	if c.Request.Body != nil {
		data, err = io.ReadAll(io.LimitReader(c.Request.Body, 16*1024+1))
		if err != nil || len(data) > 16*1024 {
			return errors.New("请求内容过大")
		}
		if len(data) == 0 {
			data = []byte(`{}`)
		}
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(data, &body) != nil || body == nil {
		return errors.New("请求格式不正确")
	}
	var identity benchmarkIdentity
	if raw, ok := body["_benchmark_identity"]; ok && trustBody {
		if json.Unmarshal(raw, &identity) != nil {
			return errors.New("用户来源信息格式不正确")
		}
	}
	if err := update(&identity); err != nil {
		return err
	}
	if !validBenchmarkIdentity(identity) {
		return errors.New("用户来源信息格式不正确")
	}
	body["_benchmark_identity"], _ = json.Marshal(identity)
	data, err = json.Marshal(body)
	if err != nil || len(data) > 16*1024 {
		return errors.New("请求内容过大")
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(data))
	c.Request.ContentLength = int64(len(data))
	return nil
}
