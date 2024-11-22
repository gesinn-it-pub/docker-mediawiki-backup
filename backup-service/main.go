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

type BackupSchedule struct {
	RetainCount int    `json:"retainCount"`
	Time        string `json:"time"`
	DayOfWeek   string `json:"dayOfWeek,omitempty"`
	DayOfMonth  int    `json:"dayOfMonth,omitempty"`
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
	backupPath := ensureBackupPath(filepath.Join(config.BackupDirectory, backupType))
	schedule := getSchedule(config, backupType)

	if !shouldRunBackup(schedule, backupType, now) {
		return
	}

	lastBackup, err := getLastBackupTime(backupPath)
	if err == nil && time.Since(lastBackup) < time.Minute {
		log.Printf("%s backup skipped: backup with timestamp %s already exists", backupType, lastBackup)
		return // Skip if a backup was recently created
	}

	// check if enough free storage is avaliable
	if !CheckStorage(config.BackupDirectory, config.MinStoragePercentage) {
		log.Printf("Backup skipped for %s: Not enough free storage", backupType)
		return
	}

	backupFile := filepath.Join(backupPath, fmt.Sprintf("mediawiki-backup-%s-%s.tar", backupType, now.Format("02.01.2006")))
	err = runBackupScript(backupFile)
	if err != nil {
		log.Printf("Error running %s backup: %v", backupType, err)
	} else {
		log.Printf("%s backup completed: %s", backupType, backupFile)
		cleanupOldBackups(backupPath, schedule.RetainCount)
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
