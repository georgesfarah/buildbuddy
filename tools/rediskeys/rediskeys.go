// Tool to print out some random keys from Redis.
//
// First, port-forward to the redis instance you want to analyze:
// $ kubectl port-forward -n redis-prod redis-sharded-3 6380:6379
//
// Then run the tool:
// $ bazel run tools/rediskeys
package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/buildbuddy-io/buildbuddy/server/util/log"
	"github.com/go-redis/redis/v8"
)

var (
	target          = flag.String("target", "localhost:6380", "Target of the Redis instance")
	count           = flag.Int("count", 10, "Number of keys to print")
	maxRemainingTTL = flag.Duration("max_ttl", 0*time.Second, "Only includes keys if the remaining TTL is less than this value, for measuring data expiring soon")
)

func main() {
	flag.Parse()

	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: *target})

	found := 0
	for found < *count {
		key, err := client.RandomKey(ctx).Result()
		if err != nil {
			log.Warningf("randomkey error: %v", err)
		}

		if *maxRemainingTTL > 0 {
			ttl, err := client.TTL(ctx, key).Result()
			if err != nil {
				log.Warningf("ttl error: %v", err)
			}
			if ttl > *maxRemainingTTL {
				continue
			}
		}

		fmt.Println(key)
		found++
	}
}
