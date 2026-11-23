package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const nodeIDFile = "node-id"

// LoadNodeID returns the node's stable identity. override (--node-id) wins.
// Otherwise it reads <stateDir>/node-id, and if the file is missing it creates
// one with 128 random bits, hex encoded. The IP address is not part of the
// identity, so the ID survives DHCP changes.
func LoadNodeID(stateDir, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	dir, err := ExpandHome(stateDir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, nodeIDFile)
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id == "" {
			return "", fmt.Errorf("%s is empty; delete it to create a new node ID", path)
		}
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// Another process created it between our read and create. Use its value.
			return LoadNodeID(stateDir, "")
		}
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(id + "\n"); err != nil {
		return "", err
	}
	return id, nil
}
