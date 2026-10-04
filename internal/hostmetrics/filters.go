package hostmetrics

import "strings"

var skipFSTypes = map[string]bool{
	"tmpfs": true, "overlay": true, "squashfs": true, "devtmpfs": true,
	"proc": true, "sysfs": true, "nsfs": true, "cgroup": true, "cgroup2": true,
	"autofs": true, "devfs": true, "nullfs": true,
}

var skipMountPrefixes = []string{
	"/proc", "/sys", "/dev", "/run", "/snap",
	"/var/lib/kubelet", "/var/lib/docker", "/var/lib/rancher/k3s/agent/containerd",
	"/System/Volumes", "/private/var/vm",
}

// macOS puts user data on this APFS volume, so it is the one worth graphing.
const macDataVolume = "/System/Volumes/Data"

var skipNetExact = map[string]bool{"lo": true, "lo0": true}

var skipNetPrefixes = []string{
	"veth", "cni", "flannel", "docker", "br-", "utun", "awdl", "llw", "anpi",
	"bridge", "gif", "stf", "ap", "tailscale", "vxlan", "kube-ipvs", "cali", "tunl",
}

var skipDiskPrefixes = []string{"loop", "ram", "zram", "sr", "nbd"}

// normalizeMount strips the "/host" prefix the Vector container adds to host
// mounts and reports whether the mountpoint should be kept.
func normalizeMount(mp string) (string, bool) {
	switch {
	case mp == "/host":
		mp = "/"
	case strings.HasPrefix(mp, "/host/"):
		mp = mp[len("/host"):]
	}
	if mp == "" {
		return "", false
	}
	if mp == macDataVolume {
		return mp, true
	}
	for _, p := range skipMountPrefixes {
		if mp == p || strings.HasPrefix(mp, p+"/") {
			return "", false
		}
	}
	return mp, true
}

func skipNetDevice(dev string) bool {
	return skipNetExact[dev] || hasAnyPrefix(dev, skipNetPrefixes)
}

func skipDiskDevice(dev string) bool {
	return hasAnyPrefix(dev, skipDiskPrefixes)
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
