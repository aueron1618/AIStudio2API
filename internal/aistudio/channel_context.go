package aistudio

import "context"

type requestChannelKey struct{}

// ContextWithChannel 固定公开请求的上游通道，不允许跨通道回退。
func ContextWithChannel(ctx context.Context, channel Channel) context.Context {
	return context.WithValue(ctx, requestChannelKey{}, channel)
}

// ChannelFromContext 返回 URL 指定的通道；空值保留内部调用的自动调度。
func ChannelFromContext(ctx context.Context) Channel {
	channel, _ := ctx.Value(requestChannelKey{}).(Channel)
	return channel
}

func selectionWithChannel(ctx context.Context, selection AccountSelection) AccountSelection {
	// 无模型的账户租约用于目录同步、文件和认证，不属于模型调用。
	if selection.ModelID != "" {
		if channel := ChannelFromContext(ctx); channel != "" {
			selection.Channel = channel
			selection.PreferredChannel = ""
		}
	}
	return selection
}

// FilterModelsByChannel 返回指定通道的目录。无通道标签的旧服务目录视为 Playground。
func FilterModelsByChannel(models []Model, channel Channel) []Model {
	if channel == "" {
		return models
	}
	filtered := make([]Model, 0, len(models))
	for _, model := range models {
		matches := len(model.Channels) == 0 && channel == ChannelPlayground
		for _, available := range model.Channels {
			matches = matches || available == string(channel)
		}
		if matches {
			model.Channels = []string{string(channel)}
			filtered = append(filtered, model)
		}
	}
	return filtered
}
