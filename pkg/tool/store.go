package tool

import "time"

// Package describes one downloadable tool build. Definition is authoritative
// after installation; the remaining fields describe distribution and state.
type Package struct {
	Definition       Definition `json:"definition"`
	Platform         string     `json:"platform"`
	ProtocolVersion  int        `json:"protocolVersion"`
	Author           string     `json:"author,omitempty"`
	Homepage         string     `json:"homepage,omitempty"`
	License          string     `json:"license,omitempty"`
	DownloadURL      string     `json:"downloadUrl"`
	SHA256           string     `json:"sha256"`
	Size             int64      `json:"size,omitempty"`
	Compatible       bool       `json:"compatible"`
	Installed        bool       `json:"installed"`
	InstalledVersion string     `json:"installedVersion,omitempty"`
	UpdateAvailable  bool       `json:"updateAvailable"`
}

// PackageCatalog is the JSON document published by a tool repository.
type PackageCatalog struct {
	SchemaVersion int       `json:"schemaVersion"`
	GeneratedAt   time.Time `json:"generatedAt"`
	Tools         []Package `json:"tools"`
}

// StoreSnapshot is the host's merged view of one or more tool repositories
// and the tools installed on this computer.
type StoreSnapshot struct {
	Packages  []Package `json:"packages"`
	Sources   []string  `json:"sources"`
	Warnings  []string  `json:"warnings,omitempty"`
	FetchedAt time.Time `json:"fetchedAt"`
}
