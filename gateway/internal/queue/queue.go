package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"code-review-agent/gateway/internal/jobs"
	"code-review-agent/gateway/internal/webhook"
)

const deliveryTTL = 72 * time.Hour

func RedisOpt(url string) (asynq.RedisConnOpt, *redis.Options, error) {
	asynqOpt, err := asynq.ParseRedisURI(url)
	if err != nil {
		return nil, nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	redisOpt, err := redis.ParseURL(url)
	if err != nil {
		return nil, nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	return asynqOpt, redisOpt, nil
}

type RedisDeduper struct {
	Client *redis.Client
}

func (d *RedisDeduper) Claim(ctx context.Context, deliveryID string) (bool, error) {
	return d.Client.SetNX(ctx, "ai-review:delivery:"+deliveryID, 1, deliveryTTL).Result()
}

func (d *RedisDeduper) Release(ctx context.Context, deliveryID string) error {
	return d.Client.Del(ctx, "ai-review:delivery:"+deliveryID).Err()
}

type AsynqEnqueuer struct {
	Client *asynq.Client
}

func (e *AsynqEnqueuer) Enqueue(ctx context.Context, job jobs.ReviewJob) error {
	task, err := jobs.NewReviewTask(job)
	if err != nil {
		return err
	}
	_, err = e.Client.EnqueueContext(ctx, task)
	if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
		return webhook.ErrDuplicateJob
	}
	return err
}
