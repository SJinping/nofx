package api

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var decisionLogFilenamePattern = regexp.MustCompile(`^decision_([0-9]{8})_([0-9]{6})_cycle([1-9][0-9]*)\.json$`)

// parseDecisionLogFilename accepts only the canonical format emitted by the
// decision logger. Invalid calendar dates and cycles that overflow int fail closed.
func parseDecisionLogFilename(filename string) (time.Time, int, error) {
	parts := decisionLogFilenamePattern.FindStringSubmatch(filename)
	if parts == nil {
		return time.Time{}, 0, fmt.Errorf("%w: invalid decision log filename", fs.ErrInvalid)
	}
	timestamp, err := time.Parse("20060102_150405", parts[1]+"_"+parts[2])
	if err != nil || timestamp.Year() < 1 {
		return time.Time{}, 0, fmt.Errorf("%w: invalid decision log timestamp", fs.ErrInvalid)
	}
	cycle, err := strconv.Atoi(parts[3])
	if err != nil || cycle <= 0 {
		return time.Time{}, 0, fmt.Errorf("%w: invalid decision log cycle", fs.ErrInvalid)
	}
	return timestamp, cycle, nil
}

// openDecisionLogDirectory opens each component without following symlinks.
// Descriptor-relative opens also prevent directory replacement from redirecting
// subsequent reads outside the opened directory. root is a server-owned path,
// never a request parameter. Trader IDs must be single, dot-free basenames.
func openDecisionLogDirectory(root, traderID string) (*os.File, error) {
	if traderID == "" || strings.ContainsAny(traderID, ". /\\\x00") || filepath.Base(traderID) != traderID {
		return nil, fmt.Errorf("%w: invalid trader_id", fs.ErrInvalid)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	current, err := os.Open(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	components := strings.Split(strings.TrimPrefix(absolute, string(filepath.Separator)), string(filepath.Separator))
	components = append(components, traderID)
	for _, component := range components {
		if component == "" {
			continue
		}
		fd, openErr := syscall.Openat(int(current.Fd()), component, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		current.Close()
		if openErr != nil {
			return nil, decisionLogOpenError(openErr)
		}
		current = os.NewFile(uintptr(fd), component)
	}
	return current, nil
}

func decisionLogOpenError(err error) error {
	if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) {
		return fmt.Errorf("%w: symlink or non-directory path component: %v", fs.ErrInvalid, err)
	}
	return err
}

// openDecisionLogRegularFile never follows the final symlink or blocks on a FIFO.
func openDecisionLogRegularFile(dir *os.File, filename string) (*os.File, os.FileInfo, error) {
	fd, err := syscall.Openat(int(dir.Fd()), filename, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, decisionLogOpenError(err)
	}
	file := os.NewFile(uintptr(fd), filename)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, fmt.Errorf("%w: decision log is not a regular file", fs.ErrInvalid)
	}
	return file, info, nil
}

// listDecisionLogFiles returns only canonical, regular decision files. Empty
// directories return a non-nil slice (JSON []), not null. Timestamp descending,
// cycle descending, then filename ascending defines deterministic newest-first order.
func listDecisionLogFiles(root, traderID string) ([]LogEntry, error) {
	dir, err := openDecisionLogDirectory(root, traderID)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	logs := make([]LogEntry, 0)
	for _, entry := range entries {
		timestamp, cycle, err := parseDecisionLogFilename(entry.Name())
		if err != nil || entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			continue
		}
		file, info, err := openDecisionLogRegularFile(dir, entry.Name())
		if err != nil {
			continue
		}
		file.Close()
		logs = append(logs, LogEntry{Filename: entry.Name(), Cycle: cycle, Timestamp: timestamp, Size: info.Size()})
	}
	sort.Slice(logs, func(i, j int) bool {
		if !logs[i].Timestamp.Equal(logs[j].Timestamp) {
			return logs[i].Timestamp.After(logs[j].Timestamp)
		}
		if logs[i].Cycle != logs[j].Cycle {
			return logs[i].Cycle > logs[j].Cycle
		}
		return logs[i].Filename < logs[j].Filename
	})
	return logs, nil
}

// readDecisionLogFile applies exactly the same filename and filesystem policy
// as listing. Invalid inputs/nonregular files wrap fs.ErrInvalid; missing paths
// preserve fs.ErrNotExist for HTTP error mapping by the caller.
func readDecisionLogFile(root, traderID, filename string) ([]byte, error) {
	if _, _, err := parseDecisionLogFilename(filename); err != nil {
		return nil, err
	}
	dir, err := openDecisionLogDirectory(root, traderID)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	file, _, err := openDecisionLogRegularFile(dir, filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}
