//go:build windows

package inventory

import (
	"context"
	"fmt"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var getLogicalDrives = kernel32.NewProc("GetLogicalDrives")
var getDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")

type OSInspector struct{}

func (OSInspector) List(ctx context.Context) ([]Volume, error) {
	mask, _, e := getLogicalDrives.Call()
	if mask == 0 {
		return nil, fmt.Errorf("GetLogicalDrives: %w", e)
	}
	out := make([]Volume, 0, 10)
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := string(rune('A'+i)) + ":\\"
		wide, err := syscall.UTF16PtrFromString(path)
		if err != nil {
			continue
		}
		var available, total, free uint64
		ok, _, _ := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(wide)), uintptr(unsafe.Pointer(&available)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
		if ok == 0 {
			continue
		}
		out = append(out, Volume{ID: path, Kind: "volume", MountPath: path, FSType: "windows-volume", TotalBytes: total, FreeBytes: free, AvailableBytes: available, Mounted: true})
	}
	return out, nil
}
