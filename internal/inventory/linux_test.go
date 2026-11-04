//go:build linux

package inventory

import (
	"strings"
	"testing"
)

func TestMountInfoParseAndExcludePseudo(t *testing.T) {
	s := `40 23 8:1 / / rw,relatime - ext4 /dev/sda1 rw
41 40 0:36 / /proc rw,nosuid - proc proc rw
42 40 8:2 / /mnt/data\040drive rw,relatime - ext4 /dev/sdb1 rw
43 40 8:3 / /mnt/readonly ro,relatime - vfat /dev/sdc1 ro
`
	a := parseMountInfo(s)
	if len(a) != 3 || a[1].Path != "/mnt/data drive" || a[2].ReadOnly != true {
		t.Fatalf("%+v", a)
	}
	if strings.Contains(a[1].ID, "/dev/sdb1") {
		t.Fatal("not stable mount id")
	}
}
