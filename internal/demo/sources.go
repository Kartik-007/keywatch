package demo

import (
	"context"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/source"
	"github.com/Kartik-007/keywatch/internal/testutil"
)

type fakeSource struct {
	name    string
	secrets []core.FoundSecret
	notes   []string
}

func (s fakeSource) Name() string    { return s.name }
func (s fakeSource) Available() bool { return true }
func (s fakeSource) Collect(context.Context) ([]core.FoundSecret, []string, error) {
	return s.secrets, s.notes, nil
}

func sources() []source.Source {
	const varOpenAI, varAnthropic = "OPENAI_API_KEY", "ANTHROPIC_API_KEY"
	return []source.Source{
		fakeSource{name: "local", secrets: []core.FoundSecret{{
			Provider: core.OpenAI, Value: testutil.OpenAI1,
			Location: core.Location{Source: "local", Where: ".env.local", Var: varOpenAI, Committed: true},
		}}},
		fakeSource{name: "railway", secrets: []core.FoundSecret{
			{
				Provider: core.OpenAI, Value: testutil.OpenAI1,
				Location: core.Location{Source: "railway", Where: "support-bot · production", Var: varOpenAI},
			},
			{
				Provider: core.OpenAI, Value: testutil.OpenAI2,
				Location: core.Location{Source: "railway", Where: "email-agent · production", Var: varOpenAI},
			},
		}},
		fakeSource{name: "vercel", secrets: []core.FoundSecret{{
			Provider: core.Anthropic, Value: testutil.Anthropic1,
			Location: core.Location{Source: "vercel", Where: "landing-chat · production", Var: varAnthropic},
		}}},
		// GitHub secrets are write-only, so like the real source this returns
		// no value. core.Join attaches it to …a91f by variable name.
		fakeSource{
			name: "github",
			secrets: []core.FoundSecret{{
				Provider: core.OpenAI,
				Location: core.Location{Source: "github", Where: "acme/support-bot · Actions secret", Var: varOpenAI, NameOnly: true},
			}},
			notes: []string{"GitHub secrets are write-only: matched 1 by name, value not checked."},
		},
	}
}
