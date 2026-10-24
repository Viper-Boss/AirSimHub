//go:build linux

package systemstats

import "testing"

func TestRadioMetricsUsesOnlyReportedServingCell(t *testing.T) {
	m := parseRadioMetrics("band=3\nearfcn=1650\npci=207\ntac=58916\ncell_id=204459039\nbandwidth=20\n")
	if m["band"] != "B3" || m["frequency_mhz"] != float64(1850) || m["cell_id_hex"] != "C2FCC1F" || m["tac_hex"] != "E624" {
		t.Fatalf("incorrect serving cell: %#v", m)
	}
	if _, ok := m["downlink_bandwidth_mhz"]; ok {
		t.Fatal("invented bandwidth")
	}
	invalid := parseRadioMetrics("pci=999\nband=invalid\nearfcn=bad\n")
	if len(invalid) != 0 {
		t.Fatalf("accepted invalid radio fields: %#v", invalid)
	}
}

func TestRadioMetricsReportsBandwidthOnlyWhenTheModemDid(t *testing.T) {
	m := parseRadioMetrics("band=3\ndl_bw_khz=20000\n")
	if m["downlink_bandwidth_mhz"] != float64(20) {
		t.Fatalf("reported bandwidth was dropped: %#v", m)
	}
	// 1.4 MHz arrives as 1400 kHz; it is a legal LTE width and must survive.
	if m := parseRadioMetrics("band=1\ndl_bw_khz=1400\n"); m["downlink_bandwidth_mhz"] != float64(1.4) {
		t.Fatalf("fractional width was lost: %#v", m)
	}
	// Out-of-range and garbage values are refused, never clamped or guessed.
	for _, bad := range []string{"band=3\ndl_bw_khz=0\n", "band=3\ndl_bw_khz=200000\n", "band=3\ndl_bw_khz=abc\n"} {
		if m := parseRadioMetrics(bad); m["downlink_bandwidth_mhz"] != nil {
			t.Fatalf("accepted an unusable bandwidth: %#v", m)
		}
	}
	// A bandwidth alone, with no other cell field, is not a serving cell.
	if m := parseRadioMetrics("dl_bw_khz=20000\n"); m["downlink_bandwidth_mhz"] != nil {
		t.Fatalf("bandwidth without a cell invented one: %#v", m)
	}
}
