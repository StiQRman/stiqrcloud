package version

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Current semantic version of the database node.
const (
	CurrentVersion = "v0.1.0"
	AppName        = "testcloud"
	UpdateFeedURL  = "https://raw.githubusercontent.com/stiqrhub/testcloud/main/releases/version.json"
)

// Release describes a published release version.
type Release struct {
	Version      string    `json:"version"`
	ReleaseDate  time.Time `json:"release_date"`
	Title        string    `json:"title"`
	Changelog    []string  `json:"changelog"`
	DownloadURL  string    `json:"download_url"`
	ChecksumSHA  string    `json:"checksum_sha"`
	IsMandatory  bool      `json:"is_mandatory"`
}

// UpdateStatus describes the comparison between current and latest release.
type UpdateStatus struct {
	CurrentVersion  string    `json:"current_version"`
	LatestVersion   string    `json:"latest_version"`
	UpdateAvailable bool      `json:"update_available"`
	LatestRelease   *Release  `json:"latest_release,omitempty"`
	History         []Release `json:"history"`
	CheckedAt       time.Time `json:"checked_at"`
}

// Built-in initial release history.
var DefaultHistory = []Release{
	{
		Version:     "v0.1.0",
		ReleaseDate: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Title:       "Initial Alpha Release",
		Changelog: []string{
			"Standalone-first local SQLite database with chunk storage",
			"Reticulum Network Stack (RNS) packet and destination compatibility",
			"Persistent invariant Data IDs across versions and node migrations",
			"Distributed 3-Auditor + 3-Storage quorum with Merkle audits",
			"Pluggable Bitcoin Lightning Network micropayments & Mock LN Sandbox",
			"Local dark-mode dashboard with live telemetry and Settings Update Card",
		},
		DownloadURL: "https://github.com/stiqrhub/testcloud/releases/tag/v0.1.0",
		ChecksumSHA: "99c9f52458448593b5efde40a7a7c3e7d81d4980890104f5f9362ff397a9a7f6",
		IsMandatory: false,
	},
}

// CheckUpdate queries the update status against the current version.
func CheckUpdate() UpdateStatus {
	status := UpdateStatus{
		CurrentVersion:  CurrentVersion,
		LatestVersion:   CurrentVersion,
		UpdateAvailable: false,
		History:         DefaultHistory,
		CheckedAt:       time.Now().UTC(),
	}

	if len(DefaultHistory) > 0 {
		latest := DefaultHistory[0]
		status.LatestVersion = latest.Version
		status.LatestRelease = &latest
		if latest.Version != CurrentVersion {
			status.UpdateAvailable = true
		}
	}

	// Try remote feed if accessible with a short timeout
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(UpdateFeedURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		var remoteReleases []Release
		if err := json.NewDecoder(resp.Body).Decode(&remoteReleases); err == nil && len(remoteReleases) > 0 {
			status.History = remoteReleases
			status.LatestVersion = remoteReleases[0].Version
			status.LatestRelease = &remoteReleases[0]
			if remoteReleases[0].Version != CurrentVersion {
				status.UpdateAvailable = true
			}
		}
		_ = resp.Body.Close()
	}

	return status
}

// GetVersionSummary returns a concise formatted string.
func GetVersionSummary() string {
	return fmt.Sprintf("%s %s (Database-First & Reticulum Compatible)", AppName, CurrentVersion)
}
