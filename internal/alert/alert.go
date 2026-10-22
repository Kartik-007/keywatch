// Package alert turns an anomaly into plain text a person can act on, and
// posts it to a Slack or Discord incoming webhook. Those two hosts are the
// only places it ever sends anything.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Kartik-007/keywatch/internal/anomaly"
	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/provider"
)

// Render returns the alert as plain text for CI logs, terminals and chat:
// what happened, which key and where it lives, normal vs now, and the two
// commands to run next. k is the key the alert is about (for a scary-day
// alert, the biggest spender today). A zero k is fine: the alert falls back
// to the key id.
func Render(a anomaly.Alert, k core.Key) string {
	var b strings.Builder
	id := a.KeyID
	if a.Reason == anomaly.ScaryDay {
		id = a.TopKeyID
	}
	if k.ID == "" {
		k.ID = id
	}
	name := k.Label
	if name == "" {
		name = k.ID
	}
	m := func(usd float64) string { return money(usd, a.Approx) }

	switch a.Reason {
	case anomaly.ScaryDay:
		fmt.Fprintf(&b, "keywatch alert: AI spend today is %s, over your scary-day amount of %s.\n\n",
			m(a.TodayUSD), money(a.LimitUSD, false))
		if id == "" {
			break
		}
		fmt.Fprintf(&b, "Biggest spender:  %s (%s today)\n", describe(k, name), m(a.TopKeyUSD))
		writeLocations(&b, k)
	default:
		fmt.Fprintf(&b, "keywatch alert: %s spent %s in the last hour. Normal is %s.\n\n",
			name, m(a.LastHourUSD), normal(a, m))
		fmt.Fprintf(&b, "Key:       %s\n", describe(k, name))
		writeLocations(&b, k)
		if a.NormalHourUSD == 0 {
			b.WriteString("Normal:    no spend in 95% of hours over the last 14 days\n")
		} else {
			fmt.Fprintf(&b, "Normal:    %s in 95%% of hours over the last 14 days\n", normal(a, m))
		}
		fmt.Fprintf(&b, "Now:       %s in the hour from %s UTC, %s so far today\n",
			m(a.LastHourUSD), a.HourStart.UTC().Format("15:04"), m(a.TodayUSD))
	}

	b.WriteString("\n")
	if a.Reason == anomaly.ScaryDay {
		b.WriteString("Expected? Silence the daily total for 6 hours:\n")
		fmt.Fprintf(&b, "  keywatch ack %s 6h\n", anomaly.Org)
		if id == "" {
			return b.String()
		}
		b.WriteString("Not expected? Turn off the biggest spender:\n")
	} else {
		b.WriteString("Expected? Silence this key for 6 hours:\n")
		fmt.Fprintf(&b, "  keywatch ack %s 6h\n", k.ID)
		b.WriteString("Not expected? Turn the key off:\n")
	}
	fmt.Fprintf(&b, "  keywatch disable %s\n", k.ID)
	fmt.Fprintf(&b, "  %s\n", breaks(k))
	fmt.Fprintf(&b, "  %s\n", undo(k))
	return b.String()
}

func describe(k core.Key, name string) string {
	parts := []string{name}
	if k.Provider != "" {
		parts = append(parts, k.Provider)
	}
	if k.Redacted != "" && k.Redacted != name {
		parts = append(parts, k.Redacted)
	}
	return strings.Join(parts, " · ")
}

func writeLocations(b *strings.Builder, k core.Key) {
	if len(k.Locations) == 0 {
		b.WriteString("Lives in:  nowhere keywatch can read from here\n")
		return
	}
	for i, l := range k.Locations {
		head := "Lives in:  "
		if i > 0 {
			head = "           "
		}
		fmt.Fprintf(b, "%s%s\n", head, location(l))
	}
}

func location(l core.Location) string {
	s := l.Source + ": " + l.Where
	var notes []string
	if l.Var != "" {
		notes = append(notes, l.Var)
	}
	if l.Committed {
		notes = append(notes, "committed to git")
	}
	if l.NameOnly {
		notes = append(notes, "matched by name")
	}
	if len(notes) > 0 {
		s += " (" + strings.Join(notes, ", ") + ")"
	}
	return s
}

func breaks(k core.Key) string {
	if len(k.Locations) == 0 {
		return "This breaks anything using the key. keywatch couldn't see where it's deployed."
	}
	var where []string
	for _, l := range k.Locations {
		where = append(where, l.Where+" ("+l.Source+")")
	}
	return "This breaks: " + strings.Join(where, ", ") + "."
}

func undo(k core.Key) string {
	reversible := k.Provider == core.Anthropic
	if p, ok := provider.Get(k.Provider); ok {
		reversible = p.Reversible()
	}
	if reversible {
		return "You can turn it back on with: keywatch enable " + k.ID
	}
	return "This deletes the key at the provider. It can't be undone."
}

func normal(a anomaly.Alert, m func(float64) string) string {
	if a.NormalHourUSD == 0 {
		return "no spend in most hours"
	}
	return "up to " + m(a.NormalHourUSD) + "/hour"
}

func money(usd float64, approx bool) string {
	s := fmt.Sprintf("$%.2f", usd)
	if approx {
		return "≈" + s
	}
	return s
}

// Kind is the chat service a webhook belongs to.
type Kind string

const (
	Slack   Kind = "slack"
	Discord Kind = "discord"
)

var hosts = map[string]Kind{
	"hooks.slack.com": Slack,
	"discord.com":     Discord,
	"discordapp.com":  Discord,
}

// KindOf checks that a webhook URL is an https Slack or Discord incoming
// webhook and says which. Any other host is refused.
func KindOf(webhook string) (Kind, error) {
	u, err := url.Parse(webhook)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return "", errors.New("webhook must be an https URL from Slack or Discord")
	}
	k, ok := hosts[strings.ToLower(u.Hostname())]
	switch {
	case !ok:
		return "", fmt.Errorf("webhook host %q isn't Slack (hooks.slack.com) or Discord (discord.com)", u.Hostname())
	case k == Slack && !strings.HasPrefix(u.Path, "/services/") && !strings.HasPrefix(u.Path, "/triggers/"):
		return "", errors.New("that doesn't look like a Slack incoming webhook (expected hooks.slack.com/services/…)")
	case k == Discord && !strings.HasPrefix(u.Path, "/api/webhooks/"):
		return "", errors.New("that doesn't look like a Discord webhook (expected discord.com/api/webhooks/…)")
	}
	return k, nil
}

// Client sends webhook posts. Tests swap its Transport. Redirects are never
// followed, so a post can't end up on another host.
var Client = &http.Client{
	Timeout:       15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Logf, when set, logs each network call (method, host, and the first path
// segment only: the rest of a webhook path is its secret).
var Logf func(format string, args ...any)

const discordMax = 2000

// Post sends text to a Slack or Discord incoming webhook in one HTTP POST.
func Post(ctx context.Context, webhook, text string) error {
	kind, err := KindOf(webhook)
	if err != nil {
		return err
	}
	var payload any
	switch kind {
	case Slack:
		payload = map[string]string{"text": "```\n" + text + "\n```"}
	case Discord:
		body := "```\n" + text + "\n```"
		if r := []rune(body); len(r) > discordMax {
			body = string(r[:discordMax-5]) + "…\n```" // 5 runes
		}
		payload = map[string]any{"content": body, "allowed_mentions": map[string]any{"parse": []string{}}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if Logf != nil {
		first := strings.SplitN(strings.TrimPrefix(req.URL.Path, "/"), "/", 2)[0]
		Logf("POST %s/%s/…", req.URL.Host, first)
	}
	resp, err := Client.Do(req)
	if err != nil {
		// The error text includes the URL, which is a secret. Don't pass it on.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("%s webhook: %w", kind, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s webhook returned HTTP %d", kind, resp.StatusCode)
	}
	return nil
}
