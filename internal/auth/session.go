package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	SessionLifetime  = 12 * time.Hour
	RememberLifetime = 7 * 24 * time.Hour
)

var ErrSession = errors.New("invalid session")

// Browser sessions store only a verifier hash in Redis; the raw credential
// stays in an HttpOnly cookie. Redis TTL is the authoritative expiry.
type Sessions struct {
	Redis  *redis.Client
	Prefix string
}

func (s Sessions) key(value string) (string, error) {
	if len(value) != 64 {
		return "", ErrSession
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", ErrSession
	}
	hash := sha256.Sum256([]byte(value))
	return s.Prefix + "auth:session:" + hex.EncodeToString(hash[:]), nil
}

func (s Sessions) Create(ctx context.Context, user string, remember bool) (string, time.Time, error) {
	if s.Redis == nil || user == "" {
		return "", time.Time{}, ErrSession
	}
	lifetime := SessionLifetime
	if remember {
		lifetime = RememberLifetime
	}
	for attempt := 0; attempt < 3; attempt++ {
		var bytes [32]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return "", time.Time{}, err
		}
		value := hex.EncodeToString(bytes[:])
		key, _ := s.key(value)
		created, err := s.Redis.SetNX(ctx, key, user, lifetime).Result()
		if err != nil {
			return "", time.Time{}, err
		}
		if created {
			return value, time.Now().Add(lifetime), nil
		}
	}
	return "", time.Time{}, ErrSession
}

func (s Sessions) Verify(ctx context.Context, value string) (string, error) {
	key, err := s.key(value)
	if err != nil || s.Redis == nil {
		return "", ErrSession
	}
	user, err := s.Redis.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) || err == nil && user == "" {
		return "", ErrSession
	}
	return user, err
}

func (s Sessions) Revoke(ctx context.Context, value string) error {
	key, err := s.key(value)
	if err != nil {
		return nil
	}
	if s.Redis == nil {
		return ErrSession
	}
	return s.Redis.Del(ctx, key).Err()
}
