package filews

import (
 "net/netip"
 "testing"
)

func TestPrivateFileListenerClassification(t *testing.T) {
 for _, raw := range []string{"192.168.0.147","10.0.0.8","172.16.1.8","100.114.58.74"} {
  if !isPrivateFileIP(netip.MustParseAddr(raw)) { t.Fatalf("rejected private address %s",raw) }
 }
 for _, raw := range []string{"127.0.0.1","0.0.0.0","8.8.8.8","169.254.1.4","::1"} {
  if isPrivateFileIP(netip.MustParseAddr(raw)) { t.Fatalf("accepted non-LAN interface %s",raw) }
 }
}
