package version

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Current semantic version of the database node.
const (
	CurrentVersion = "v0.3.0"
	AppName        = "stiqrcloud"
	UpdateFeedURL  = "https://raw.githubusercontent.com/stiqrhub/stiqrcloud/main/releases/version.json"
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
		Version:     "v0.3.0",
		ReleaseDate: time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC),
		Title:       "Personal Swarm Mirroring, Airgap Cold Backups & Public WAN Gateway",
		Changelog: []string{
			"Personal Swarm multi-node automatic mirroring across trusted Reticulum and LAN peers",
			"Relaxed 0-fee zero-knowledge audits for owner-paired personal swarm nodes",
			"Airgap Cold Backup workflow with instant synchronization and safe-to-disconnect status",
			"Configurable storage partition controls (Personal Quota vs Public Mesh Quota) and Satoshis pricing",
			"Public WAN dashboard access toggle (-allow-public-dashboard) with Cloudflare Tunnel compatibility",
			"Zero-dependency pure JS Reticulum node QR code generator and interactive dashboard UI",
			"PocketBase API compatibility layer and resilient chunk-based note attachment sync",
		},
		DownloadURL: "https://github.com/StiQRman/stiqrcloud/releases/tag/v0.3.0",
		ChecksumSHA: "8918ed2ba94eff2c72134d7b6665907f51775717e32969f985bdc6afa646c2f5",
		IsMandatory: false,
	},
	{
		Version:     "v0.2.0",
		ReleaseDate: time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC),
		Title:       "Post-Quantum 2-of-3 Threshold & PocketBase Collections UI",
		Changelog: []string{
			"Post-Quantum ML-KEM-768 (FIPS 203) key encapsulation and ML-DSA-65 (FIPS 204) signatures",
			"Shamir's Secret Sharing (2-of-3 threshold) over GF(2^8) with information-theoretic security",
			"FastCDC content-defined chunking (Gear rolling hash) preventing cascading boundary shifts",
			"Signed Merkle chunk manifests with incremental partial-re-encryption on file edits",
			"Strict node-level write authorization verifying owner & authorized editor ACL signatures",
			"PocketBase-style dashboard UI with Collections sidebar, dynamic schema fields, and lockbox visualizer",
		},
		DownloadURL: "https://github.com/stiqrhub/stiqrcloud/releases/tag/v0.2.0",
		ChecksumSHA: "007e8425d8b0c27dd19e4f6665a018bb6885c690e3340eaefe80456ff4927262",
		IsMandatory: false,
	},
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
		DownloadURL: "https://github.com/stiqrhub/stiqrcloud/releases/tag/v0.1.0",
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
