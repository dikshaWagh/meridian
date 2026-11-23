package config

import (
	"errors"
	"net"
)

// DetectAdvertiseIP returns the first up, non-loopback IPv4 address on this
// machine. It is used when --advertise-ip is not set.
func DetectAdvertiseIP() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if v4 := ipNet.IP.To4(); v4 != nil && !v4.IsLoopback() {
				return v4.String(), nil
			}
		}
	}
	return "", errors.New("no non-loopback IPv4 address found; pass --advertise-ip")
}
