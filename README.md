# Introduction

Implement a simple rate limiter

# How to start

## 1. Start Redis

docker run -d -p 6379:6379 redis:8-alpine

## 2. Run the program

```sh
go mod tidy
go run main.go
```

# Rate Limiting Algorithms

## 1. Fixed Window Counter

### The Idea
Count requests in fixed time buckets. Reset when time's up.

```
Minute 1        Minute 2
[■■■■■]         [■■]
5 requests      2 requests
Reset at 60s →  Start fresh
```

### How It Works
1. Request comes in
2. Increment counter
3. If counter ≤ limit: ✅ Allow
4. If counter > limit: ❌ Block
5. After 60 seconds: counter resets to 0

### The Boundary Problem ⚠️

```
00:55 → Make 5 requests ✅ (allowed)
01:01 → Make 5 more ✅ (new window, allowed)

Problem: 10 requests in 6 seconds.
But limit is 5 per minute.
```

Users can game the system by timing requests at window boundaries.

### Pros & Cons

✅ **Pros:**
- Very simple
- Low memory (one counter per user)
- Fast

❌ **Cons:**
- Boundary problem (can get 2× limit)
- Sudden resets feel arbitrary

### When to Use
- Internal tools
- Quick prototypes
- Low-stakes applications


## 2. Sliding Window Log

### The Idea
Remember the timestamp of EVERY request. Count how many are in the last 60 seconds.

```
Timestamps: [10s, 15s, 20s, 45s, 70s]
Current time: 75s
Window: Last 60s = [15s to 75s]

Count:
10s ❌ Too old
15s ✅ In window
20s ✅ In window
45s ✅ In window
70s ✅ In window

Total: 4 requests in window
```

### How It Works
1. New request at time T
2. Delete timestamps older than (T - 60 seconds)
3. Count remaining timestamps
4. If count < limit: ✅ Allow and save timestamp
5. If count ≥ limit: ❌ Block

### Example
Limit: 5 per 60 seconds

```
10s → Save [10] → ✅
15s → Save [10, 15] → ✅
20s → Save [10, 15, 20] → ✅
25s → Save [10, 15, 20, 25] → ✅
30s → Save [10, 15, 20, 25, 30] → ✅
35s → Count = 5 → ❌ BLOCKED

70s → Remove <10s → [15, 20, 25, 30]
     → Count = 4 → ✅ Allow
```

### No Boundary Problem

```
55s → 5 requests [55, 55, 55, 55, 55]
61s → Check window [1s to 61s]
     → All 5 from 55s still there
     → Count = 5 ≥ 5
     → ❌ BLOCKED
```

### Pros & Cons

✅ **Pros:**
- 100% accurate
- No boundary problem
- Truly sliding window

❌ **Cons:**
- High memory (stores every request)
- Slower (more operations)
- Not scalable for millions of users

### When to Use
- Banking systems
- Payment processing
- When perfect accuracy is critical
- Low-medium traffic

## 3. Sliding Window Counter

### The Idea
Use TWO counters (previous + current window) and estimate the count.

```
Previous [0-60s]: 10 requests
Current [60-120s]: 4 requests
Now at 90s (halfway into current)

Math:
- We're 50% into current window
- From previous: 10 × 50% = 5
- From current: 4 × 100% = 4
- Estimate: 5 + 4 = 9 requests
```

### Visual

```
Previous    Current
[10 req]    [4 req]
  ├────┼────────┤
  0s   60s    120s
           ↑
         90s (we are here)

Looking back 60s = [30s to 90s]
- Last 30s of previous: ~5 requests
- First 30s of current: 4 requests
- Total estimate: 9 requests
```

### How It Works
1. Get previous window count
2. Get current window count
3. Calculate % into current window
4. Estimate = previous × (1-%) + current × 100%
5. If estimate < limit: ✅ Allow

### Accuracy

Not perfect, but very accurate

Why not 100%?
- Assumes even distribution
- Actual requests might cluster

But good enough for most cases

### Pros & Cons

✅ **Pros:**
- Good accuracy
- Low memory (only 2 counters)
- Fast
- Mostly solves boundary problem

❌ **Cons:**
- Slight estimation error
- More complex math

### When to Use
- **Production default choice**
- High traffic APIs
- Most modern systems
- When you need balance

## 4. Token Bucket

### The Idea
Bucket holds tokens. Each request costs 1 token. Tokens refill continuously.

```
Start: [🪙🪙🪙🪙🪙] 5 tokens

Request → -1 token
       [🪙🪙🪙🪙·]

Time passes → +tokens
       [🪙🪙🪙🪙🪙] Full!

5 quick requests:
       [·····] Empty!

Next request → ❌ No tokens!

Wait... → refill
       [🪙🪙···]

Request → ✅ Allowed
       [🪙····]
```

### How It Works

**State:**
- tokens: current token count
- last_refill: when we last updated

**On each request:**
1. Calculate time passed
2. Add tokens: time × refill_rate
3. Cap at capacity
4. If tokens ≥ 1: ✅ Allow, subtract 1
5. If tokens < 1: ❌ Block

### Burst Handling

Scenario: User idle for 10 seconds

Bucket fills up: [🪙🪙🪙🪙🪙]

User makes 5 requests instantly:
All allowed

This is a "burst", bucket was full from waiting.

### Why It's Popular

Allows bursts but controls average rate

Average rate: 60 tokens/minute
But can burst up to capacity instantly

Good for:
- APIs (user saves up tokens)
- File uploads (one big file = many tokens)
- Bursty traffic patterns

### Pros & Cons

✅ **Pros:**
- Allows bursts (good UX)
- Smooth refilling
- Industry standard
- Flexible (different costs per request)

❌ **Cons:**
- Needs atomic operations (Lua script)
- Slightly complex
- Float math

### When to Use
- **Public APIs** (most popular choice)
- When bursts are OK
- AWS, GitHub, Stripe all use this
- Modern applications

## Summary Table

| Algorithm | Complexity | Memory | Accuracy | Bursts | Production Ready |
|-----------|-----------|--------|----------|--------|------------------|
| Fixed Window | ⭐ Easy | Low | 50% | No | ❌ |
| Sliding Log | ⭐⭐⭐ Hard | High | 100% | No | ✅ Critical only |
| Sliding Counter | ⭐⭐ Medium | Low | 95% | No | ✅ Yes |
| Token Bucket | ⭐⭐ Medium | Low | 95% | Yes | ✅ Best default |

## Key Takeaways

1. **Fixed Window** = Simple but has boundary problem
2. **Sliding Log** = Perfect accuracy but expensive
3. **Sliding Counter** = Good balance for high traffic
4. **Token Bucket** = Most popular, allows bursts
