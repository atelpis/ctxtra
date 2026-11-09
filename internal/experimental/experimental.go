// Package experimental turns on features that are not released yet.
package experimental

import "os"

// MCPVar turns on MCP servers: the mcp-servers config keys, the ctxtra mcp
// commands, binding add --mcp and the MCP entry that setup writes. Without
// it, all of them are off, so a release can ship the work in progress
// without anything users could start to rely on.
const MCPVar = "CTXTRA_EXPERIMENTAL_MCP"

// MCP reports whether MCP servers are on.
func MCP() bool { return os.Getenv(MCPVar) == "1" }
