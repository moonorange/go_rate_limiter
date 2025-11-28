# Introduction

Implement a simple rate limiter

## How to start

# 1. Start Redis
docker run -d -p 6379:6379 redis:7-alpine

# 2. Put both files in a directory and run
go mod tidy
go run main.go
