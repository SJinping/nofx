package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func decisionLogFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "trader_123-abc")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return root, dir
}

func putDecisionLog(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"cycle_number":1}`), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionLogListStrictFiltering(t *testing.T) {
	root, dir := decisionLogFixture(t)
	valid := []string{"decision_20240229_235959_cycle1.json", "decision_20260301_010203_cycle2.json", "decision_20260301_010203_cycle10.json"}
	for _, name := range valid {
		putDecisionLog(t, filepath.Join(dir, name))
	}
	invalid := []string{
		"account.json", "equity_history.json", "decision_20260229_010203_cycle1.json",
		"decision_20261301_010203_cycle1.json", "decision_20260301_240000_cycle1.json",
		"decision_20260301_016000_cycle1.json", "decision_20260301_010260_cycle1.json",
		"decision_20260301_010203_cycle0.json", "decision_20260301_010203_cycle-1.json",
		"decision_20260301_010203_cycle+1.json", "decision_20260301_010203_cycle01.json",
		"decision_20260301_010203_cycle999999999999999999999999999999.json",
		"decision_20260301_010203_cycle1_extra.json", "decision_20260301_010203_cycle1.json.bak",
		"other_20260301_010203_cycle1.json", "decision_2026031_010203_cycle1.json",
		"decision_20260301_10203_cycle1.json", "decision_20260301_010203_cycle1.JSON",
		"decision_00000101_010203_cycle1.json",
	}
	for _, name := range invalid {
		putDecisionLog(t, filepath.Join(dir, name))
	}
	if err := os.Mkdir(filepath.Join(dir, "decision_20260101_000000_cycle1.json"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	putDecisionLog(t, outside)
	if err := os.Symlink(outside, filepath.Join(dir, "decision_20260102_000000_cycle1.json")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "decision_20260103_000000_cycle1.json"), 0600); err != nil {
		t.Fatal(err)
	}
	logs, err := listDecisionLogFiles(root, filepath.Base(dir))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{valid[2], valid[1], valid[0]}
	var got []string
	for _, entry := range logs {
		got = append(got, entry.Filename)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filenames = %v; want %v", got, want)
	}
	if logs[0].Cycle != 10 || logs[0].Size != int64(len(`{"cycle_number":1}`)) || !logs[0].Timestamp.Equal(time.Date(2026, 3, 1, 1, 2, 3, 0, time.UTC)) {
		t.Fatalf("incorrect metadata: %+v", logs[0])
	}
	for _, name := range invalid {
		if _, err := readDecisionLogFile(root, filepath.Base(dir), name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("read %q: got %v, want invalid", name, err)
		}
	}
}

func TestDecisionLogEmptyListIsJSONArray(t *testing.T) {
	root, dir := decisionLogFixture(t)
	putDecisionLog(t, filepath.Join(dir, "unrelated.json"))
	logs, err := listDecisionLogFiles(root, filepath.Base(dir))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(logs)
	if err != nil || string(data) != "[]" {
		t.Fatalf("JSON = %s, error = %v", data, err)
	}
}

func TestDecisionLogReadRegularFile(t *testing.T) {
	root, dir := decisionLogFixture(t)
	name := "decision_20261031_123456_cycle7.json"
	putDecisionLog(t, filepath.Join(dir, name))
	got, err := readDecisionLogFile(root, filepath.Base(dir), name)
	if err != nil || string(got) != `{"cycle_number":1}` {
		t.Fatalf("read = %q, %v", got, err)
	}
	if _, err := readDecisionLogFile(root, filepath.Base(dir), "decision_20261031_123456_cycle8.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file error = %v", err)
	}
	if _, err := listDecisionLogFiles(root, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing trader error = %v", err)
	}
}

func TestDecisionLogRejectTraversal(t *testing.T) {
	root, dir := decisionLogFixture(t)
	name := "decision_20261031_123456_cycle7.json"
	putDecisionLog(t, filepath.Join(dir, name))
	for _, trader := range []string{"", ".", "..", "../trader_123-abc", "trader_123-abc/..", "a/b", `a\b`, "/tmp", "trader.name", ".hidden", "bad\x00id"} {
		t.Run("trader_"+strings.ReplaceAll(trader, "/", "_"), func(t *testing.T) {
			if _, err := listDecisionLogFiles(root, trader); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("list error = %v", err)
			}
			if _, err := readDecisionLogFile(root, trader, name); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("read error = %v", err)
			}
		})
	}
	for _, filename := range []string{"", "..", "../" + name, "nested/" + name, `nested\` + name, "/" + name, name + "\x00"} {
		if _, err := readDecisionLogFile(root, filepath.Base(dir), filename); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("filename %q error = %v", filename, err)
		}
	}
}

func TestDecisionLogRejectNonRegularFiles(t *testing.T) {
	root, dir := decisionLogFixture(t)
	name := "decision_20261031_123456_cycle7.json"
	for _, kind := range []string{"directory", "symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(dir, name)
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside.json")
				putDecisionLog(t, outside)
				err = os.Symlink(outside, path)
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Remove(path) })
			if _, err := readDecisionLogFile(root, filepath.Base(dir), name); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("read error = %v; want invalid", err)
			}
			logs, err := listDecisionLogFiles(root, filepath.Base(dir))
			if err != nil || len(logs) != 0 {
				t.Fatalf("list = %v, %v", logs, err)
			}
		})
	}
}

func TestDecisionLogRejectSymlinkDirectories(t *testing.T) {
	root, dir := decisionLogFixture(t)
	name := "decision_20261031_123456_cycle7.json"
	putDecisionLog(t, filepath.Join(dir, name))
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(root, linkedRoot); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ root, trader string }{{root, "alias"}, {linkedRoot, filepath.Base(dir)}}
	container := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(container, "intermediate")); err != nil {
		t.Fatal(err)
	}
	cases = append(cases, struct{ root, trader string }{filepath.Join(container, "intermediate", "nested"), "missing"})
	for _, tc := range cases {
		if _, err := listDecisionLogFiles(tc.root, tc.trader); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("list %q/%q error = %v", tc.root, tc.trader, err)
		}
		if _, err := readDecisionLogFile(tc.root, tc.trader, name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("read %q/%q error = %v", tc.root, tc.trader, err)
		}
	}
}
