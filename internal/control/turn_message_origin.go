package control

import (
	"context"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

func withTurnInputOrigin(ctx context.Context, synthetic bool) context.Context {
	origin := provider.MessageOriginUser
	if synthetic {
		origin = provider.MessageOriginHost
	}
	return agent.WithInputMessageOrigin(ctx, origin)
}

func persistedUserTurn(content, raw string, images, audio []string, createdAt int64) provider.Message {
	return provider.Message{
		Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content: content, RawContent: raw,
		Images: append([]string(nil), images...), Audio: append([]string(nil), audio...), CreatedAt: createdAt,
	}
}
