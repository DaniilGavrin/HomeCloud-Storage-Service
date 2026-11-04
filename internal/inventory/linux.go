//go:build linux

package inventory

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// linux mountinfo includes pseudo mounts; we expose real storage-oriented
// mounts only. Snapshots are observational, not a grant to access content.
var pseudo = map[string]bool{"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "tmpfs": true, "cgroup": true, "cgroup2": true, "pstore": true, "securityfs": true, "debugfs": true, "tracefs": true, "fusectl": true, "configfs": true, "mqueue": true, "hugetlbfs": true, "nsfs": true, "autofs": true, "binfmt_misc": true, "rpc_pipefs": true, "bpf": true}

func decodeMount(s string) string {
	r := strings.NewReplacer(`\040`, ` `, `\011`, `\t`, `\012`, `\n`, `\134`, `\`)
	return r.Replace(s)
}

type mount struct {
	ID, Path, FS string
	ReadOnly     bool
}

func parseMountInfo(src string) []mount {
	out := []mount{}
	sc := bufio.NewScanner(strings.NewReader(src))
	sc.Buffer(make([]byte, 8192), 1<<20)
	seen := map[string]bool{}
	for sc.Scan() {
		line := sc.Text()
		parts := strings.SplitN(line, " - ", 2)
		if len(parts) != 2 {
			continue
		}
		left := strings.Fields(parts[0])
		right := strings.Fields(parts[1])
		if len(left) < 6 || len(right) < 3 {
			continue
		}
		fs := right[0]
		if pseudo[fs] || strings.HasPrefix(fs, "fuse.") && fs != "fuseblk" {
			continue
		}
		path := decodeMount(left[4])
		if seen[path] {
			continue
		}
		seen[path] = true
		ro := false
		for _, v := range strings.Split(left[5], ",") {
			if v == "ro" {
				ro = true
			}
		}
		out = append(out, mount{ID: left[2] + ":" + strconv.Quote(path), Path: path, FS: fs, ReadOnly: ro})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

type OSInspector struct{}

func (OSInspector) List(ctx context.Context) ([]Volume, error) {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	mounts := parseMountInfo(string(b))
	out := make([]Volume, 0, len(mounts))
	for _, m := range mounts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(m.Path, &st); err != nil {
			continue
		}
		size := uint64(st.Bsize)
		if st.Bsize <= 0 {
			return nil, fmt.Errorf("invalid block size for %s", m.Path)
		}
		out = append(out, Volume{ID: m.ID, Kind: "filesystem", MountPath: m.Path, FSType: m.FS, TotalBytes: st.Blocks * size, FreeBytes: st.Bfree * size, AvailableBytes: st.Bavail * size, ReadOnly: m.ReadOnly, Mounted: true})
	}
	// Discover physical block devices even when they have no mounted filesystem.
	// They are inventory-only: no content is published without a disk/folder root.
	block, err := os.ReadDir("/sys/block")
	if err == nil {
		for _, d := range block {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			name := d.Name()
			if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") {
				continue
			}
			raw, e := os.ReadFile(filepath.Join("/sys/block", name, "size"))
			if e != nil {
				continue
			}
			sectors, e := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
			if e != nil || sectors == 0 {
				continue
			}
			model, _ := os.ReadFile(filepath.Join("/sys/block", name, "device", "model"))
			out = append(out, Volume{ID: "block:/dev/" + name, Kind: "block", Device: "/dev/" + name, Model: strings.TrimSpace(string(model)), TotalBytes: sectors * 512, Mounted: false})
		}
	}
	return out, nil
}
