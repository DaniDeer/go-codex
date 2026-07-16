package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Commands is the narrow Redis command surface the adapters use. Constructors
// accept this interface — never a concrete client — so unit tests run against
// a hand-written fake and the adapter stays decoupled from the client library.
//
// [NewCommands] adapts a go-redis client. Any other implementation works as
// long as it honours the [ErrCacheMiss] contract on Get.
type Commands interface {
	// Get returns the value stored at key, or an error satisfying
	// errors.Is(err, ErrCacheMiss) when the key does not exist.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores value at key. ttl zero means no expiry.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Del removes the given keys. Deleting a missing key is not an error.
	Del(ctx context.Context, keys ...string) error
}

// NewCommands wraps a go-redis [goredis.UniversalClient] as [Commands].
// *redis.Client, *redis.ClusterClient, and sentinel-failover clients all
// satisfy UniversalClient — one shim covers every deployment shape.
// This is the only place the adapter touches go-redis.
func NewCommands(c goredis.UniversalClient) Commands {
	return goredisCommands{c: c}
}

type goredisCommands struct{ c goredis.UniversalClient }

func (g goredisCommands) Get(ctx context.Context, key string) ([]byte, error) {
	b, err := g.c.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return nil, ErrCacheMiss
		}
		return nil, err
	}
	return b, nil
}

func (g goredisCommands) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return g.c.Set(ctx, key, value, ttl).Err()
}

func (g goredisCommands) Del(ctx context.Context, keys ...string) error {
	return g.c.Del(ctx, keys...).Err()
}
