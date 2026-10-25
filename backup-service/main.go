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
}

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

	// Run as a service
	for {
		runDueBackups(config, time.Now())
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

	retainCount, hasFilesystem := schedule.filesystemRetainCount()
	if hasFilesystem {
		backupPath, err := ensureBackupPath(filepath.Join(config.BackupDirectory, backupType))
		if err != nil {
			log.Fatalf("Error creating backup directory %s: %v", backupPath, err)
		}

		lastBackup, err := getLastBackupTime(backupPath)
		if err == nil && now.Sub(lastBackup) < time.Minute {
			log.Printf("%s filesystem backup skipped: backup with timestamp %s already exists", backupType, lastBackup)
		} else if !CheckStorage(config.BackupDirectory, config.MinStoragePercentage) {
			log.Printf("%s filesystem backup skipped: not enough free storage", backupType)
		} else {
			backupFile := filepath.Join(backupPath, fmt.Sprintf("mediawiki-backup-%s-%s.tar", backupType, now.Format("02.01.2006")))
			if err := runBackupScript(backupFile); err != nil {
				log.Printf("Error running %s filesystem backup: %v", backupType, err)
			} else {
				log.Printf("%s filesystem backup completed: %s", backupType, backupFile)
				cleanupOldBackups(backupPath, retainCount)
			}
		}
	}

	if schedule.Restic != nil {
		if err := runResticBackup(backupType, schedule.Restic.RetainCount); err != nil {
			log.Printf("Error running %s restic backup: %v", backupType, err)
		} else {
			log.Printf("%s restic backup completed", backupType)
		}
	}
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

func getLastBackupTime(backupPath string) (time.Time, error) {
	files, err := os.ReadDir(backupPath)
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

func runBackupScript(backupFile string) error {
	cmd := execCommand(createScript)
	cmd.Env = append(os.Environ(), fmt.Sprintf("BACKUP_FILE=%s", backupFile))

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Backup script output (error): %s", string(output))
		return fmt.Errorf("script error: %v", err)
	}
	log.Printf("Backup script output: %s", string(output))
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

func runResticBackup(backupType string, retainCount int) error {
	// restic backup — env vars (RESTIC_REPOSITORY, RESTIC_PASSWORD, AWS_*) must be set
	if err := runLogged("restic backup", execCommand("restic", "backup",
		"--tag", backupType,
		"/var/www/html/images",
	)); err != nil {
		return err
	}

	// restic backup mysql dump via stdin
	if err := runLogged("restic db backup", execCommand("bash", "-c",
		`MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump -h "$MYSQL_HOST" --default-character-set=utf8 --single-transaction --quick wiki -c | bzip2 -z9 | restic backup --stdin --stdin-filename wiki.sql.bz2 --tag `+backupType,
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
	files, err := os.ReadDir(backupPath)
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
