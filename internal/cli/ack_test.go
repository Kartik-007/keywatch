package cli

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAckSetsVariable(t *testing.T) {
	g := newGHRecorder(t)
	stubPrompts(t, "", true)
	now := time.Date(2026, 10, 21, 14, 20, 30, 0, time.UTC)
	ackNow = func() time.Time { return now }
	t.Cleanup(func() { ackNow = time.Now })

	out, err := execWatchCmd(t, "ack", "a91f", "3d")
	if err != nil {
		t.Fatal(err, out)
	}
	if got := g.vars["KEYWATCH_ACK_A91F"]; got != "2026-10-24T14:20:30Z" {
		t.Errorf("vars = %v", g.vars)
	}
	if !strings.Contains(out, "gh variable delete KEYWATCH_ACK_A91F") {
		t.Errorf("out:\n%s", out)
	}
}

func TestAckOrgAndDeclined(t *testing.T) {
	g := newGHRecorder(t)
	stubPrompts(t, "")
	out, err := execWatchCmd(t, "ack", "org", "6h")
	if err != nil || len(g.vars) > 0 || !strings.Contains(out, "daily-total alert") {
		t.Errorf("err=%v vars=%v out:\n%s", err, g.vars, out)
	}
}

func TestAckErrors(t *testing.T) {
	g := newGHRecorder(t)
	stubPrompts(t, "", true, true, true, true)
	for _, args := range [][]string{
		{"ack", "a91f", "soon"},
		{"ack", "a91f", "-1h"},
		{"ack", "a91f", "100d"},
		{"ack", "a b", "6h"},
	} {
		if _, err := execWatchCmd(t, args...); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
	if len(g.vars) > 0 {
		t.Errorf("vars set on bad input: %v", g.vars)
	}
	g.err = errors.New("gh: not logged in")
	if _, err := execWatchCmd(t, "ack", "a91f", "6h"); err == nil {
		t.Error("want gh error passed on")
	}
}

func TestParseAckDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30m": 30 * time.Minute, "6h": 6 * time.Hour, "1h30m": 90 * time.Minute,
		"3d": 72 * time.Hour, "1w": 7 * 24 * time.Hour, "90d": 90 * 24 * time.Hour,
	} {
		if got, err := parseAckDuration(in); err != nil || got != want {
			t.Errorf("%s: got %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "d", "0h", "xd", "13w"} {
		if _, err := parseAckDuration(in); err == nil {
			t.Errorf("%q: want error", in)
		}
	}
}
