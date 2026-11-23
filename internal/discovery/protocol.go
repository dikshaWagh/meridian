// Package discovery implements the UDP leader discovery messages. UDP carries
// only DISCOVER_LEADER and LEADER_FOUND; nothing else goes over it.
package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dikshaWagh/meridian/internal/control"
)

const (
	TypeDiscoverLeader = "DISCOVER_LEADER"
	TypeLeaderFound    = "LEADER_FOUND"
	// MaxDatagramBytes is the largest payload accepted. Larger datagrams are dropped.
	MaxDatagramBytes = 1024
)

// DiscoverLeader is the worker's broadcast probe.
type DiscoverLeader struct {
	Type            string `json:"type"`
	ProtocolVersion int    `json:"protocol_version"`
	MessageID       string `json:"message_id"`
	NodeID          string `json:"node_id"`
	Timestamp       string `json:"timestamp"`
	Sig             string `json:"sig"`
}

// LeaderFound is the leader's unicast reply. It names the TCP control port.
type LeaderFound struct {
	Type            string `json:"type"`
	ProtocolVersion int    `json:"protocol_version"`
	MessageID       string `json:"message_id"`
	LeaderID        string `json:"leader_id"`
	Address         string `json:"address"`
	ControlPort     int    `json:"control_port"`
	Timestamp       string `json:"timestamp"`
	Sig             string `json:"sig"`
}

// NewDiscoverLeader builds a signed probe for nodeID.
func NewDiscoverLeader(secret []byte, nodeID string, now time.Time) ([]byte, error) {
	m := DiscoverLeader{
		Type:            TypeDiscoverLeader,
		ProtocolVersion: control.ProtocolVersion,
		MessageID:       control.NewMessageID(),
		NodeID:          nodeID,
		Timestamp:       now.UTC().Format(time.RFC3339),
	}
	sig, err := control.Signature(secret, m)
	if err != nil {
		return nil, err
	}
	m.Sig = sig
	return json.Marshal(m)
}

// NewLeaderFound builds a signed reply.
func NewLeaderFound(secret []byte, leaderID, address string, controlPort int, now time.Time) ([]byte, error) {
	m := LeaderFound{
		Type:            TypeLeaderFound,
		ProtocolVersion: control.ProtocolVersion,
		MessageID:       control.NewMessageID(),
		LeaderID:        leaderID,
		Address:         address,
		ControlPort:     controlPort,
		Timestamp:       now.UTC().Format(time.RFC3339),
	}
	sig, err := control.Signature(secret, m)
	if err != nil {
		return nil, err
	}
	m.Sig = sig
	return json.Marshal(m)
}

// ParseDiscoverLeader checks the probe's shape and signature.
func ParseDiscoverLeader(secret []byte, data []byte) (DiscoverLeader, error) {
	var m DiscoverLeader
	if len(data) > MaxDatagramBytes {
		return m, errors.New("datagram too large")
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("malformed probe: %w", err)
	}
	if m.Type != TypeDiscoverLeader {
		return m, fmt.Errorf("type %q is not %s", m.Type, TypeDiscoverLeader)
	}
	if m.ProtocolVersion != control.ProtocolVersion {
		return m, fmt.Errorf("protocol_version %d", m.ProtocolVersion)
	}
	if !control.VerifySignature(secret, m, m.Sig) {
		return m, errors.New("bad signature")
	}
	return m, nil
}

// ParseLeaderFound checks a reply's shape and signature. A reply that fails
// the check must be ignored, so a rogue responder cannot take registrations.
func ParseLeaderFound(secret []byte, data []byte) (LeaderFound, error) {
	var m LeaderFound
	if len(data) > MaxDatagramBytes {
		return m, errors.New("datagram too large")
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("malformed reply: %w", err)
	}
	if m.Type != TypeLeaderFound {
		return m, fmt.Errorf("type %q is not %s", m.Type, TypeLeaderFound)
	}
	if m.ProtocolVersion != control.ProtocolVersion {
		return m, fmt.Errorf("protocol_version %d", m.ProtocolVersion)
	}
	if m.ControlPort < 1 || m.ControlPort > 65535 {
		return m, fmt.Errorf("control_port %d is out of range", m.ControlPort)
	}
	if !control.VerifySignature(secret, m, m.Sig) {
		return m, errors.New("bad signature")
	}
	return m, nil
}
