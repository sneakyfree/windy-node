// Package config is where windy-node keeps its identity and state. The node token lives in its own 0600 file.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Paths: /etc/windy-node + /var/lib/windy-node for the system service (root); ~/.config/windy-node otherwise.
type Paths struct{ Dir, State string }

func Default() Paths {
	if d := os.Getenv("WINDY_NODE_DIR"); d != "" {
		return Paths{Dir: d, State: d}
	}
	if os.Geteuid() == 0 {
		return Paths{Dir: "/etc/windy-node", State: "/var/lib/windy-node"}
	}
	home, _ := os.UserConfigDir()
	d := filepath.Join(home, "windy-node")
	return Paths{Dir: d, State: d}
}

func (p Paths) TokenFile() string  { return filepath.Join(p.Dir, "token") }
func (p Paths) NodeFile() string   { return filepath.Join(p.Dir, "node.json") }
func (p Paths) OptOutFile() string { return filepath.Join(p.Dir, "no-commands") }
func (p Paths) Outbox() string     { return filepath.Join(p.State, "outbox.jsonl") }

// Node is the non-secret identity: which node this is and which Mind it reports to.
type Node struct {
	NodeID  string `json:"node_id"`
	MindURL string `json:"mind_url"`
}

var ErrNotEnrolled = errors.New("this machine is not enrolled yet: run `windy-node enrol --code <code>`")

func (p Paths) Save(n Node, token string) error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return err
	}
	if err := writeFile(p.TokenFile(), []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(n, "", "  ")
	return writeFile(p.NodeFile(), append(b, '\n'), 0o600)
}

func (p Paths) Load() (Node, string, error) {
	var n Node
	b, err := os.ReadFile(p.NodeFile())
	if os.IsNotExist(err) {
		return n, "", ErrNotEnrolled
	}
	if err != nil {
		return n, "", err
	}
	if err := json.Unmarshal(b, &n); err != nil {
		return n, "", err
	}
	t, err := os.ReadFile(p.TokenFile())
	if err != nil {
		return n, "", ErrNotEnrolled
	}
	return n, strings.TrimSpace(string(t)), nil
}

// CommandsAllowed is false whenever the owner's local opt-out file exists (checked before every command).
func (p Paths) CommandsAllowed() bool {
	_, err := os.Stat(p.OptOutFile())
	return os.IsNotExist(err)
}

func writeFile(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
