// Package claude defines the Claude Code agent: the official client,
// installed with the published native installer as the agent user, and the
// hosts that the client and its sign-in need (D24). Orbit runs the client
// unmodified and never handles its credentials (R04, D08).
package claude

import "github.com/cultofthecode/orbit/internal/agent"

// Definition is the Claude Code agent, named claude-code.
var Definition = agent.Definition{
	Name:    "claude-code",
	Install: "curl -fsSL https://claude.ai/install.sh | bash",
	EgressDomains: []string{
		"claude.ai",
		"downloads.claude.ai",
		"api.anthropic.com",
		"platform.claude.com",
		"mcp-proxy.anthropic.com",
		"code.claude.com",
		"http-intake.logs.us5.datadoghq.com",
		"browser-intake-us5-datadoghq.com",
	},
}
