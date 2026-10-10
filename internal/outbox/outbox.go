// Package outbox keeps readings on disk while Mind can't take them (offline, or the route isn't there yet) and
// hands them back oldest first. It holds at most Cap entries; the oldest are dropped first.
package outbox

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Outbox struct {
	Path string
	Cap  int
	mu   sync.Mutex
}

func (o *Outbox) load() ([]json.RawMessage, error) {
	b, err := os.ReadFile(o.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []json.RawMessage
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || !json.Valid(line) {
			continue // a torn last line after a crash is skipped, not fatal
		}
		out = append(out, append(json.RawMessage(nil), line...))
	}
	return out, sc.Err()
}

func (o *Outbox) save(items []json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(o.Path), 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, it := range items {
		buf.Write(it)
		buf.WriteByte('\n')
	}
	tmp := o.Path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, o.Path)
}

// Add appends one item, dropping the oldest beyond Cap.
func (o *Outbox) Add(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return err
	}
	items = append(items, b)
	if o.Cap > 0 && len(items) > o.Cap {
		items = items[len(items)-o.Cap:]
	}
	return o.save(items)
}

// Peek returns up to n of the oldest items without removing them.
func (o *Outbox) Peek(n int) ([]json.RawMessage, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil || len(items) <= n {
		return items, err
	}
	return items[:n], nil
}

// Ack removes the n oldest items (after Mind accepted them).
func (o *Outbox) Ack(n int) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	items, err := o.load()
	if err != nil {
		return err
	}
	if n > len(items) {
		n = len(items)
	}
	return o.save(items[n:])
}

// Len is how many items wait.
func (o *Outbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	items, _ := o.load()
	return len(items)
}
