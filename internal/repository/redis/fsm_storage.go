package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"interview-bot/internal/domain"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

var releaseLockScript = goredis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

type FSMStorage struct {
	client *goredis.Client
	ttl    time.Duration
}

func NewFSMStorage(client *goredis.Client, ttl time.Duration) *FSMStorage {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &FSMStorage{
		client: client,
		ttl:    ttl,
	}

}

func (s *FSMStorage) key(userID int64) string {
	return fmt.Sprintf("fsm:user:%d", userID)
}
func (s *FSMStorage) lockKey(userID int64) string {
	return fmt.Sprintf("lock:user:%d", userID)
}

func (s *FSMStorage) SetState(ctx context.Context, userID int64, state domain.UserState) error {
	state.UpdatedAt = time.Now()
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("ошибка сериализации user_state: %w", err)
	}
	err = s.client.Set(ctx, s.key(userID), data, s.ttl).Err()
	if err != nil {
		return fmt.Errorf("ошибка сохранения state в redis: %w", err)
	}
	return nil
}

func (s *FSMStorage) GetState(ctx context.Context, userID int64) (*domain.UserState, bool, error) {
	data, err := s.client.GetEx(ctx, s.key(userID), s.ttl).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("ошибка чтения state из redis: %w", err)
	}
	var state domain.UserState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, false, fmt.Errorf("ошибка десериализации user_state: %w", err)
	}
	return &state, true, nil
}
func (s *FSMStorage) ResetState(ctx context.Context, userID int64) error {
	err := s.client.Del(ctx, s.key(userID)).Err()
	if err != nil {
		return fmt.Errorf("ошибка сброса state в redis: %w", err)
	}
	return nil
}
func (s *FSMStorage) AcquireLock(ctx context.Context, userID int64, ttl time.Duration) (string, bool, error) {
	if ttl <= 0 {
		ttl = 75 * time.Second
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", false, fmt.Errorf("ошибка генерации токена лока: %w", err)
	}
	token := hex.EncodeToString(b[:])
	ok, err := s.client.SetNX(ctx, s.lockKey(userID), token, ttl).Result()
	if err != nil {
		return "", false, fmt.Errorf("ошибка захвата лока в redis: %w", err)
	}
	if !ok {
		return "", false, nil
	}
	return token, true, nil
}

const (
	maxEvalsPerMinute      = 5
	maxEvalsPerHour        = 90
	spamBanThresholdPerMin = 15
)

var rateLimitScript = goredis.NewScript(`
if redis.call("EXISTS", KEYS[3]) == 1 then
	return 0
end
local m = redis.call("INCR", KEYS[1])
if m == 1 or redis.call("TTL", KEYS[1]) == -1 then
	redis.call("EXPIRE", KEYS[1], 60)
end
if m > tonumber(ARGV[3]) then
	redis.call("SET", KEYS[3], "1", "EX", 600)
	return 0
end
if m > tonumber(ARGV[1]) then
	return 0
end
local h = redis.call("INCR", KEYS[2])
if h == 1 or redis.call("TTL", KEYS[2]) == -1 then
	redis.call("EXPIRE", KEYS[2], 3600)
end
if h > tonumber(ARGV[2]) then
	return 0
end
return 1
`)

// ReleaseLock атомарно снимает лок только если токен совпадает с нашим
func (s *FSMStorage) ReleaseLock(ctx context.Context, userID int64, token string) error {
	if token == "" {
		return nil
	}
	err := releaseLockScript.Run(ctx, s.client, []string{s.lockKey(userID)}, token).Err()
	if err != nil && !errors.Is(err, goredis.Nil) {
		return fmt.Errorf("ошибка освобождения лока в redis: %w", err)
	}
	return nil
}
func (s *FSMStorage) AllowEval(ctx context.Context, userID int64) (bool, error) {
	keys := []string{
		fmt.Sprintf("rl:min:%d", userID),
		fmt.Sprintf("rl:hour:%d", userID),
		fmt.Sprintf("rl:ban:%d", userID),
	}
	res, err := rateLimitScript.Run(
		ctx,
		s.client,
		keys,
		maxEvalsPerMinute,
		maxEvalsPerHour,
		spamBanThresholdPerMin,
	).Int()
	if err != nil {
		return false, fmt.Errorf("ошибка проверки rate-limit в redis: %w", err)
	}
	return res == 1, nil
}
func (s *FSMStorage) selectedTopicsKey(userID int64) string {
	return fmt.Sprintf("topics:selected:%d", userID)
}

// ToggleSelectedTopic переключает состояние чекбокса темы в Redis
func (s *FSMStorage) ToggleSelectedTopic(ctx context.Context, userID int64, topicID string) (bool, error) {
	key := s.selectedTopicsKey(userID)
	isMember, err := s.client.SIsMember(ctx, key, topicID).Result()
	if err != nil {
		return false, fmt.Errorf("ошибка проверки выбранной темы в redis: %w", err)
	}
	if isMember {
		if err := s.client.SRem(ctx, key, topicID).Err(); err != nil {
			return false, fmt.Errorf("ошибка удаления темы из выбранных: %w", err)
		}
		return false, nil
	}
	pipe := s.client.Pipeline()
	pipe.SAdd(ctx, key, topicID)
	pipe.Expire(ctx, key, 2*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("ошибка добавления темы в выбранные: %w", err)
	}
	return true, nil
}

// GetSelectedTopics возвращает список отмеченных чекбоксами тем
func (s *FSMStorage) GetSelectedTopics(ctx context.Context, userID int64) ([]string, error) {
	key := s.selectedTopicsKey(userID)
	members, err := s.client.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("ошибка получения выбранных тем: %w", err)
	}
	return members, nil
}

// ClearSelectedTopics очищает выбор чекбоксов
func (s *FSMStorage) ClearSelectedTopics(ctx context.Context, userID int64) error {
	key := s.selectedTopicsKey(userID)
	return s.client.Del(ctx, key).Err()
}

// SelectAllTopics отмечает сразу пачку тем
func (s *FSMStorage) SelectAllTopics(ctx context.Context, userID int64, topicIDs []string) error {
	if len(topicIDs) == 0 {
		return nil
	}
	key := s.selectedTopicsKey(userID)
	pipe := s.client.Pipeline()
	pipe.Del(ctx, key)
	args := make([]interface{}, len(topicIDs))
	for i, id := range topicIDs {
		args[i] = id
	}
	pipe.SAdd(ctx, key, args...)
	pipe.Expire(ctx, key, 2*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *FSMStorage) qMsgKey(userID int64) string {
	return fmt.Sprintf("qmsg:user:%d", userID)
}

// SaveQuestionMsgID сохраняет ID сообщения с вопросом в отдельном ключе Redis (атомарно, без гонок с FSM)
func (s *FSMStorage) SaveQuestionMsgID(ctx context.Context, userID int64, msgID int) error {
	return s.client.Set(ctx, s.qMsgKey(userID), msgID, s.ttl).Err()
}

// GetQuestionMsgID возвращает ID сообщения с вопросом из отдельного ключа Redis
func (s *FSMStorage) GetQuestionMsgID(ctx context.Context, userID int64) (int, error) {
	val, err := s.client.Get(ctx, s.qMsgKey(userID)).Int()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return 0, nil
		}
		return 0, err
	}
	return val, nil
}
