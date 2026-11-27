# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Standard rate-limit HTTP headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`) on protected responses and `Retry-After` on HTTP `429` responses in `RateLimitMiddleware`.
- Centralized Mockery v3 configuration (`.mockery.yaml` with `testify` template), `make mocks` target pinned to `v3.9.2`, and CI mock-drift check in `.github/workflows/ci.yml`.
- Atomic Redis rate-limiting Lua script embedded via `//go:embed` (`internal/infra/redis/scripts/add_request.lua`, executed via `EVALSHA` / `redis.NewScript`) with automatic orphan-key TTL healing, sub-second `PTTL` precision, and `miniredis` unit test suite (`>= 85%` package coverage) in `internal/infra/redis`.
- Structured configuration logging via `log/slog` (`LoadConfigWithLogger`), `STORAGE_TYPE` (`redis` | `memory`) option, HTTP server timeout settings, invariant validation (`Config.Validate`), and Redis credential masking (`MaskRedisURL`) in `internal/infra/config`.
- `RateLimitDecision` domain entity and unit tests in `internal/business/entity` (`>= 85%` package coverage).
- Typed store result structs (`AddRequestResult`, `BlockStatus`) and `context.Context` propagation in `service.LimiterStore` contract.

### Changed
- Refactored `RateLimitMiddleware` (`internal/infra/http/middleware`) to propagate `r.Context()`, inject `*slog.Logger` without logging raw `API_KEY` tokens, translate comments/tests to English, expand table-driven unit tests (`97.6%` package coverage), and remove `internal/infra/http/middleware/` from `.golangci.yml` temporary exclusions.
- Migrated `MockRateLimiterUseCase` generation from Mockery v2 `//go:generate` directives to Mockery v3 (`NewMockRateLimiterUseCase(t)` + `.EXPECT()`), and updated `internal/infra/http/middleware/ratelimit_test.go` accordingly.
- Refactored `RateLimiterUseCase` (`internal/business/usecase`) to propagate `context.Context`, inject `*slog.Logger` and `RateLimiterConfig` (`EnableTokenLimiting`, `DefaultTokenTTL`), return `*entity.RateLimitDecision`, update `MockRateLimiterUseCase`, add table-driven unit tests (`>= 85%` package coverage), and remove `internal/business/` from `.golangci.yml` temporary exclusions.
- Renamed `redis.RedisStore`/`redis.NewRedisStore` to idiomatic `redis.Store`/`redis.NewStore`, removed `internal/infra/redis/` from `.golangci.yml` temporary exclusions, and standardized key prefixes and error wrapping.
- Translated domain entities (`LimiterKey`, `TokenConfig`), DTOs (`CreateAPIKeyRequest`, `CreateAPIKeyResponse`), and `service.LimiterStore` comments to English.
- Minimally adapted `RedisStore`, `InMemoryStore`, and `RateLimiterUseCase` signatures to satisfy the updated `service.LimiterStore` contract.

### Removed
- Dead code: `RequestEvent` (`internal/business/entity/request_event.go`), `LimiterStoreConfig`, and `NewLimiterStore` (`internal/business/service/store.go`).

### Fixed
- Replaced sleeping background goroutines in `InMemoryStore.SaveTokenConfig` with lazy expiration, fixed canonical `token:<id>` key lookup and `ResetKey` mismatch, added `context.Context` cancellation checks and `RedisStore`-parity rate-limit calculations, and removed `internal/infra/storage/` from `.golangci.yml` temporary exclusions (`>= 85%` package coverage).

## [0.2.0] - 2026-11-25

### Added
- Automated Continuous Deployment workflow (`.github/workflows/deploy.yml`) that triggers a Render production deployment via Deploy Hook only after the `CI` workflow succeeds on `main`.
- Support for `REDIS_URL` connection strings (including TLS `rediss://` for managed Redis providers such as Upstash).
- GitHub Repository Rulesets (`protect-main` and `protect-dev`) enforcing Pull Requests, blocking direct pushes, force-pushes, and branch deletions, and requiring all 4 CI status checks (`Lint & Format`, `Unit Tests`, `Integration Tests`, `Docker Build`) to pass before merging.
- GitHub Actions CI workflow (`.github/workflows/ci.yml`) running parallel jobs for `gofmt` + `golangci-lint`, unit tests with `-race` and coverage summary, integration tests with `-race` and coverage summary, and multi-stage Docker build validation.
- Versioned Git hooks in `.githooks/` (`pre-commit` enforcing `gofmt`, `make lint`, and `make test-unit`; `commit-msg` enforcing Conventional Commits 1.0.0 in English).
- Standardized `Makefile` providing targets for build, run, test with race detection, configurable coverage gates (`test-unit`, `test-integration`), coverage reporting, linting, hooks setup, and Docker Compose orchestration.
- Multi-stage `Dockerfile` (`golang:1.26.4-alpine` builder and non-root `alpine:3.21` runtime with healthcheck).
- `.dockerignore` to keep Docker build contexts minimal and free of local artifacts.
- `STORAGE_TYPE` configuration variable (`redis` | `memory`) in `.env.example`.
- `CHANGELOG.md` following Keep a Changelog and Semantic Versioning conventions.

### Changed
- Standardized application port configuration from `APP_PORT` to `PORT` (default `8080`) and aligned default rate-limiter settings across `config.go`, `.env.example`, `Dockerfile`, and `docker-compose.yml` without logging sensitive credentials.
- Linters configuration in `.golangci.yml` enabling `errcheck`, `govet`, `staticcheck`, `ineffassign`, `unused`, `revive`, `gosec`, `misspell`, and `gofmt`.
- Updated `docker-compose.yml` to orchestrate both `redis` (with healthcheck) and the `app` service.
- Updated Go version in `go.mod` from `1.24.4` to `1.26.4`.
- Renamed `.env example` to `.env.example` with English documentation and defaults.
- Renamed `API/health.http` to `API/requests.http`, fixed JSON payload syntax, and replaced hardcoded token with a placeholder.
- Consolidated root shell scripts into executable English scripts under `scripts/` (`test_ip_sequential.sh`, `test_ip_round_robin.sh`, `test_mixed_rate_limit.sh`).
- Expanded `.gitignore` to exclude binaries, coverage reports, and IDE/OS files.

## [0.1.0] - 2025-10-21

### Added
- Initial HTTP rate limiter implementation supporting IP and `API_KEY` token rate limiting with Redis and in-memory storage backends.
