package health

import (
	"testing"
	"time"
)

func TestExpandChecks_Speedtest(t *testing.T) {
	expanded := expandChecks([]string{"speedtest"})
	want := map[string]bool{"download": true, "upload": true, "ping": true}
	for _, c := range expanded {
		delete(want, c)
	}
	if len(want) > 0 {
		t.Errorf("speedtest missing checks: %v", want)
	}
}

func TestExpandChecks_Passthrough(t *testing.T) {
	in := []string{"dns", "http", "quic"}
	out := expandChecks(in)
	if len(out) != 3 {
		t.Errorf("len = %d, want 3", len(out))
	}
}

func TestFormatThroughput(t *testing.T) {
	cases := []struct {
		bps  float64
		want string
	}{
		{2 << 20, "2.00MB/s"},
		{512 * 1024, "512.00KB/s"},
		{100, "100B/s"},
	}
	for _, c := range cases {
		got := FormatThroughput(c.bps)
		if got != c.want {
			t.Errorf("FormatThroughput(%.0f) = %q, want %q", c.bps, got, c.want)
		}
	}
}

func TestFormatDuration_MillisecondRound(t *testing.T) {
	s := FormatDuration(38 * time.Millisecond)
	if s != "38ms" {
		t.Errorf("got %q, want 38ms", s)
	}
}

func TestFormatDuration_MicrosecondRound(t *testing.T) {
	s := FormatDuration(500 * time.Microsecond)
	if s != "500µs" {
		t.Errorf("got %q, want 500µs", s)
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}
	cfg.defaults()
	if cfg.Timeout == 0 {
		t.Error("Timeout should not be 0 after defaults()")
	}
	if cfg.DNSServer == "" {
		t.Error("DNSServer should not be empty after defaults()")
	}
	if cfg.PingCount == 0 {
		t.Error("PingCount should not be 0 after defaults()")
	}
}

func TestSummarizePing_Empty(t *testing.T) {
	avg, mn, mx, jitter := summarizePing(nil)
	if avg != 0 || mn != 0 || mx != 0 || jitter != 0 {
		t.Errorf("empty summarizePing should return all zeros")
	}
}

func TestSummarizePing_Single(t *testing.T) {
	samples := []time.Duration{50 * time.Millisecond}
	avg, mn, mx, jitter := summarizePing(samples)
	if avg != 50*time.Millisecond {
		t.Errorf("avg = %v, want 50ms", avg)
	}
	if mn != mx {
		t.Error("single sample: min should equal max")
	}
	if jitter != 0 {
		t.Error("single sample: jitter should be 0")
	}
}

func TestSummarizePing_Multiple(t *testing.T) {
	samples := []time.Duration{10, 20, 30, 40, 50}
	avg, mn, mx, _ := summarizePing(samples)
	if mn != 10 {
		t.Errorf("min = %v, want 10", mn)
	}
	if mx != 50 {
		t.Errorf("max = %v, want 50", mx)
	}
	if avg != 30 {
		t.Errorf("avg = %v, want 30", avg)
	}
}

func TestBuildDNSQuery(t *testing.T) {
	q := buildDNSQuery("google.com")
	if len(q) < 12 {
		t.Errorf("DNS query too short: %d bytes", len(q))
	}
	// Transaction ID = 0xabcd
	if q[0] != 0xab || q[1] != 0xcd {
		t.Errorf("transaction ID = %02x%02x, want abcd", q[0], q[1])
	}
	// Flags = 0x0100 (RD=1): q[2]=0x01, q[3]=0x00
	if q[2] != 0x01 || q[3] != 0x00 {
		t.Errorf("flags = %02x%02x, want 0100", q[2], q[3])
	}
}
