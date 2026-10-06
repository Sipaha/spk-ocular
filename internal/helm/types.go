// Package helm implements UI-only Helm workflows using the embedded SDK.
package helm

import (
	"github.com/spk/spk-ocular/internal/core"
	"time"
)

type Storage struct {
	Driver        string `json:"driver"`
	SQLConnection string `json:"sqlConnection,omitempty"`
}

type Repository struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	CAFile      string `json:"caFile,omitempty"`
	CertFile    string `json:"certFile,omitempty"`
	KeyFile     string `json:"keyFile,omitempty"`
	Insecure    bool   `json:"insecure,omitempty"`
	HasPassword bool   `json:"hasPassword,omitempty"`
}

type ChartRef struct {
	Repository string `json:"repository"`
	Name       string `json:"name"`
	Version    string `json:"version"`
}

type Chart struct {
	ChartRef
	Description string `json:"description"`
	AppVersion  string `json:"appVersion"`
	Deprecated  bool   `json:"deprecated"`
	Values      string `json:"values,omitempty"`
	Readme      string `json:"readme,omitempty"`
}

type Release struct {
	Name         string    `json:"name"`
	Namespace    string    `json:"namespace"`
	Revision     int       `json:"revision"`
	Chart        string    `json:"chart"`
	ChartVersion string    `json:"chartVersion"`
	AppVersion   string    `json:"appVersion"`
	Status       string    `json:"status"`
	Updated      time.Time `json:"updated"`
}

type Resource struct {
	Ref        *core.Ref `json:"ref,omitempty"`
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Namespace  string    `json:"namespace"`
	Name       string    `json:"name"`
}

type Detail struct {
	Resources []Resource `json:"resources"`
	Release
	Values         string    `json:"values"`
	ComputedValues string    `json:"computedValues"`
	Manifest       string    `json:"manifest"`
	Notes          string    `json:"notes"`
	Hooks          string    `json:"hooks"`
	History        []Release `json:"history"`
}

type Operation struct {
	Action          string   `json:"action"`
	Namespace       string   `json:"namespace"`
	Name            string   `json:"name"`
	Chart           ChartRef `json:"chart"`
	Values          string   `json:"values"`
	Revision        int      `json:"revision"`
	TimeoutSeconds  int      `json:"timeoutSeconds"`
	Wait            bool     `json:"wait"`
	DisableHooks    bool     `json:"disableHooks"`
	CreateNamespace bool     `json:"createNamespace"`
	KeepHistory     bool     `json:"keepHistory"`
}

type Plan struct {
	PreviewMode      string    `json:"previewMode"`
	ID               string    `json:"id"`
	Operation        Operation `json:"operation"`
	CurrentRevision  int       `json:"currentRevision"`
	Manifest         string    `json:"manifest"`
	PreviousManifest string    `json:"previousManifest"`
	Notes            string    `json:"notes"`
	Warnings         []string  `json:"warnings"`
	Expires          time.Time `json:"expires"`
}

type Result struct {
	Outcome string   `json:"outcome"`
	Message string   `json:"message"`
	Release *Release `json:"release,omitempty"`
}
