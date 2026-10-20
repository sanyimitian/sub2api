package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// DownstreamAssessmentFilter uses an inclusive start and exclusive end, in the
// same user timezone as usage records. Days counts calendar days, including idle days.
type DownstreamAssessmentFilter struct {
	GroupID        int64 // Membership filter only; usage is summed across all groups.
	StartTime      time.Time
	EndTime        time.Time
	Days           int
	DailyThreshold int64
}

type DownstreamAssessmentUser struct {
	UserID        int64      `json:"user_id"`
	Email         string     `json:"email"`
	Username      string     `json:"username"`
	TotalRequests int64      `json:"total_requests"`
	TotalTokens   int64      `json:"total_tokens"`
	DailyAverage  float64    `json:"daily_average"`
	Qualified     bool       `json:"qualified"`
	LastUsedAt    *time.Time `json:"last_used_at"`
}

type DownstreamAssessmentResult struct {
	Items            []DownstreamAssessmentUser `json:"items"`
	Total            int64                      `json:"total"`
	QualifiedCount   int64                      `json:"qualified_count"`
	UnqualifiedCount int64                      `json:"unqualified_count"`
	TotalTokens      int64                      `json:"total_tokens"`
	Days             int                        `json:"days"`
	DailyThreshold   int64                      `json:"daily_threshold"`
	Page             int                        `json:"page"`
	PageSize         int                        `json:"page_size"`
	Pages            int                        `json:"pages"`
}

// DownstreamAssessmentRepository is an aggregate capability of the usage repository.
type DownstreamAssessmentRepository interface {
	GetDownstreamAssessment(context.Context, pagination.PaginationParams, DownstreamAssessmentFilter) (*DownstreamAssessmentResult, error)
}

func (s *UsageService) GetDownstreamAssessment(ctx context.Context, params pagination.PaginationParams, filter DownstreamAssessmentFilter) (*DownstreamAssessmentResult, error) {
	repo, ok := s.usageRepo.(DownstreamAssessmentRepository)
	if !ok {
		return nil, fmt.Errorf("downstream assessment repository is unavailable")
	}
	return repo.GetDownstreamAssessment(ctx, params, filter)
}
