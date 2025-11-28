package main

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()
var rdb *redis.Client

func init() {
	rdb = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
}

func main() {
	userID := "user:123"

	fmt.Println("Testing Fixed Window Counter...")
	demoFixedWindow(userID)
	time.Sleep(2 * time.Second)

	fmt.Println("\nTesting Sliding Window Log...")
	demoSlidingLog(userID)
	time.Sleep(2 * time.Second)

	fmt.Println("\nTesting Sliding Window Counter...")
	demoSlidingCounter(userID)
	time.Sleep(2 * time.Second)

	fmt.Println("\nTesting Token Bucket...")
	demoTokenBucket(userID)
}

func fixedWindowAllow(userID string, limit int64, window time.Duration) (bool, error) {
	key := fmt.Sprintf("fixed:%s", userID)

	count, err := rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}

	if count == 1 {
		rdb.Expire(ctx, key, window)
	}

	return count <= limit, nil
}

func demoFixedWindow(userID string) {
	limit := int64(5)

	for i := 1; i <= 7; i++ {
		allowed, _ := fixedWindowAllow(userID, limit, 10*time.Second)
		fmt.Printf("Request %d: %t\n", i, allowed)
	}

	rdb.Del(ctx, fmt.Sprintf("fixed:%s", userID))
}

func slidingLogAllow(userID string, limit int64, window time.Duration) (bool, error) {
	key := fmt.Sprintf("log:%s", userID)
	now := time.Now().UnixMilli()
	windowStart := now - window.Milliseconds()

	rdb.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart))

	count, err := rdb.ZCard(ctx, key).Result()
	if err != nil {
		return false, err
	}

	if count >= limit {
		return false, nil
	}

	rdb.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: now})
	rdb.Expire(ctx, key, window)

	return true, nil
}

func demoSlidingLog(userID string) {
	limit := int64(5)

	for i := 1; i <= 7; i++ {
		allowed, _ := slidingLogAllow(userID, limit, 2*time.Second)
		fmt.Printf("Request %d: %t\n", i, allowed)
		time.Sleep(300 * time.Millisecond)
	}

	rdb.Del(ctx, fmt.Sprintf("log:%s", userID))
}

func slidingCounterAllow(userID string, limit int64, window time.Duration) (bool, error) {
	now := time.Now()
	currentWindow := now.Truncate(window).Unix()
	previousWindow := now.Add(-window).Truncate(window).Unix()

	currentKey := fmt.Sprintf("counter:%s:%d", userID, currentWindow)
	previousKey := fmt.Sprintf("counter:%s:%d", userID, previousWindow)

	currentCount, _ := rdb.Get(ctx, currentKey).Int64()
	previousCount, _ := rdb.Get(ctx, previousKey).Int64()

	percentIntoWindow := float64(now.Unix()%int64(window.Seconds())) / float64(window.Seconds())
	estimatedCount := float64(previousCount)*(1-percentIntoWindow) + float64(currentCount)

	if estimatedCount >= float64(limit) {
		return false, nil
	}

	rdb.Incr(ctx, currentKey)
	rdb.Expire(ctx, currentKey, window*2)

	return true, nil
}

func demoSlidingCounter(userID string) {
	limit := int64(5)

	for i := 1; i <= 7; i++ {
		allowed, _ := slidingCounterAllow(userID, limit, 3*time.Second)
		fmt.Printf("Request %d: %t\n", i, allowed)
		time.Sleep(400 * time.Millisecond)
	}

	keys, _ := rdb.Keys(ctx, fmt.Sprintf("counter:%s:*", userID)).Result()
	if len(keys) > 0 {
		rdb.Del(ctx, keys...)
	}
}

var tokenBucketScript = redis.NewScript(`
	local key = KEYS[1]
	local capacity = tonumber(ARGV[1])
	local rate = tonumber(ARGV[2])
	local now = tonumber(ARGV[3])

	local tokens = tonumber(redis.call('HGET', key, 'tokens') or capacity)
	local last = tonumber(redis.call('HGET', key, 'last') or now)

	local elapsed = now - last
	tokens = math.min(capacity, tokens + elapsed * rate)

	if tokens < 1 then
		return 0
	end

	tokens = tokens - 1
	redis.call('HMSET', key, 'tokens', tokens, 'last', now)
	redis.call('EXPIRE', key, 3600)

	return 1
`)

func tokenBucketAllow(userID string, capacity float64, rate float64) (bool, error) {
	key := fmt.Sprintf("bucket:%s", userID)
	now := float64(time.Now().UnixNano()) / 1e9

	result, err := tokenBucketScript.Run(ctx, rdb, []string{key}, capacity, rate, now).Result()
	if err != nil {
		return false, err
	}

	return result.(int64) == 1, nil
}

func demoTokenBucket(userID string) {
	capacity := 5.0
	rate := 1.0

	for i := 1; i <= 7; i++ {
		allowed, _ := tokenBucketAllow(userID, capacity, rate)
		fmt.Printf("Request %d: %t\n", i, allowed)
		time.Sleep(400 * time.Millisecond)
	}

	rdb.Del(ctx, fmt.Sprintf("bucket:%s", userID))
}
