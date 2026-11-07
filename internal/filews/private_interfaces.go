package filews

import (
    "net"
    "net/netip"
    "sort"
)

// PrivateListenerIPv4 returns usable, currently assigned LAN and overlay
// addresses. Listening per address avoids binding a public/WAN interface.
// A loopback-only fallback keeps local development functional offline.
func PrivateListenerIPv4() ([]string, error) {
    interfaces, err := net.Interfaces()
    if err != nil { return nil, err }
    addresses := make(map[string]struct{})
    for _, iface := range interfaces {
        if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 { continue }
        assigned, err := iface.Addrs()
        if err != nil { continue }
        for _, a := range assigned {
            prefix, err := netip.ParsePrefix(a.String())
            if err != nil { continue }
            ip := prefix.Addr().Unmap()
            if isPrivateFileIP(ip) { addresses[ip.String()] = struct{}{} }
        }
    }
    if len(addresses) == 0 { return []string{"127.0.0.1"}, nil }
    result := make([]string, 0, len(addresses))
    for address := range addresses { result = append(result, address) }
    // Prefer ordinary LAN first. Keep tailnet as a separately published
    // fallback for a client outside the home LAN.
    sort.Slice(result, func(i, j int) bool {
        a, _ := netip.ParseAddr(result[i]); b, _ := netip.ParseAddr(result[j])
        ac := isTailnetFileIP(a); bc := isTailnetFileIP(b)
        if ac != bc { return !ac }
        return a.Less(b)
    })
    return result, nil
}

func isTailnetFileIP(ip netip.Addr) bool {
    return netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}
func isPrivateFileIP(ip netip.Addr) bool {
    return ip.Is4() && !ip.IsLoopback() && !ip.IsUnspecified() &&
      (ip.IsPrivate() || isTailnetFileIP(ip))
}
