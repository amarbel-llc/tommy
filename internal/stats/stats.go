// Package stats emits best-effort stats-me / statsd telemetry for the tommy
// CLI.
//
// stats-me is upstream statsd packaged under Bun; clients publish by sending UDP
// datagrams in the statsd wire format to the daemon (see stats-me-clients(7) in
// the amarbel-llc/stats-me repo). There is no library API and no auth — anything
// that can write UDP can publish.
//
// Emission is gated on the *presence* of the STATSD_HOST / STATSD_PORT
// environment variables that the stats-me home-manager module exports via
// home.sessionVariables. When neither is set every call is a no-op, so tommy
// never sprays UDP at a host that has not opted in. When at least one is present
// we follow the documented resolution order: STATSD_HOST (present-but-empty
// treated as loopback 127.0.0.1) and STATSD_PORT (default 8125).
//
// UDP is fire-and-forget: any failure to resolve, dial, or send is swallowed.
// Telemetry must never perturb code generation. The mirror of piggy's
// crates/piggy/src/stats.rs (the reference implementation), adapted to Go and to
// the tommy.<op> namespace.
package stats

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHost = "127.0.0.1"
	defaultPort = 8125
)

// Outcome is the result of an operation, encoded into the metric name and a tag.
type Outcome int

const (
	Success Outcome = iota
	Failure
)

func (o Outcome) String() string {
	if o == Failure {
		return "failure"
	}
	return "success"
}

// outcomeOfCode maps a process exit code to an Outcome (0 = success), matching
// the exit code the timed CLI handlers return.
func outcomeOfCode(code int) Outcome {
	if code == 0 {
		return Success
	}
	return Failure
}

// endpoint resolves the stats-me endpoint from the environment, returning
// ok=false when neither STATSD_HOST nor STATSD_PORT is present (the opt-in
// gate). A present-but-empty STATSD_HOST falls back to loopback.
func endpoint() (host string, port int, ok bool) {
	hostVar, hostSet := os.LookupEnv("STATSD_HOST")
	portVar, portSet := os.LookupEnv("STATSD_PORT")
	if !hostSet && !portSet {
		return "", 0, false
	}
	host = hostVar
	if host == "" {
		host = defaultHost
	}
	port = defaultPort
	if p, err := strconv.Atoi(portVar); err == nil {
		port = p
	}
	return host, port, true
}

// send fires a statsd payload (one or more newline-separated lines) at the
// endpoint, fire-and-forget. Every error is swallowed.
func send(payload string) {
	host, port, ok := endpoint()
	if !ok {
		return
	}
	conn, err := net.Dial("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Write([]byte(payload))
}

// sanitize maps anything outside [A-Za-z0-9] to '_' and lowercases alphabetics,
// since statsd treats '.' as the hierarchy separator.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// payload builds the two-line statsd payload (counter + duration timer) for a
// completed op under tommy.<op>. Pure — the wire shape is asserted in tests; op
// is assumed already sanitized. Both lines carry DogStatsD-style op/result tags.
func payload(op string, outcome Outcome, ms int64) string {
	result := outcome.String()
	return fmt.Sprintf(
		"tommy.%s.%s:1|c|#op:%s,result:%s\ntommy.%s.duration:%d|ms|#op:%s,result:%s",
		op, result, op, result,
		op, ms, op, result,
	)
}

// record emits a counter keyed by op + outcome plus a duration timer under the
// tommy.<op> namespace.
func record(op string, outcome Outcome, elapsed time.Duration) {
	send(payload(sanitize(op), outcome, elapsed.Milliseconds()))
}

// Timed runs f (a CLI subcommand handler returning its process exit code),
// emits a tommy.<op> counter (keyed by success/failure of the exit code) plus a
// duration timer, and returns the code so the caller can os.Exit it. The
// closure always runs; only the emit is gated on STATSD_*.
func Timed(op string, f func() int) int {
	start := time.Now()
	code := f()
	record(op, outcomeOfCode(code), time.Since(start))
	return code
}
