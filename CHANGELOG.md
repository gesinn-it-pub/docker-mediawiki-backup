# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions before 2.3.0 are documented in the GitHub releases only.

## [Unreleased]

### Added

- Backup service: `daysOfWeek` (daily only) restricts the daily backup to the listed weekdays.
- Backup service: top-level `exclusive` option; if several tiers are due on the same day, only the highest runs
  (monthly > weekly > daily). Skipped runs are logged.
- Optional restic backup target alongside filesystem backups.
- Unit tests for the backup service, `make ci` / `make ci-unit` targets and a Go version matrix in CI.

### Changed

- Backup service: unknown weekday names in `daysOfWeek`, and `daysOfWeek` on weekly or monthly, now fail when the
  configuration is loaded.
- GitHub token for `make release` is resolved from an environment variable derived from the git identity
  (falls back to `GH_API_TOKEN`).
- Scripts reject unknown command line arguments.
- Go is pinned to a maintained release.
