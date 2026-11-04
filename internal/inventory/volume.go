package inventory

import "context"

// Volume is an OS mount/device inventory item, not a published filesystem root.
// Seeing a disk does not grant any right to read or modify its contents.
type Volume struct {
	ID             string `json:"volume_id"`
	Kind           string `json:"kind"` // block or filesystem/volume
	Device         string `json:"device,omitempty"`
	Model          string `json:"model,omitempty"`
	MountPath      string `json:"mount_path"`
	FSType         string `json:"fs_type"`
	TotalBytes     uint64 `json:"size_bytes"`
	FreeBytes      uint64 `json:"free_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	ReadOnly       bool   `json:"read_only"`
	Removable      bool   `json:"removable"`
	Mounted        bool   `json:"mounted"`
}
type Inspector interface {
	List(context.Context) ([]Volume, error)
}
