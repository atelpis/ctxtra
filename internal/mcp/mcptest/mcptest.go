// Package mcptest is an MCP server for tests: it runs on stdin and stdout
// and has tools that report what the server got.
package mcptest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Main serves on stdin and stdout until stdin closes. Arguments:
//
//	-delay D    wait D (a Go duration) before answering anything
//	-tools a,b  also offer tools with these names, which answer "ok"
//	-pidfile F  write the server's process ID to F
//	-ignore-term  keep running on SIGTERM
//	-child F    start a child process that ignores SIGTERM, and write its
//	            process ID to F
//	-exit-after D  exit with status 17 after D
//	-exit-now   exit with status 17 before answering anything
//
// Tools: echo (returns its arguments), env (its folder and environment),
// fail (an error that repeats its arguments), slow (waits until cancelled,
// sending progress), big (a 1 MiB answer), bigprogress (1 MiB of progress), hang (never answers, also when cancelled), and the
// -tools names. It also offers a prompt and a
// resource, which a proxy must not forward.
func Main(args []string) {
	var delay time.Duration
	var extra []string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-delay":
			delay, _ = time.ParseDuration(args[i+1])
		case "-tools":
			extra = strings.Split(args[i+1], ",")
		case "-pidfile":
			_ = os.WriteFile(args[i+1], []byte(fmt.Sprint(os.Getpid())), 0o644)
		}
	}
	if slices.Contains(args, "-exit-now") {
		os.Exit(17)
	}
	if slices.Contains(args, "-ignore-term") {
		ignoreTerm()
	}
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-child":
			startChild(args[i+1])
		case "-exit-after":
			if d, err := time.ParseDuration(args[i+1]); err == nil {
				time.AfterFunc(d, func() { os.Exit(17) })
			}
		}
	}
	time.Sleep(delay)
	_ = Server(extra).Run(context.Background(), &mcp.StdioTransport{})
}

// Server is the test server, with extra tools.
func Server(extra []string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "mcptest", Version: "1"}, nil)
	obj := map[string]any{"type": "object"}
	s.AddTool(&mcp.Tool{Name: "echo", Description: "Echo the arguments.", InputSchema: obj, Title: "Echo",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return text("echo " + string(req.Params.Arguments)), nil
		})
	s.AddTool(&mcp.Tool{Name: "env", Description: "The folder and environment.", InputSchema: obj},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			wd, _ := os.Getwd()
			env := os.Environ()
			slices.Sort(env)
			b, _ := json.Marshal(map[string]any{"cwd": wd, "env": env})
			return text(string(b)), nil
		})
	s.AddTool(&mcp.Tool{Name: "fail", Description: "Fail.", InputSchema: obj},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, fmt.Errorf("failed with %s", req.Params.Arguments)
		})
	s.AddTool(&mcp.Tool{Name: "slow", Description: "Wait until cancelled.", InputSchema: obj},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if tok := req.Params.GetProgressToken(); tok != nil {
				_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Progress: 1, Message: "started"})
			}
			<-ctx.Done()
			return text("cancelled"), nil
		})
	s.AddTool(&mcp.Tool{Name: "big", Description: "Answer 1 MiB.", InputSchema: obj},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return text(strings.Repeat("x", 1<<20)), nil
		})
	s.AddTool(&mcp.Tool{Name: "bigprogress", Description: "Send 1 MiB of progress, then wait until cancelled.", InputSchema: obj},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if tok := req.Params.GetProgressToken(); tok != nil {
				_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Progress: 1, Message: strings.Repeat("x", 1<<20)})
			}
			<-ctx.Done()
			return text("cancelled"), nil
		})
	s.AddTool(&mcp.Tool{Name: "hang", Description: "Never answer.", InputSchema: obj},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			select {}
		})
	for _, name := range extra {
		s.AddTool(&mcp.Tool{Name: name, Description: "Tool " + name + ".", InputSchema: obj},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return text("ok " + name), nil
			})
	}
	s.AddPrompt(&mcp.Prompt{Name: "greet"}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{}, nil
	})
	s.AddResource(&mcp.Resource{Name: "r", URI: "test://r"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{}, nil
	})
	return s
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
