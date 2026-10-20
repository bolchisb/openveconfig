package main

// The preferences, kept in memory and written to disk on a goroutine of
// their own.

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"fyne.io/fyne/v2"
)

// prefsDir and prefsFile name the file the preferences are kept in, under
// the user's configuration directory.
const prefsDir, prefsFile = "OpenVeConfig", "preferences.json"

// prefStore implements fyne.Preferences. Fyne's own store rewrites its file
// on the goroutine that sets a value, so a write that hangs (an antivirus
// holding the file, a slow disk) froze the window. prefStore keeps the
// values in memory and hands every change to one writer goroutine, so
// setting a value never waits for the disk.
type prefStore struct {
	mu        sync.RWMutex
	values    map[string]any
	listeners []func()
	// changes counts the changes; saved is the count the last write held.
	changes, saved uint64
	dirty          chan struct{} // wakes the writer; holds at most one signal
	write          func(data []byte) error
}

var _ fyne.Preferences = (*prefStore)(nil)

// prefsApp is the app with its preferences replaced by a prefStore.
type prefsApp struct {
	fyne.App
	prefs *prefStore
}

func (a *prefsApp) Preferences() fyne.Preferences { return a.prefs }

// prefsPath returns the file the preferences are kept in, or "" when the
// system has no configuration directory.
func prefsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		log.Printf("no directory for the preferences, they are not saved: %v", err)
		return ""
	}
	return filepath.Join(dir, prefsDir, prefsFile)
}

// newPrefStore returns a store that keeps its values in path, read now.
// When path does not exist yet, the store starts with the values of old,
// the store of an earlier version, and saves them to path. With path ""
// nothing is read or saved.
func newPrefStore(path string, old fyne.Preferences) *prefStore {
	p := &prefStore{values: map[string]any{}, dirty: make(chan struct{}, 1),
		write: func([]byte) error { return nil }}
	if path != "" {
		p.write = func(data []byte) error { return writeAtomic(path, data) }
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(data, &p.values); err != nil {
				log.Printf("reading the preferences: %v", err)
			}
			convertLists(p.values)
		case errors.Is(err, os.ErrNotExist):
			// Fyne's store offers its values outside fyne.Preferences.
			if o, ok := old.(interface{ ReadValues(func(map[string]any)) }); ok {
				o.ReadValues(func(m map[string]any) {
					for k, v := range m {
						p.values[k] = v
					}
				})
				p.changes++
				p.dirty <- struct{}{}
			}
		default:
			log.Printf("reading the preferences: %v", err)
		}
	}
	go p.run()
	return p
}

// convertLists turns the lists JSON decodes as []any into the typed lists
// the getters look for, as Fyne's store does.
func convertLists(values map[string]any) {
	for k, v := range values {
		items, ok := v.([]any)
		if !ok || len(items) == 0 {
			continue
		}
		switch items[0].(type) {
		case bool:
			values[k] = typedList[bool](items)
		case float64:
			values[k] = typedList[float64](items)
		case string:
			values[k] = typedList[string](items)
		}
	}
}

// typedList returns items as a []T, or items itself if one is not a T.
func typedList[T any](items []any) any {
	out := make([]T, 0, len(items))
	for _, it := range items {
		t, ok := it.(T)
		if !ok {
			return items
		}
		out = append(out, t)
	}
	return out
}

// run writes the values each time they change. Changes made while a write
// runs are written together by the next one.
func (p *prefStore) run() {
	for range p.dirty {
		p.mu.RLock()
		data, err := json.Marshal(p.values)
		n := p.changes
		p.mu.RUnlock()
		if err == nil {
			err = p.write(data)
		}
		if err != nil {
			log.Printf("saving the preferences: %v", err)
		}
		p.mu.Lock()
		p.saved = n
		p.mu.Unlock()
	}
}

// flush waits until the changes made so far are written, or timeout has
// passed.
func (p *prefStore) flush(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p.mu.RLock()
		done := p.saved == p.changes
		p.mu.RUnlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// writeAtomic replaces path with data. It writes a new file and renames it
// over path, so that path is never left half written.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, prefsFile+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err2 := f.Close(); err == nil {
		err = err2
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

func (p *prefStore) get(key string) (any, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.values[key]
	return v, ok
}

// set stores value and, if it changed, wakes the writer and calls the
// listeners. It does not wait for the write.
func (p *prefStore) set(key string, value any) {
	p.mu.Lock()
	if old, ok := p.values[key]; ok && reflect.DeepEqual(old, value) {
		p.mu.Unlock()
		return
	}
	p.values[key] = value
	p.changedLocked()
}

// changedLocked counts a change made under p.mu, unlocks p.mu, wakes the
// writer and calls the listeners.
func (p *prefStore) changedLocked() {
	p.changes++
	listeners := p.listeners
	p.mu.Unlock()
	select {
	case p.dirty <- struct{}{}:
	default: // the writer is already due to run
	}
	for _, l := range listeners {
		l()
	}
}

// value returns the value of key if it is a T, else fallback.
func value[T any](p *prefStore, key string, fallback T) T {
	if v, ok := p.get(key); ok {
		if t, ok := v.(T); ok {
			return t
		}
	}
	return fallback
}

func (p *prefStore) Bool(key string) bool { return p.BoolWithFallback(key, false) }
func (p *prefStore) BoolWithFallback(key string, fallback bool) bool {
	return value(p, key, fallback)
}
func (p *prefStore) SetBool(key string, v bool) { p.set(key, v) }
func (p *prefStore) BoolList(key string) []bool { return p.BoolListWithFallback(key, []bool{}) }
func (p *prefStore) BoolListWithFallback(key string, fallback []bool) []bool {
	return value(p, key, fallback)
}
func (p *prefStore) SetBoolList(key string, v []bool) { p.set(key, slices.Clone(v)) }

func (p *prefStore) Float(key string) float64 { return p.FloatWithFallback(key, 0) }
func (p *prefStore) FloatWithFallback(key string, fallback float64) float64 {
	return value(p, key, fallback)
}
func (p *prefStore) SetFloat(key string, v float64) { p.set(key, v) }
func (p *prefStore) FloatList(key string) []float64 { return p.FloatListWithFallback(key, []float64{}) }
func (p *prefStore) FloatListWithFallback(key string, fallback []float64) []float64 {
	return value(p, key, fallback)
}
func (p *prefStore) SetFloatList(key string, v []float64) { p.set(key, slices.Clone(v)) }

func (p *prefStore) Int(key string) int { return p.IntWithFallback(key, 0) }

// IntWithFallback also reads the float64 that JSON decodes a number as.
func (p *prefStore) IntWithFallback(key string, fallback int) int {
	v, _ := p.get(key)
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return fallback
}
func (p *prefStore) SetInt(key string, v int) { p.set(key, v) }
func (p *prefStore) IntList(key string) []int { return p.IntListWithFallback(key, []int{}) }

// IntListWithFallback also reads the []float64 that JSON decodes a list of
// numbers as.
func (p *prefStore) IntListWithFallback(key string, fallback []int) []int {
	v, _ := p.get(key)
	switch l := v.(type) {
	case []int:
		return l
	case []float64:
		ints := make([]int, len(l))
		for i, f := range l {
			ints[i] = int(f)
		}
		return ints
	}
	return fallback
}
func (p *prefStore) SetIntList(key string, v []int) { p.set(key, slices.Clone(v)) }

func (p *prefStore) String(key string) string { return p.StringWithFallback(key, "") }
func (p *prefStore) StringWithFallback(key, fallback string) string {
	return value(p, key, fallback)
}
func (p *prefStore) SetString(key string, v string) { p.set(key, v) }
func (p *prefStore) StringList(key string) []string { return p.StringListWithFallback(key, []string{}) }
func (p *prefStore) StringListWithFallback(key string, fallback []string) []string {
	return value(p, key, fallback)
}
func (p *prefStore) SetStringList(key string, v []string) { p.set(key, slices.Clone(v)) }

func (p *prefStore) RemoveValue(key string) {
	p.mu.Lock()
	if _, ok := p.values[key]; !ok {
		p.mu.Unlock()
		return
	}
	delete(p.values, key)
	p.changedLocked()
}

func (p *prefStore) AddChangeListener(f func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listeners = append(p.listeners, f)
}

func (p *prefStore) ChangeListeners() []func() {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return slices.Clone(p.listeners)
}
