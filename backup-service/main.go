package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Config struct to parse backup.json
type Config struct {
	Backups struct {
		Daily   BackupSchedule `json:"daily"`
		Weekly  BackupSchedule `json:"weekly"`
		Monthly BackupSchedule `json:"monthly"`
	} `json:"backups"`
	BackupDirectory      string  `json:"backupDirectory"`
	MinStoragePercentage float64 `json:"minStoragePercentage"`
	// Exclusive: if several tiers are due on the same day, only the highest one
	// (monthly > weekly > daily) runs.
	Exclusive bool `json:"exclusive,omitempty"`
	// Retry controls how failed filesystem/restic runs are repeated. Unset: defaults apply.
	Retry *RetryConfig `json:"retry,omitempty"`
}

// RetryConfig configures retries of a failed backup run.
type RetryConfig struct {
	// Attempts is the number of additional attempts after the first failure; 0 disables retries.
	Attempts int `json:"attempts"`
	// WindowMinutes is the time after the scheduled run within which all retries must have run.
	WindowMinutes int `json:"windowMinutes,omitempty"`
}

const (
	defaultRetryAttempts      = 3
	defaultRetryWindowMinutes = 15
)

type StorageTarget struct {
	RetainCount int `json:"retainCount"`
}

type BackupSchedule struct {
	// Legacy: retainCount directly on the schedule (filesystem-only, backward compatible)
	RetainCount int    `json:"retainCount"`
	Time        string `json:"time"`
	DayOfWeek   string `json:"dayOfWeek,omitempty"`
	// DaysOfWeek restricts the daily tier to these weekdays (English names); empty means every day.
	DaysOfWeek []string       `json:"daysOfWeek,omitempty"`
	DayOfMonth int            `json:"dayOfMonth,omitempty"`
	Filesystem *StorageTarget `json:"filesystem,omitempty"`
	Restic     *StorageTarget `json:"restic,omitempty"`
}

// filesystemRetainCount returns the effective retain count for filesystem backups.
// Prefers the explicit "filesystem" key; falls back to legacy "retainCount" for backward compatibility.
func (s BackupSchedule) filesystemRetainCount() (int, bool) {
	if s.Filesystem != nil {
		return s.Filesystem.RetainCount, true
	}
	if s.RetainCount > 0 {
		return s.RetainCount, true
	}
	return 0, false
}

var logFile *os.File

// Seams for tests.
var (
	execCommand  = exec.Command
	createScript = "/usr/local/bin/create"
)

func main() {
	// Load configuration
	config, err := loadConfig("/backup.json")
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	applyDefaults(config)

	// Initialize log file
	logFilePath := filepath.Join(config.BackupDirectory, "docker-mediawiki-backup.log")
	logFile, err = os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Error opening log file: %v", err)
	}
	defer logFile.Close()

	// Set log output to log file

	multiWriter := io.MultiWriter(logFile, os.Stdout)
	log.SetOutput(multiWriter)

	log.Println("Backup service started")
	removeStalePartFiles(config)

	// Run as a service
	for {
		now := time.Now()
		runDueBackups(config, now)
		runPendingRetries(config, now)
		time.Sleep(1 * time.Minute)
	}
}

func applyDefaults(config *Config) {
	if config.BackupDirectory == "" {
		log.Println("BackupDirectory is undefined, defaulting to /backup")
		config.BackupDirectory = "/backup"
	}
	if config.MinStoragePercentage == 0.0 {
		log.Println("MinStoragePercentage is undefined, defaulting to 20%")
		config.MinStoragePercentage = 20.0
	}
	if config.Retry == nil {
		config.Retry = &RetryConfig{Attempts: defaultRetryAttempts}
	}
	if config.Retry.WindowMinutes == 0 {
		config.Retry.WindowMinutes = defaultRetryWindowMinutes
	}
}

// backupTypes lists the tiers in ascending rank.
var backupTypes = []string{"daily", "weekly", "monthly"}

func runDueBackups(config *Config, now time.Time) {
	for _, backupType := range backupTypes {
		handleBackup(config, backupType, now)
	}
}

func loadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	if err := validateConfig(&config); err != nil {
		return nil, err
	}
	return &config, nil
}

func validateConfig(config *Config) error {
	for _, backupType := range backupTypes {
		schedule := getSchedule(config, backupType)
		if len(schedule.DaysOfWeek) > 0 && backupType != "daily" {
			return fmt.Errorf("backups.%s: daysOfWeek is only supported for daily (use dayOfWeek for weekly)", backupType)
		}
		for _, name := range schedule.DaysOfWeek {
			if !isWeekdayName(name) {
				return fmt.Errorf("backups.%s.daysOfWeek: unknown weekday %q (expected English names like \"Monday\")", backupType, name)
			}
		}
	}
	if r := config.Retry; r != nil && (r.Attempts < 0 || r.WindowMinutes < 0) {
		return fmt.Errorf("retry: attempts and windowMinutes must not be negative")
	}
	return nil
}

func isWeekdayName(name string) bool {
	for d := time.Sunday; d <= time.Saturday; d++ {
		if d.String() == name {
			return true
		}
	}
	return false
}

func handleBackup(config *Config, backupType string, now time.Time) {
	schedule := getSchedule(config, backupType)

	run, reason := decideBackup(config, backupType, now)
	if !run {
		if reason != "" {
			log.Printf("%s skipped: %s", backupType, reason)
		}
		return
	}

	if _, hasFilesystem := schedule.filesystemRetainCount(); hasFilesystem {
		runTarget(config, retryJob{backupType: backupType, target: targetFilesystem, scheduled: now}, now)
	}
	if schedule.Restic != nil {
		runTarget(config, retryJob{backupType: backupType, target: targetRestic, scheduled: now}, now)
	}
}

const (
	targetFilesystem = "filesystem"
	targetRestic     = "restic"
)

// retryJob is a failed backup target waiting for its next attempt.
type retryJob struct {
	backupType string
	target     string
	scheduled  time.Time // the scheduled run; determines the backup file name
	attempt    int       // retries already started
	next       time.Time // earliest time of the next attempt
	deadline   time.Time // no attempt after this
}

// pendingRetries is only touched from the main loop. Retries are queued instead of
// slept on, so other tiers due in the meantime still run on time.
var pendingRetries []retryJob

// runTarget makes one attempt for job.target. On failure it queues the next retry
// (if configured) or reports that retries are exhausted.
func runTarget(config *Config, job retryJob, now time.Time) {
	var err error
	switch job.target {
	case targetFilesystem:
		err = runFilesystemBackup(config, job)
	case targetRestic:
		restic := getSchedule(config, job.backupType).Restic
		if err = runResticBackup(job.backupType, restic.RetainCount, job.scheduled); err == nil {
			log.Printf("%s restic backup completed", job.backupType)
		}
	}
	if err == nil {
		if job.attempt > 0 {
			log.Printf("%s %s backup succeeded on retry %d", job.backupType, job.target, job.attempt)
		}
		return
	}
	log.Printf("Error running %s %s backup (attempt %d): %v", job.backupType, job.target, job.attempt+1, err)

	attempts, window := 0, time.Duration(0)
	if r := config.Retry; r != nil && r.Attempts > 0 {
		attempts = r.Attempts
		minutes := r.WindowMinutes
		if minutes == 0 {
			minutes = defaultRetryWindowMinutes
		}
		window = time.Duration(minutes) * time.Minute
	}
	if job.attempt >= attempts {
		if attempts > 0 {
			log.Printf("%s %s backup failed finally after %d attempts", job.backupType, job.target, job.attempt+1)
		}
		return
	}
	if job.attempt == 0 {
		job.deadline = job.scheduled.Add(window)
	}
	// Spread the retries evenly over the window; the last one is due at the deadline.
	job.attempt++
	job.next = job.scheduled.Add(window * time.Duration(job.attempt) / time.Duration(attempts))
	log.Printf("%s %s backup: retry %d/%d scheduled for %s",
		job.backupType, job.target, job.attempt, attempts, job.next.Format("15:04:05"))
	pendingRetries = append(pendingRetries, job)
}

// runPendingRetries runs the retries that are due at now and drops those whose window has passed.
func runPendingRetries(config *Config, now time.Time) {
	queue := pendingRetries
	pendingRetries = nil
	for _, job := range queue {
		switch {
		case now.After(job.deadline):
			log.Printf("%s %s backup failed finally: retry window exceeded", job.backupType, job.target)
		case now.Before(job.next):
			pendingRetries = append(pendingRetries, job)
		default:
			log.Printf("%s %s backup: starting retry %d", job.backupType, job.target, job.attempt)
			runTarget(config, job, now)
		}
	}
}

// runFilesystemBackup performs one filesystem attempt. Skips (existing backup, low
// storage) are not failures and are not retried.
func runFilesystemBackup(config *Config, job retryJob) error {
	backupType := job.backupType
	retainCount, _ := getSchedule(config, backupType).filesystemRetainCount()
	backupPath, err := ensureBackupPath(filepath.Join(config.BackupDirectory, backupType))
	if err != nil {
		return fmt.Errorf("creating backup directory %s: %w", backupPath, err)
	}

	if job.attempt == 0 {
		lastBackup, err := getLastBackupTime(backupPath)
		if err == nil && job.scheduled.Sub(lastBackup) < time.Minute {
			log.Printf("%s filesystem backup skipped: backup with timestamp %s already exists", backupType, lastBackup)
			return nil
		}
	}
	if !CheckStorage(config.BackupDirectory, config.MinStoragePercentage) {
		log.Printf("%s filesystem backup skipped: not enough free storage", backupType)
		return nil
	}
	backupFile := filepath.Join(backupPath,
		fmt.Sprintf("mediawiki-backup-%s-%s.tar", backupType, job.scheduled.Format("02.01.2006")))
	if err := runBackupScript(backupFile); err != nil {
		return err
	}
	log.Printf("%s filesystem backup completed: %s", backupType, backupFile)
	cleanupOldBackups(backupPath, retainCount)
	return nil
}

func ensureBackupPath(path string) (string, error) {
	_, err := os.Stat(path)
	if err == nil {
		return path, nil
	}
	if !os.IsNotExist(err) {
		return path, err
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return path, err
	}
	log.Printf("Created backup directory: %s", path)
	return path, nil
}

func getSchedule(config *Config, backupType string) BackupSchedule {
	switch backupType {
	case "daily":
		return config.Backups.Daily
	case "weekly":
		return config.Backups.Weekly
	case "monthly":
		return config.Backups.Monthly
	}
	return BackupSchedule{}
}

// isDueDay reports whether the tier applies to the day of now, ignoring the time of day.
func isDueDay(schedule BackupSchedule, backupType string, now time.Time) bool {
	switch backupType {
	case "daily":
		if len(schedule.DaysOfWeek) == 0 {
			return true
		}
		for _, name := range schedule.DaysOfWeek {
			if now.Weekday().String() == name {
				return true
			}
		}
		return false
	case "weekly":
		return now.Weekday().String() == schedule.DayOfWeek
	case "monthly":
		return now.Day() == schedule.DayOfMonth
	}
	return false
}

func isDueTime(schedule BackupSchedule, now time.Time) bool {
	scheduledTime, _ := time.Parse("15:04", schedule.Time)
	return now.Hour() == scheduledTime.Hour() && now.Minute() == scheduledTime.Minute()
}

func shouldRunBackup(schedule BackupSchedule, backupType string, now time.Time) bool {
	return isDueDay(schedule, backupType, now) && isDueTime(schedule, now)
}

// supersededBy returns the highest tier above backupType that is due today, or "".
// It depends only on the date, never on the time of day or call order, so each
// tier reaches the same decision at its own time slot.
func supersededBy(config *Config, backupType string, now time.Time) string {
	for i := len(backupTypes) - 1; i >= 0 && backupTypes[i] != backupType; i-- {
		higher := getSchedule(config, backupTypes[i])
		_, hasFilesystem := higher.filesystemRetainCount()
		if (hasFilesystem || higher.Restic != nil) && isDueDay(higher, backupTypes[i], now) {
			return backupTypes[i]
		}
	}
	return ""
}

// decideBackup reports whether backupType has to run at now. If it is scheduled
// for this minute but suppressed, reason explains why (for the log).
func decideBackup(config *Config, backupType string, now time.Time) (run bool, reason string) {
	schedule := getSchedule(config, backupType)
	if !isDueTime(schedule, now) {
		return false, ""
	}
	if !isDueDay(schedule, backupType, now) {
		if backupType == "daily" {
			return false, fmt.Sprintf("%s is not in daysOfWeek", now.Weekday())
		}
		return false, ""
	}
	if config.Exclusive {
		if higher := supersededBy(config, backupType, now); higher != "" {
			return false, fmt.Sprintf("%s is due today", higher)
		}
	}
	return true, ""
}

// dueBackups lists the tiers that run at now, in execution order.
func dueBackups(config *Config, now time.Time) []string {
	var due []string
	for _, backupType := range backupTypes {
		if run, _ := decideBackup(config, backupType, now); run {
			due = append(due, backupType)
		}
	}
	return due
}

// CheckStorage checks if there is enough free storage available on the mount of the backup directory
func CheckStorage(backupDirectory string, minPercentage float64) bool {
	var stat syscall.Statfs_t
	err := syscall.Statfs(backupDirectory, &stat)
	if err != nil {
		log.Printf("Error checking disk space for %s: %v", backupDirectory, err)
		return false
	}

	// Calculate percentage of free space
	freeSpace := stat.Bavail * uint64(stat.Bsize)
	totalSpace := stat.Blocks * uint64(stat.Bsize)
	percentageFree := float64(freeSpace) / float64(totalSpace) * 100

	log.Printf("Storage check: %.2f%% free space available on the backup mount", percentageFree)

	// Check if the available free space is above the minimum required
	if percentageFree < minPercentage {
		log.Printf("Warning: Insufficient storage. Required: %.2f%%, Available: %.2f%%", minPercentage, percentageFree)
		return false
	}

	return true
}

// partSuffix marks an archive that is still being written. Such files are never
// treated as backups: a run only renames its file to the final name on success.
const partSuffix = ".part"

func isPartFile(name string) bool {
	return strings.HasSuffix(name, partSuffix)
}

// backupEntries lists the completed backups in backupPath, ignoring *.part files.
func backupEntries(backupPath string) ([]os.DirEntry, error) {
	files, err := os.ReadDir(backupPath)
	if err != nil {
		return nil, err
	}
	complete := files[:0]
	for _, file := range files {
		if !isPartFile(file.Name()) {
			complete = append(complete, file)
		}
	}
	return complete, nil
}

// removeStalePartFiles deletes partial archives left behind by an interrupted run
// (e.g. the container was stopped mid-backup). Called once at startup.
func removeStalePartFiles(config *Config) {
	for _, backupType := range backupTypes {
		dir := filepath.Join(config.BackupDirectory, backupType)
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, file := range files {
			if !isPartFile(file.Name()) {
				continue
			}
			if err := os.Remove(filepath.Join(dir, file.Name())); err != nil {
				log.Printf("Error deleting stale partial backup %s: %v", file.Name(), err)
			} else {
				log.Printf("Deleted stale partial backup: %s", file.Name())
			}
		}
	}
}

func getLastBackupTime(backupPath string) (time.Time, error) {
	files, err := backupEntries(backupPath)
	if err != nil {
		return time.Time{}, err
	}

	// Get the most recent file
	var lastModTime time.Time
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(lastModTime) {
			lastModTime = info.ModTime()
		}
	}
	return lastModTime, nil
}

// runBackupScript lets the create script write to backupFile+".part" and renames
// it to backupFile only if the script succeeded. A failed run leaves no file behind.
func runBackupScript(backupFile string) error {
	partFile := backupFile + partSuffix
	os.Remove(partFile)

	cmd := execCommand(createScript)
	cmd.Env = append(os.Environ(), fmt.Sprintf("BACKUP_FILE=%s", partFile))

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Backup script output (error): %s", string(output))
		os.Remove(partFile)
		return fmt.Errorf("script error: %v", err)
	}
	log.Printf("Backup script output: %s", string(output))
	if err := os.Rename(partFile, backupFile); err != nil {
		os.Remove(partFile)
		return fmt.Errorf("finalizing %s: %v", backupFile, err)
	}
	return nil
}

// runLogged runs cmd with the process environment and logs its combined output.
func runLogged(label string, cmd *exec.Cmd) error {
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("%s output (error): %s", label, string(output))
		return fmt.Errorf("%s error: %v", label, err)
	}
	log.Printf("%s output: %s", label, string(output))
	return nil
}

// runResticBackup writes one run to the restic repository: the images directory and
// a plain (uncompressed, one INSERT per row, primary-key order) SQL dump from stdin, so
// that restic can deduplicate both. Both snapshots carry the tier tag and a common
// run-<scheduled> tag, so `restore restic` can restore them as a pair. The dump snapshot
// gets the "complete" tag only after mysqldump succeeded; restore ignores snapshots
// without it. Env vars (RESTIC_REPOSITORY, RESTIC_PASSWORD, AWS_*) must be set.
func runResticBackup(backupType string, retainCount int, scheduled time.Time) error {
	tags := []string{"--tag", backupType, "--tag", "run-" + scheduled.UTC().Format("20060102T150405Z")}
	if v := os.Getenv("MEDIAWIKI_BACKUP_VERSION"); v != "" {
		tags = append(tags, "--tag", "version-"+v)
	}

	imageArgs := append([]string{"backup", "--tag", "images"}, tags...)
	if err := runLogged("restic backup", execCommand("restic", append(imageArgs, "/var/www/html/images")...)); err != nil {
		return err
	}

	// The pending tag is unique per attempt, so a retry never completes the partial
	// snapshot of an earlier failed attempt.
	pending := fmt.Sprintf("pending-%d", time.Now().UnixNano())
	dumpTags := strings.Join(append([]string{"--tag", "db", "--tag", pending}, tags...), " ")
	if err := runLogged("restic db backup", execCommand("bash", "-c",
		`set -o pipefail; MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump -h "$MYSQL_HOST" --default-character-set=utf8 `+
			`--single-transaction --quick --order-by-primary wiki -c `+
			`| restic backup --stdin --stdin-filename wiki.sql `+dumpTags,
	)); err != nil {
		// Best effort: do not leave the partial dump in the repository.
		_ = runLogged("restic discard partial db backup", execCommand("restic", "forget", "--tag", pending))
		return err
	}
	if err := runLogged("restic mark db backup complete", execCommand("restic", "tag",
		"--tag", pending, "--add", "complete", "--remove", pending,
	)); err != nil {
		return err
	}

	// forget old snapshots according to retainCount for this backupType
	return runLogged("restic forget", execCommand("restic", "forget", "--prune",
		"--tag", backupType,
		fmt.Sprintf("--keep-%s", backupType), fmt.Sprintf("%d", retainCount),
	))
}

func cleanupOldBackups(backupPath string, retainCount int) {
	files, err := backupEntries(backupPath)
	if err != nil {
		log.Printf("Error reading backup directory: %v", err)
		return
	}

	// Sort files by modification time (oldest first)
	sort.Slice(files, func(i, j int) bool {
		infoI, _ := files[i].Info()
		infoJ, _ := files[j].Info()
		return infoI.ModTime().Before(infoJ.ModTime())
	})

	// Remove oldest files if exceeding retain count
	for len(files) > retainCount {
		oldest := files[0]
		if err := os.Remove(filepath.Join(backupPath, oldest.Name())); err != nil {
			log.Printf("Error deleting file %s: %v", oldest.Name(), err)
		} else {
			log.Printf("Deleted old backup: %s", oldest.Name())
		}
		files = files[1:]
	}
}
