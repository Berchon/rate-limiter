# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `.dockerignore` to keep Docker build contexts minimal and free of local artifacts.
- `STORAGE_TYPE` configuration variable (`redis` | `memory`) in `.env.example`.
- `CHANGELOG.md` following Keep a Changelog and Semantic Versioning conventions.

### Changed
- Updated Go version in `go.mod` from `1.24.4` to `1.26.4`.
- Renamed `.env example` to `.env.example` with English documentation and defaults.
- Renamed `API/health.http` to `API/requests.http`, fixed JSON payload syntax, and replaced hardcoded token with a placeholder.
- Consolidated root shell scripts into executable English scripts under `scripts/` (`test_ip_sequential.sh`, `test_ip_round_robin.sh`, `test_mixed_rate_limit.sh`).
- Expanded `.gitignore` to exclude binaries, coverage reports, and IDE/OS files.

## [0.1.0] - 2025-10-21

### Added
- Initial HTTP rate limiter implementation supporting IP and `API_KEY` token rate limiting with Redis and in-memory storage backends.
