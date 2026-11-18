package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const settingKeyModelPlazaCatalog = "model_plaza_synced_catalog"
const settingKeyAvailableChannelCatalog = "available_channels_synced_catalog"

type ModelPlazaSyncResult struct {
	SyncedAt   time.Time `json:"synced_at"`
	GroupCount int       `json:"group_count"`
	ModelCount int       `json:"model_count"`
}

type plazaSavedModel struct {
	Platform string `json:"platform"`
	Name     string `json:"name"`
}

type plazaSavedCatalog struct {
	ModelPlazaSyncResult
	Groups map[int64][]plazaSavedModel `json:"groups"`
}

// SyncModels is called only by the explicit admin action. Save the complete
// selection atomically after all reads succeed; a failed sync keeps the old list.
// Prices and access checks continue to use their current authoritative sources.
func (s *ModelPlazaService) SyncModels(ctx context.Context) (*ModelPlazaSyncResult, error) {
	return s.syncModels(ctx, settingKeyModelPlazaCatalog)
}

func (s *ModelPlazaService) SyncAvailableChannelModels(ctx context.Context) (*ModelPlazaSyncResult, error) {
	return s.syncModels(ctx, settingKeyAvailableChannelCatalog)
}

func (s *ModelPlazaService) syncModels(ctx context.Context, key string) (*ModelPlazaSyncResult, error) {
	if s.settingRepo == nil {
		return nil, fmt.Errorf("model plaza settings repository is unavailable")
	}
	groups, err := s.listGroups(ctx, true)
	if err != nil {
		return nil, err
	}
	catalog := plazaSavedCatalog{
		ModelPlazaSyncResult: ModelPlazaSyncResult{SyncedAt: time.Now().UTC(), GroupCount: len(groups)},
		Groups:               make(map[int64][]plazaSavedModel, len(groups)),
	}
	for _, group := range groups {
		for _, model := range group.Models {
			catalog.Groups[group.ID] = append(catalog.Groups[group.ID], plazaSavedModel{Platform: model.Platform, Name: model.Name})
			catalog.ModelCount++
		}
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		return nil, fmt.Errorf("encode model plaza catalog: %w", err)
	}
	if err := s.settingRepo.Set(ctx, key, string(raw)); err != nil {
		return nil, fmt.Errorf("save model plaza catalog: %w", err)
	}
	return &catalog.ModelPlazaSyncResult, nil
}

// Public reads never inspect accounts or update the saved selection. Before the
// first manual sync, keep the existing catalog for backwards compatibility.
func (s *ModelPlazaService) filterSavedGroupModels(ctx context.Context, byGroup map[int64]*PlazaGroup) error {
	catalog, err := s.loadSavedCatalog(ctx, settingKeyModelPlazaCatalog)
	if err != nil {
		return err
	}
	if catalog == nil {
		return nil
	}
	for id, group := range byGroup {
		allowed := make(map[plazaSavedModel]bool, len(catalog.Groups[id]))
		for _, model := range catalog.Groups[id] {
			allowed[model] = true
		}
		filtered := make([]PlazaModel, 0, len(group.Models))
		for _, model := range group.Models {
			if allowed[plazaSavedModel{Platform: model.Platform, Name: model.Name}] {
				filtered = append(filtered, model)
			}
		}
		group.Models = filtered
	}
	return nil
}

func (s *ModelPlazaService) loadSavedCatalog(ctx context.Context, key string) (*plazaSavedCatalog, error) {
	if s.settingRepo == nil {
		return nil, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, key)
	if errors.Is(err, ErrSettingNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read display model catalog: %w", err)
	}
	var catalog plazaSavedCatalog
	if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
		return nil, fmt.Errorf("decode display model catalog: %w", err)
	}
	if catalog.Groups == nil {
		return nil, fmt.Errorf("invalid display model catalog")
	}
	return &catalog, nil
}

// ApplyAvailableChannelCatalog keeps the saved model selection scoped to each
// group. The handler applies user visibility before rendering those group rows,
// so a model supported by a hidden group cannot leak into a visible group's row.
func (s *ModelPlazaService) ApplyAvailableChannelCatalog(ctx context.Context, channels []AvailableChannel) error {
	catalog, err := s.loadSavedCatalog(ctx, settingKeyAvailableChannelCatalog)
	if err != nil {
		return err
	}
	if catalog == nil {
		return nil
	}
	for i := range channels {
		ch := &channels[i]
		ch.GroupModels = make(map[int64][]SupportedModel, len(ch.Groups))
		for _, group := range ch.Groups {
			allowed := make(map[plazaSavedModel]bool, len(catalog.Groups[group.ID]))
			for _, model := range catalog.Groups[group.ID] {
				allowed[model] = true
			}
			for _, model := range ch.SupportedModels {
				if allowed[plazaSavedModel{Platform: model.Platform, Name: model.Name}] {
					ch.GroupModels[group.ID] = append(ch.GroupModels[group.ID], model)
				}
			}
		}
	}
	return nil
}
