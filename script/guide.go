package script

import _ "embed"

// Guide is the language reference written for agents. The CLI, the HTTP API
// and the MCP server all serve this same text.
//
//go:embed guide.md
var Guide string
