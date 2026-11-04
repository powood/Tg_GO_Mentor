package redis

import (
	"context"
	"fmt"
	"interview-bot/internal/config"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// NewClient создает и проверяет подключение к Redis
func NewClient(ctx context.Context, cfg config.RedisConfig) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     20,
		MinIdleConns: 4,
	})
	// Проверяем связь с Redis через Ping
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("не удалось подключиться к redis (ping): %w", err)
	}
	return client, nil
}
