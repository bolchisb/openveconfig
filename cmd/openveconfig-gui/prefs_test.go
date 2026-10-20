package main

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

// TestPrefStoreSetDoesNotWaitForDisk sets values while the disk write
// hangs, as when an antivirus holds the file.
func TestPrefStoreSetDoesNotWaitForDisk(t *testing.T) {
	p := newPrefStore("", nil)
	hang := make(chan struct{})
	defer close(hang)
	p.write = func([]byte) error { <-hang; return nil }

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 100 {
			p.SetInt("n", i)
			p.SetStringList(prefRecentFiles, []string{"a", "b"})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Set waited for the disk write")
	}
	if got := p.Int("n"); got != 99 {
		t.Errorf("Int = %d, want 99", got)
	}
	start := time.Now()
	p.flush(50 * time.Millisecond)
	if d := time.Since(start); d > time.Second {
		t.Errorf("flush waited %v past its timeout", d)
	}
}

// TestPrefStoreSavesAndReads writes values to a file and reads them back
// with their types, as a restart does.
func TestPrefStoreSavesAndReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), prefsDir, prefsFile)
	p := newPrefStore(path, nil)
	p.SetBool("b", true)
	p.SetInt("i", 7)
	p.SetFloat("f", 1.5)
	p.SetString("s", "x")
	p.SetStringList("sl", []string{"a", "b"})
	p.SetIntList("il", []int{1, 2})
	p.SetBool("gone", true)
	p.RemoveValue("gone")
	p.flush(5 * time.Second)

	q := newPrefStore(path, nil)
	if !q.Bool("b") || q.Int("i") != 7 || q.Float("f") != 1.5 || q.String("s") != "x" {
		t.Errorf("read b=%v i=%d f=%v s=%q", q.Bool("b"), q.Int("i"), q.Float("f"), q.String("s"))
	}
	if got := q.StringList("sl"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("StringList = %v", got)
	}
	if got := q.IntList("il"); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("IntList = %v", got)
	}
	if q.BoolWithFallback("gone", false) {
		t.Error("removed value was read back")
	}
}

// TestPrefStoreImportsOldStore starts a new file from the values of
// Fyne's store, once.
func TestPrefStoreImportsOldStore(t *testing.T) {
	old := test.NewTempApp(t).Preferences()
	old.SetString(prefTheme, "dark")
	path := filepath.Join(t.TempDir(), prefsFile)

	p := newPrefStore(path, old)
	if got := p.String(prefTheme); got != "dark" {
		t.Fatalf("imported theme = %q, want dark", got)
	}
	p.flush(5 * time.Second)
	old.SetString(prefTheme, "light")
	if got := newPrefStore(path, old).String(prefTheme); got != "dark" {
		t.Errorf("theme after restart = %q, want dark from the new file", got)
	}
}

// TestPrefStoreListeners calls the listeners on a change only.
func TestPrefStoreListeners(t *testing.T) {
	p := newPrefStore("", nil)
	calls := 0
	p.AddChangeListener(func() { calls++ })
	p.SetString("s", "a")
	p.SetString("s", "a")
	p.RemoveValue("s")
	p.RemoveValue("s")
	if calls != 2 {
		t.Errorf("listener called %d times, want 2", calls)
	}
}
