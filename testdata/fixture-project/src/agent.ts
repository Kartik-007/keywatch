// fixture: a hardcoded fake Anthropic key and a reused fake OpenAI key
const anthropic = new Anthropic({ apiKey: "{{ANTHROPIC_1}}" });
const openai = new OpenAI({ apiKey: process.env.OPENAI_API_KEY ?? "{{OPENAI_1}}" });
