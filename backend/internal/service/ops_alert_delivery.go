package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type OpsAlertDelivery struct {
	Channel       string    `json:"channel"`
	Recipient     string    `json:"recipient"`
	Status        string    `json:"status"`
	Attempts      int       `json:"attempts"`
	LastError     string    `json:"last_error,omitempty"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
}

func opsAlertDeliveries(event *OpsAlertEvent) map[string]*OpsAlertDelivery {
	deliveries := map[string]*OpsAlertDelivery{}
	if event.Dimensions != nil {
		if raw, err := json.Marshal(event.Dimensions["notifications"]); err == nil {
			_ = json.Unmarshal(raw, &deliveries)
		}
	}
	if deliveries == nil {
		deliveries = map[string]*OpsAlertDelivery{}
	}
	return deliveries
}

func (s *OpsAlertEvaluatorService) deferAlertEmail(ctx context.Context, event *OpsAlertEvent, recipient string) {
	deliveries := opsAlertDeliveries(event)
	key := "email:" + recipient
	delivery := deliveries[key]
	if delivery == nil {
		delivery = &OpsAlertDelivery{Channel: "email", Recipient: recipient}
		deliveries[key] = delivery
	}
	delivery.Status = "pending"
	delivery.NextAttemptAt = time.Now().UTC().Add(time.Minute)
	if s.opsRepo.UpdateAlertDeliveries(ctx, event.ID, deliveries) == nil {
		if event.Dimensions == nil {
			event.Dimensions = map[string]any{}
		}
		event.Dimensions["notifications"] = deliveries
	}
}

// Save before sending so a restart does not reset the retry budget. Successful
// recipients are independent: one unavailable channel cannot resend the others.
func (s *OpsAlertEvaluatorService) deliverAlert(ctx context.Context, event *OpsAlertEvent, channel, recipient string, send func() error) bool {
	deliveries := opsAlertDeliveries(event)
	key := channel + ":" + recipient
	delivery := deliveries[key]
	now := time.Now().UTC()
	if delivery == nil {
		delivery = &OpsAlertDelivery{Channel: channel, Recipient: recipient}
		deliveries[key] = delivery
	}
	if delivery.Status == "sent" || delivery.Attempts >= 3 || now.Before(delivery.NextAttemptAt) {
		return false
	}
	delivery.Attempts++
	delivery.Status = "pending"
	delivery.NextAttemptAt = now.Add(time.Duration(delivery.Attempts) * time.Minute)
	if err := s.opsRepo.UpdateAlertDeliveries(ctx, event.ID, deliveries); err != nil {
		return false
	}
	if event.Dimensions == nil {
		event.Dimensions = map[string]any{}
	}
	event.Dimensions["notifications"] = deliveries
	if err := send(); err != nil {
		delivery.Status = "failed"
		// SMTP errors may contain credentials or upstream response data.
		delivery.LastError = "Email delivery failed; check SMTP settings"
		if channel == "qq" {
			delivery.LastError = err.Error()
		}
	} else {
		delivery.Status = "sent"
		delivery.LastError = ""
	}
	// The network operation may have used up the evaluator's deadline.
	persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.opsRepo.UpdateAlertDeliveries(persistCtx, event.ID, deliveries); err != nil {
		logger.LegacyPrintf("service.ops_alert_evaluator", "[OpsAlertEvaluator] save delivery result failed (event=%d channel=%s)", event.ID, channel)
	}
	return delivery.Status == "sent"
}

func (s *OpsAlertEvaluatorService) maybeSendAlertQQ(ctx context.Context, runtimeCfg *OpsAlertRuntimeSettings, rule *OpsAlertRule, event *OpsAlertEvent) {
	if s.opsService == nil || !rule.NotifyQQ {
		return
	}
	if runtimeCfg != nil && runtimeCfg.Silencing.Enabled && isOpsAlertSilenced(time.Now().UTC(), rule, event, runtimeCfg.Silencing) {
		return
	}
	cfg, err := s.opsService.getQQNotificationConfig(ctx)
	if err != nil || !cfg.Enabled {
		return
	}
	s.deliverAlert(ctx, event, "qq", cfg.GroupID, func() error {
		message := fmt.Sprintf("【Sub2API 告警】%s\n级别：%s\n%s\n时间：%s", rule.Name, rule.Severity, event.Description, event.FiredAt.Format(time.RFC3339))
		return sendOpsQQMessage(ctx, cfg, message)
	})
}

func (s *OpsAlertEvaluatorService) retryAlertNotifications(ctx context.Context, runtimeCfg *OpsAlertRuntimeSettings, rules []*OpsAlertRule) {
	byID := map[int64]*OpsAlertRule{}
	for _, rule := range rules {
		if rule != nil && rule.Enabled {
			byID[rule.ID] = rule
		}
	}
	start := time.Now().UTC().Add(-24 * time.Hour)
	events, err := s.opsRepo.ListAlertEvents(ctx, &OpsAlertEventFilter{Limit: 100, StartTime: &start, PendingNotifications: true})
	if err != nil {
		return
	}
	for _, event := range events {
		if ctx.Err() != nil {
			return
		}
		rule := byID[event.RuleID]
		if rule == nil {
			continue
		}
		platform, groupID, region := parseOpsAlertRuleScope(rule.Filters)
		if platform != "" {
			silenced, err := s.opsRepo.IsAlertSilenced(ctx, rule.ID, platform, groupID, region, time.Now().UTC())
			if err != nil || silenced {
				continue
			}
		}
		s.maybeSendAlertEmail(ctx, runtimeCfg, rule, event)
		s.maybeSendAlertQQ(ctx, runtimeCfg, rule, event)
	}
}

func (s *OpsAlertEvaluatorService) sendAlertEmailTo(ctx context.Context, rule *OpsAlertRule, event *OpsAlertEvent, addr string) error {
	if s.emailService.notificationEmailService != nil {
		err := s.emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event: NotificationEmailEventOpsAlert, RecipientEmail: addr, RecipientName: emailRecipientName(addr),
			SourceType: "ops_alert", SourceID: fmt.Sprintf("%d", event.ID), Variables: opsAlertEmailVariables(rule, event),
		})
		if err == nil || !shouldFallbackNotificationEmail(err) {
			return err
		}
	}
	subject := fmt.Sprintf("[Ops Alert][%s] %s", strings.TrimSpace(rule.Severity), strings.TrimSpace(rule.Name))
	return s.emailService.SendEmail(ctx, addr, subject, buildOpsAlertEmailBody(rule, event))
}
