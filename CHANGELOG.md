# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions before 2.3.0 are documented in the GitHub releases only.

## [Unreleased]

### Changed

- restic is now taken from the official `restic/restic` image (0.19.1, kept current by Dependabot) instead of the
  distribution package (0.12.1). Only restic 0.14 and newer can compress repositories, which the uncompressed dump
  relies on, and 0.17 and newer report a missing repository with exit code 10. Repositories created by restic 0.14 or
  newer (format v2) cannot be read by older versions, so a later image downgrade below 0.14 is not possible for them.
  Existing format v1 repositories keep working.
- Backup service: the uncompressed restic dump uses the standard `INSERT` format again instead of one `INSERT` per
  row (`--skip-extended-insert`, introduced in 2.5.0). Measurements showed that it only saves about 11 % of the
  snapshot growth compared to extended inserts, but doubles the dump size and makes the restore several times
  slower. Snapshots made by 2.5.0 stay restorable. The first run after the update is larger than usual, because it
  hardly deduplicates against the 2.5.0 snapshots.

## [2.5.0] - 2026-11-01

### Added

- `restore restic [<snapshot>|latest] [--tag <tier>]` restores the database and the images from a restic repository.
  It uses the newest complete run (or the run of the given db snapshot) so that both belong together.

### Changed

- Backup service: the restic target stores the database dump uncompressed as `wiki.sql` (`--skip-extended-insert
  --order-by-primary`) instead of `wiki.sql.bz2`, so that restic can deduplicate it. The first run after the update
  is therefore large. Snapshots of the old format stay in the repository; they are not restorable with
  `restore restic` and have to be removed with `restic forget`.
- Backup service: restic snapshots are tagged `images`/`db`, `run-<scheduled time>` and `version-<image version>`.
  A dump snapshot is tagged `complete` only after `mysqldump` succeeded; a failed dump is discarded.
- Integration test: runs against MediaWiki 1.39 and 1.43 with `mysql:8` instead of MediaWiki 1.35 with `mysql:5`;
  CI covers both releases (`make test MW_VERSION=1.39`).

## [2.4.0] - 2026-11-01

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
