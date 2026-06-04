package stats

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPayloadWireShape(t *testing.T) {
	got := payload("generate", Success, 7)
	want := "tommy.generate.success:1|c|#op:generate,result:success\n" +
		"tommy.generate.duration:7|ms|#op:generate,result:success"
	if got != want {
		t.Fatalf("payload =\n%q\nwant\n%q", got, want)
	}
	if f := payload("generate", Failure, 3); !strings.HasPrefix(f, "tommy.generate.failure:1|c|#op:generate,result:failure") {
		t.Fatalf("failure payload = %q", f)
	}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"generate": "generate",
		"FMT":      "fmt",
		"a.b@c":    "a_b_c",
		"k 1":      "k_1",
	} {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOutcomeOfCode(t *testing.T) {
	if outcomeOfCode(0) != Success {
		t.Error("exit 0 should be Success")
	}
	if outcomeOfCode(1) != Failure || outcomeOfCode(-1) != Failure {
		t.Error("non-zero exit should be Failure")
	}
}

func TestEndpointGatedOnEnvPresence(t *testing.T) {
	savedHost, hostOK := os.LookupEnv("STATSD_HOST")
	savedPort, portOK := os.LookupEnv("STATSD_PORT")
	t.Cleanup(func() {
		restoreEnv("STATSD_HOST", savedHost, hostOK)
		restoreEnv("STATSD_PORT", savedPort, portOK)
	})

	os.Unsetenv("STATSD_HOST")
	os.Unsetenv("STATSD_PORT")
	if _, _, ok := endpoint(); ok {
		t.Fatal("no env -> endpoint should be absent (opt-in gate)")
	}

	os.Setenv("STATSD_PORT", "9999")
	if h, p, ok := endpoint(); !ok || h != "127.0.0.1" || p != 9999 {
		t.Fatalf("port-only: got (%q, %d, %v), want (127.0.0.1, 9999, true)", h, p, ok)
	}

	os.Setenv("STATSD_HOST", "")
	if h, _, ok := endpoint(); !ok || h != "127.0.0.1" {
		t.Fatalf("empty host should fall back to loopback: got (%q, %v)", h, ok)
	}

	os.Setenv("STATSD_HOST", "10.0.0.5")
	os.Unsetenv("STATSD_PORT")
	if h, p, ok := endpoint(); !ok || h != "10.0.0.5" || p != 8125 {
		t.Fatalf("host-only: got (%q, %d, %v), want (10.0.0.5, 8125, true)", h, p, ok)
	}
}

func TestTimedReturnsCodeWithoutEnv(t *testing.T) {
	savedHost, hostOK := os.LookupEnv("STATSD_HOST")
	savedPort, portOK := os.LookupEnv("STATSD_PORT")
	os.Unsetenv("STATSD_HOST")
	os.Unsetenv("STATSD_PORT")
	t.Cleanup(func() {
		restoreEnv("STATSD_HOST", savedHost, hostOK)
		restoreEnv("STATSD_PORT", savedPort, portOK)
	})
	if got := Timed("noop", func() int { return 0 }); got != 0 {
		t.Errorf("Timed(0) = %d, want 0", got)
	}
	if got := Timed("noop", func() int { return 42 }); got != 42 {
		t.Errorf("Timed(42) = %d, want 42", got)
	}
}

// TestRecordSendsOverUDP exercises the full send path against a real loopback
// listener, asserting the bytes on the wire match payload().
func TestRecordSendsOverUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer pc.Close()
	_, port, _ := net.SplitHostPort(pc.LocalAddr().String())
	t.Setenv("STATSD_HOST", "127.0.0.1")
	t.Setenv("STATSD_PORT", port)

	record("generate", Success, 5*time.Millisecond)

	if err := pc.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got, want := string(buf[:n]), payload("generate", Success, 5); got != want {
		t.Fatalf("received %q, want %q", got, want)
	}
}

func restoreEnv(key, val string, ok bool) {
	if ok {
		os.Setenv(key, val)
	} else {
		os.Unsetenv(key)
	}
}
