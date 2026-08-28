package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// CodexModels serves the Codex models manifest for Codex clients.
//
// Codex CLI and the Codex desktop app refresh their model picker from
// GET {base_url}/models?client_version=... (custom provider mode) or
// GET /backend-api/codex/models (chatgpt_base_url mode). Both routes land
// here. Groups with explicit account model mappings are generated locally;
// otherwise ChatGPT manifests are proxied verbatim and custom API key manifests
// receive provider-compatibility normalization plus short-lived caching.
func (h *OpenAIGatewayHandler) CodexModels(c *gin.Context) {
	if c.Request.Context().Err() != nil {
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "invalid_request_error", "API key group is required")
		return
	}
	if apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex models manifest is only available for OpenAI and Composite groups")
		return
	}

	ifNoneMatch := c.GetHeader("If-None-Match")
	configuredManifest, configured, err := h.gatewayService.BuildGroupConfiguredCodexModelsManifest(
		c.Request.Context(),
		apiKey.Group,
		ifNoneMatch,
	)
	if err != nil {
		if c.Request.Context().Err() != nil {
			return
		}
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to build Codex models manifest")
		return
	}
	if configured {
		writeCodexModelsManifestResponse(c, configuredManifest)
		return
	}

	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	failedAccountIDs := make(map[int64]struct{})
	switchCount := 0
	cooldownGuardVetoCount := 0
	var lastUpstreamErr error
	reqLog := requestLogger(c, "handler.openai.codex_models")

	for {
		account, err := h.gatewayService.SelectAccountForModelWithExclusions(c.Request.Context(), apiKey.GroupID, "", "", failedAccountIDs)
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if lastUpstreamErr != nil {
				h.errorResponse(c, infraerrors.Code(lastUpstreamErr), "upstream_error", infraerrors.Message(lastUpstreamErr))
				return
			}
			h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI accounts")
			return
		}
		// 让 ops 错误日志携带实际选中的上游账号，便于定位失效账号（#4544）。
		setOpsSelectedAccount(c, account.ID, account.Platform)
		attemptCtx, cooldownAttempt, cooldownBlocked, cooldownErr := beginHandlerAPIKeyCooldownAttempt(
			c.Request.Context(), h.gatewayService, account, "", true,
		)
		if cooldownErr != nil {
			reqLog.Warn("openai.codex_models.api_key_cooldown_guard_failed", zap.Int64("account_id", account.ID), zap.Error(cooldownErr))
		}
		if cooldownBlocked {
			failedAccountIDs[account.ID] = struct{}{}
			cooldownGuardVetoCount++
			if cooldownGuardVetoCount >= maxAPIKeyCooldownGuardVetoAttempts {
				h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI accounts")
				return
			}
			continue
		}
		if cooldownAttempt != nil {
			cooldownAttempt.MarkRequestSent()
		}

		// The client ETag represents the final group-specific body, so fetch the
		// source manifest before applying local filtering and alias metadata.
		manifest, err := h.gatewayService.FetchCodexModelsManifest(attemptCtx, account, c.Query("client_version"), "")
		if err != nil {
			if _, observeErr := h.gatewayService.ObserveAPIKeyAttemptError(attemptCtx, account, cooldownAttempt, err, time.Now().UTC()); observeErr != nil {
				reqLog.Warn("openai.codex_models.api_key_cooldown_observe_failed", zap.Int64("account_id", account.ID), zap.Error(observeErr))
			}
			if c.Request.Context().Err() != nil {
				return
			}
			if service.IsRetryableCodexModelsManifestError(err) && switchCount < maxAccountSwitches {
				failedAccountIDs[account.ID] = struct{}{}
				switchCount++
				lastUpstreamErr = err
				continue
			}
			h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
			return
		}
		if observeErr := h.gatewayService.ObserveAPIKeyAttemptSuccess(attemptCtx, account, cooldownAttempt, time.Now().UTC()); observeErr != nil {
			reqLog.Warn("openai.codex_models.api_key_cooldown_success_failed", zap.Int64("account_id", account.ID), zap.Error(observeErr))
		}
		if err := h.gatewayService.CompleteAPIKeyCodexModelsManifestForClient(manifest, account); err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to complete Codex models manifest")
			return
		}
		if err := h.gatewayService.MergeGroupConfiguredCodexModels(c.Request.Context(), apiKey.Group, manifest, ifNoneMatch); err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to build Codex models manifest")
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}

		writeCodexModelsManifestResponse(c, manifest)
		return
	}
}

func writeCodexModelsManifestResponse(c *gin.Context, manifest *service.CodexModelsManifest) {
	if manifest.ETag != "" {
		c.Header("ETag", manifest.ETag)
	}
	if manifest.NotModified {
		c.Status(http.StatusNotModified)
		c.Writer.WriteHeaderNow()
		return
	}
	c.Data(http.StatusOK, "application/json", manifest.Body)
}
