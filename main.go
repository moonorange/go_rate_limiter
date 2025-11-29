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

// Fixed Window algorithm
// Restrict a number of requests from a clint to a fixed number within the time window.
// It is simple and easy to implement
// but it might lead more traffic than expected
// if spikes happen during the border of the time window.
func fixedWindowAllow(userID string, limit int64, window time.Duration) (bool, error) {
	key := fmt.Sprintf("fixed:%s", userID)

	count, err := rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}

	// For the first request within the time window, set the expiration
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

// Sliding Window Log algorithm
// Stores timestamp of each request in a sorted set.
// Provides accurate rate limiting but uses more memory (one entry per request).
func slidingLogAllow(userID string, limit int64, window time.Duration) (bool, error) {
	key := fmt.Sprintf("log:%s", userID)
	now := time.Now().UnixMilli()
	windowStart := now - window.Milliseconds()

	// Remove timestamps older than the sliding window
	rdb.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart))

	// Count requests within the current window
	count, err := rdb.ZCard(ctx, key).Result()
	if err != nil {
		return false, err
	}

	if count >= limit {
		return false, nil
	}

	// Log this request timestamp
	rdb.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: now})
	// Reset TTL for cleanup of inactive users
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

// Sliding Window Counter algorithm
// Hybrid approach that approximates a sliding window using fixed window counters.
// More accurate than Fixed Window, more memory efficient than Sliding Log.
func slidingCounterAllow(userID string, limit int64, window time.Duration) (bool, error) {
	now := time.Now()
	// Calculate start timestamps for current and previous fixed windows
	// Truncate the current time to the start of the current window
	// e.g. 1705329824 with 10s window -> 1705329820
	currentWindow := now.Truncate(window).Unix()
	// Truncate the time of the previous window
	// e.g. 1705329824-10 with 10s window -> 1705329810
	previousWindow := now.Add(-window).Truncate(window).Unix()

	currentKey := fmt.Sprintf("counter:%s:%d", userID, currentWindow)
	previousKey := fmt.Sprintf("counter:%s:%d", userID, previousWindow)

	// Get counts from both windows
	currentCount, _ := rdb.Get(ctx, currentKey).Int64()
	previousCount, _ := rdb.Get(ctx, previousKey).Int64()

	// Calculate how far into the current window we are (0.0 to 1.0)
	// Example: timestamp 1705329824 with 10s window
	// 1705329824 % 10 = 4 seconds into window
	// 4 / 10 = 0.4 (40% through the window)
	percentIntoWindow := float64(now.Unix()%int64(window.Seconds())) / float64(window.Seconds())

	// Estimate total requests using weighted average
	// Example: previousCount=4, currentCount=2, percentIntoWindow=0.4
	// 4 * (1-0.4) + 2 = 4 * 0.6 + 2 = 2.4 + 2 = 4.4 requests
	estimatedCount := float64(previousCount)*(1-percentIntoWindow) + float64(currentCount)

	if estimatedCount >= float64(limit) {
		return false, nil
	}

	rdb.Incr(ctx, currentKey)
	// Keep data for 2x window to ensure previous window data is available
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

// Using Lua script to ensure race conditions don't occur
// when multiple clients try to access the same resource at the same time.
// The script is executed atomically, so only one client can execute it at a time.
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

// Token Bucket algorithm
// Implements a token bucket with a fixed capacity and a refill rate.
// Allows bursts of traffic up to capacity but refills over time.
// More accurate than Fixed Window and Sliding Window Counter.
func tokenBucketAllow(userID string, capacity float64, rate float64) (bool, error) {
	key := fmt.Sprintf("bucket:%s", userID)
	// Convert the current time to a float64 in seconds
	now := float64(time.Now().UnixNano()) / 1e9

	result, err := tokenBucketScript.Run(ctx, rdb, []string{key}, capacity, rate, now).Result()
	if err != nil {
		return false, err
	}
	// Redis Lua returns int64
	// Type-assert it to compare with 1
	if val, ok := result.(int64); ok {
		return val == 1, nil
	} else {
		return false, fmt.Errorf("unexpected type from Redis")
	}
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
