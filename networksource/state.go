// Package networksource defines the bounded, file-based host control protocol.
// The checker never executes host commands or accepts arbitrary command lines.
package networksource

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const StatusName = "status.json"
const RequestName = "request.json"
const ProcessingName = "processing.json"
const Freshness = 15 * time.Second

var InterfaceName = regexp.MustCompile(`^en[0-9]{1,3}$`)

type Interface struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Address string `json:"address,omitempty"`
}

type Profile struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Interface string `json:"interface"`
	Available bool   `json:"available"`
	Address   string `json:"address,omitempty"`
}

type Job struct {
	ID        string `json:"id,omitempty"`
	Action    string `json:"action,omitempty"`
	SourceID  string `json:"sourceId,omitempty"`
	State     string `json:"state"`
	Message   string `json:"message,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	Rollback  string `json:"rollback,omitempty"`
}

func (j Job) Active() bool { return j.State == "queued" || j.State == "running" }

type Snapshot struct {
	Version        int         `json:"version"`
	Enabled        bool        `json:"enabled"`
	ActiveSourceID string      `json:"activeSourceId"`
	Epoch          string      `json:"epoch"`
	UpdatedAt      int64       `json:"updatedAt"`
	Profiles       []Profile   `json:"profiles"`
	Interfaces     []Interface `json:"interfaces"`
	Job            Job         `json:"job"`
	Message        string      `json:"message,omitempty"`
}

type Request struct {
	ID            string `json:"id"`
	Action        string `json:"action"`
	SourceID      string `json:"sourceId"`
	Interface     string `json:"interface"`
	ExpectedEpoch string `json:"expectedEpoch"`
	RequestedAt   int64  `json:"requestedAt"`
}

func Read(dir string) (Snapshot, error) {
	var s Snapshot
	file, err := os.Open(filepath.Join(dir, StatusName))
	if err != nil {
		return s, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return s, fmt.Errorf("invalid network source snapshot size")
	}
	if err = json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	if s.Version != 1 || (s.ActiveSourceID != "iphone" && s.ActiveSourceID != "router") || s.Epoch == "" {
		return s, fmt.Errorf("invalid network source snapshot")
	}
	return s, nil
}

func (s Snapshot) Fresh(now time.Time) bool {
	age := now.Sub(time.Unix(s.UpdatedAt, 0))
	return s.Enabled && s.UpdatedAt > 0 && age >= -5*time.Second && age <= Freshness
}

func Exists(dir, name string) bool {
	_, err := os.Lstat(filepath.Join(dir, name))
	return !os.IsNotExist(err)
}
