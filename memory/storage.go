package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

func appendJSONL(path string, v any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func loadJSONL[T any](path string, maxLines int) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []T{}, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// allow large lines (analysis could get long)
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 8*1024*1024)

	out := make([]T, 0, 256)
	for sc.Scan() {
		if maxLines > 0 && len(out) >= maxLines {
			break
		}
		var item T
		if err := json.Unmarshal(sc.Bytes(), &item); err != nil {
			continue
		}
		out = append(out, item)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func writeJSONFile(path string, v any) error {
	tmp := path + ".tmp"
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func analysisFilePath(baseDir, tradeID string) string {
	return filepath.Join(baseDir, "analyses", fmt.Sprintf("%s.json", tradeID))
}
