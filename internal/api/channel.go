package api

import (
	"context"
	"net/http"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

// models 让所有协议的模型查询使用相同的通道目录。
func (s *server) models(ctx context.Context) ([]aistudio.Model, error) {
	models, err := s.service.Models(ctx)
	if err != nil {
		return nil, err
	}
	return aistudio.FilterModelsByChannel(models, aistudio.ChannelFromContext(ctx)), nil
}

func channelMiddleware(channel aistudio.Channel, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(aistudio.ContextWithChannel(r.Context(), channel)))
	})
}
