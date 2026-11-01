package api

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

const antiTruncationModelPrefix = "\u6297\u622a\u65ad-"

func (s *server) antiTruncationEnabled() bool {
	return s.config.AntiTruncation != nil && s.config.AntiTruncation()
}

func (s *server) antiTruncationModels(models []aistudio.Model) []aistudio.Model {
	enabled := s.antiTruncationEnabled()
	result := make([]aistudio.Model, 0, len(models)*2)
	for _, model := range models {
		id := strings.TrimPrefix(model.ID, "models/")
		// The prefix is reserved, including when a user mapping uses it.
		if strings.HasPrefix(id, antiTruncationModelPrefix) {
			continue
		}
		result = append(result, model)
		if !enabled {
			continue
		}
		alias := model
		alias.ID = antiTruncationModelPrefix + id
		if alias.Name == "" {
			alias.Name = id
		}
		alias.Name = antiTruncationModelPrefix + alias.Name
		alias.CapabilityOptions = maps.Clone(model.CapabilityOptions)
		if names, ok := model.CapabilityOptions["aliases"]; ok {
			aliases := make([]string, len(names))
			for index, name := range names {
				aliases[index] = antiTruncationModelPrefix + strings.TrimPrefix(name, "models/")
			}
			alias.CapabilityOptions["aliases"] = aliases
		}
		result = append(result, alias)
	}
	return result
}

func (s *server) resolveAntiTruncationModel(model string) (string, bool, error) {
	name := strings.TrimPrefix(strings.TrimSpace(model), "models/")
	base, prefixed := strings.CutPrefix(name, antiTruncationModelPrefix)
	if !prefixed {
		return model, false, nil
	}
	if !s.antiTruncationEnabled() {
		return "", false, fmt.Errorf("%w: anti-truncation models are disabled", aistudio.ErrModelNotFound)
	}
	if base == "" || strings.HasPrefix(base, antiTruncationModelPrefix) || strings.HasPrefix(base, "models/") {
		return "", false, fmt.Errorf("%w: invalid anti-truncation model %q", aistudio.ErrModelNotFound, model)
	}
	return base, true, nil
}

func (s *server) generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	model, enabled, err := s.resolveAntiTruncationModel(request.Model)
	if err != nil {
		return nil, err
	}
	request.Model, request.AntiTruncation = model, enabled
	return s.service.Generate(ctx, request)
}

func (s *server) countTokens(ctx context.Context, request aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	model, _, err := s.resolveAntiTruncationModel(request.Model)
	if err != nil {
		return aistudio.TokenCount{}, err
	}
	request.Model = model
	return s.service.CountTokens(ctx, request)
}
