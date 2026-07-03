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
}

type StorageTarget struct {
	RetainCount int `json:"retainCount"`
}

type BackupSchedule struct {
	// Legacy: retainCount directly on the schedule (filesystem-only, backward compatible)
	RetainCount int            `json:"retainCount"`
	Time        string         `json:"time"`
	DayOfWeek   string         `json:"dayOfWeek,omitempty"`
	DayOfMonth  int            `json:"dayOfMonth,omitempty"`
	Filesystem  *StorageTarget `json:"filesystem,omitempty"`
	Restic      *StorageTarget `json:"restic,omitempty"`
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

func main() {
	// Load configuration
	config, err := loadConfig("/backup.json")
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	if config.BackupDirectory == "" {
		log.Println("BackupDirectory is undefined, defaulting to /backup")
		config.BackupDirectory = "/backup"
	}
	if config.MinStoragePercentage == 0.0 {
		log.Println("MinStoragePercentage is undefined, defaulting to 20%")
		config.MinStoragePercentage = 20.0
	}

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
		now := time.Now()
		for _, backupType := range []string{"daily", "weekly", "monthly"} {
			handleBackup(config, backupType, now)
		}
		time.Sleep(1 * time.Minute)
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
	return &config, nil
}

func handleBackup(config *Config, backupType string, now time.Time) {
	schedule := getSchedule(config, backupType)

	if !shouldRunBackup(schedule, backupType, now) {
		return
	}

	retainCount, hasFilesystem := schedule.filesystemRetainCount()
	if hasFilesystem {
		backupPath := ensureBackupPath(filepath.Join(config.BackupDirectory, backupType))

		lastBackup, err := getLastBackupTime(backupPath)
		if err == nil && time.Since(lastBackup) < time.Minute {
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

func ensureBackupPath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0755); err != nil {
			log.Fatalf("Error creating backup directory %s: %v", path, err)
		}
		log.Printf("Created backup directory: %s", path)
	}
	return path
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

func shouldRunBackup(schedule BackupSchedule, backupType string, now time.Time) bool {
	scheduledTime, _ := time.Parse("15:04", schedule.Time)
	switch backupType {
	case "daily":
		return now.Hour() == scheduledTime.Hour() && now.Minute() == scheduledTime.Minute()
	case "weekly":
		return now.Weekday().String() == schedule.DayOfWeek &&
			now.Hour() == scheduledTime.Hour() && now.Minute() == scheduledTime.Minute()
	case "monthly":
		return now.Day() == schedule.DayOfMonth &&
			now.Hour() == scheduledTime.Hour() && now.Minute() == scheduledTime.Minute()
	}
	return false
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
	cmd := exec.Command("/usr/local/bin/create")
	cmd.Env = append(os.Environ(), fmt.Sprintf("BACKUP_FILE=%s", backupFile))

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Backup script output (error): %s", string(output))
		return fmt.Errorf("script error: %v", err)
	}
	log.Printf("Backup script output: %s", string(output))
	return nil
}

func runResticBackup(backupType string, retainCount int) error {
	// restic backup — env vars (RESTIC_REPOSITORY, RESTIC_PASSWORD, AWS_*) must be set
	backupCmd := exec.Command("restic", "backup",
		"--tag", backupType,
		"/var/www/html/images",
	)
	backupCmd.Env = os.Environ()
	if output, err := backupCmd.CombinedOutput(); err != nil {
		log.Printf("restic backup output (error): %s", string(output))
		return fmt.Errorf("restic backup error: %v", err)
	} else {
		log.Printf("restic backup output: %s", string(output))
	}

	// restic backup mysql dump via stdin
	dumpCmd := exec.Command("bash", "-c",
		`MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump -h "$MYSQL_HOST" --default-character-set=utf8 --single-transaction --quick wiki -c | bzip2 -z9 | restic backup --stdin --stdin-filename wiki.sql.bz2 --tag `+backupType,
	)
	dumpCmd.Env = os.Environ()
	if output, err := dumpCmd.CombinedOutput(); err != nil {
		log.Printf("restic db backup output (error): %s", string(output))
		return fmt.Errorf("restic db backup error: %v", err)
	} else {
		log.Printf("restic db backup output: %s", string(output))
	}

	// forget old snapshots according to retainCount for this backupType
	keepFlag := fmt.Sprintf("--keep-%s", backupType)
	forgetCmd := exec.Command("restic", "forget", "--prune",
		"--tag", backupType,
		keepFlag, fmt.Sprintf("%d", retainCount),
	)
	forgetCmd.Env = os.Environ()
	if output, err := forgetCmd.CombinedOutput(); err != nil {
		log.Printf("restic forget output (error): %s", string(output))
		return fmt.Errorf("restic forget error: %v", err)
	} else {
		log.Printf("restic forget output: %s", string(output))
	}

	return nil
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
