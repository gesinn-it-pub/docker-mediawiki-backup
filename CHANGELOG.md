# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions before 2.3.0 are documented in the GitHub releases only.

## [Unreleased]

### Added

- Backup service: failed filesystem and restic runs are retried (default: 3 additional attempts within 15 minutes). Configurable via the top-level `retry` option (`attempts`, `windowMinutes`); `attempts: 0` disables retries.

### Fixed

- Backup service: a backup directory that cannot be created no longer terminates the whole service. The error is logged and the run is handled like any other failed run (retried if retries are configured), so the other tiers and the restic target keep running.

### Changed

- Backup service: filesystem backups are written to `*.tar.part` and renamed only after the `create` script
  succeeded. A failed run no longer leaves a partial archive that counts as the latest backup or pushes out a good
  one during cleanup. `*.part` files are ignored when determining the last backup and when cleaning up, and stale
  ones are removed at startup.
- README: add an introduction describing what the image does and which backup targets it supports.

## [2.3.0] - 2026-10-25

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
