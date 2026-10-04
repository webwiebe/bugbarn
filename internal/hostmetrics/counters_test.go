package hostmetrics

import (
	"testing"
	"time"
)

func netTags(dev string) map[string]string {
	return map[string]string{"collector": "network", "device": dev}
}

func TestNetworkRates(t *testing.T) {
	tests := []struct {
		dev  string
		keep bool
	}{
		{"eth0", true}, {"en0", true}, {"wlan0", true},
		{"lo", false}, {"lo0", false}, {"veth1234", false}, {"cni0", false}, {"flannel.1", false},
		{"docker0", false}, {"br-abc", false}, {"utun3", false}, {"awdl0", false}, {"llw0", false},
		{"anpi0", false}, {"bridge0", false}, {"gif0", false}, {"stf0", false}, {"ap1", false},
		{"tailscale0", false}, {"vxlan.calico", false}, {"kube-ipvs0", false}, {"cali123", false}, {"tunl0", false},
	}
	for _, tc := range tests {
		t.Run(tc.dev, func(t *testing.T) {
			clock := t0
			n := newTestNormalizer(&clock)
			first, _, _ := n.Parse(body(
				counter("network_receive_bytes_total", t0, 1000, netTags(tc.dev)),
				counter("network_transmit_bytes_total", t0, 500, netTags(tc.dev)),
			))
			if len(first) != 0 {
				t.Fatalf("first scrape emitted %+v", first)
			}
			t1 := t0.Add(10 * time.Second)
			samples, _, _ := n.Parse(body(
				counter("network_receive_bytes_total", t1, 3000, netTags(tc.dev)),
				counter("network_transmit_bytes_total", t1, 1500, netTags(tc.dev)),
			))
			if !tc.keep {
				if len(samples) != 0 {
					t.Fatalf("filtered device emitted %+v", samples)
				}
				return
			}
			wantValue(t, samples, "net."+tc.dev+".rx_bytes_per_s", 200)
			wantValue(t, samples, "net."+tc.dev+".tx_bytes_per_s", 100)
		})
	}
}

func TestRateSkipsResetAndStaleTimestamps(t *testing.T) {
	clock := t0
	n := newTestNormalizer(&clock)
	tags := netTags("eth0")
	t1, t2 := t0.Add(10*time.Second), t0.Add(20*time.Second)
	samples, _, _ := n.Parse(body(
		counter("network_receive_bytes_total", t0, 1000, tags),
		counter("network_receive_bytes_total", t1, 10, tags),  // reset
		counter("network_receive_bytes_total", t1, 999, tags), // same timestamp
		counter("network_receive_bytes_total", t2, 110, tags),
	))
	if len(samples) != 1 {
		t.Fatalf("samples = %+v", samples)
	}
	wantValue(t, samples, "net.eth0.rx_bytes_per_s", 10)
}

func TestDiskRates(t *testing.T) {
	tests := []struct {
		dev  string
		keep bool
	}{
		{"vda", true}, {"nvme0n1", true}, {"disk0", true},
		{"loop0", false}, {"ram0", false}, {"zram0", false}, {"sr0", false}, {"nbd0", false},
	}
	for _, tc := range tests {
		t.Run(tc.dev, func(t *testing.T) {
			clock := t0
			n := newTestNormalizer(&clock)
			tags := func() map[string]string { return map[string]string{"collector": "disk", "device": tc.dev} }
			t1 := t0.Add(4 * time.Second)
			samples, _, _ := n.Parse(body(
				counter("disk_read_bytes_total", t0, 0, tags()),
				counter("disk_written_bytes_total", t0, 100, tags()),
				counter("disk_read_bytes_total", t1, 400, tags()),
				counter("disk_written_bytes_total", t1, 900, tags()),
			))
			if !tc.keep {
				if len(samples) != 0 {
					t.Fatalf("filtered device emitted %+v", samples)
				}
				return
			}
			wantValue(t, samples, "disk."+tc.dev+".read_bytes_per_s", 100)
			wantValue(t, samples, "disk."+tc.dev+".write_bytes_per_s", 200)
		})
	}
}
