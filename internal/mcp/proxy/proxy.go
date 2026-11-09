// Package proxy is the MCP server that ctxtra setup adds to each agent: it
// gives the session the tools of the MCP servers bound to its folder, and
// forwards each call to the server the tool came from.
//
// The servers are fixed when the proxy starts, like the agents' own MCP
// servers. A proxy problem never breaks a session: a server that fails is
// left out, and the proxy serves the tools it can.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ctxtra/ctxtra/internal/mcp/names"
	"github.com/ctxtra/ctxtra/internal/mcp/upstream"
)

// Deadline is how long after its start the proxy waits for slow servers
// before it answers the first tool list, when Options doesn't say. Codex
// and Muse keep only the first list, and an agent that gives up on the
// proxy loses every tool.
const Deadline = 10 * time.Second

// Options are what one proxy run needs.
type Options struct {
	Agent   string // the agent that started the proxy, for the log
	Version string
	Dir     string    // the session folder
	Start   time.Time // when the process started; Deadline counts from it
	// Deadline is the agent's wait for slow servers; 0 for the package's
	// Deadline.
	Deadline time.Duration
	// Servers returns the servers bound to dir, and problems to log. It
	// never fails: a broken config has no servers.
	Servers func(dir string) ([]upstream.Server, []string)
	// Connect starts a session with one server; nil uses Dial.
	Connect func(ctx context.Context, s upstream.Server, dir string, c *mcp.Client) (*Upstream, error)
	// Log records what happened; nil logs nothing.
	Log func(Record)
}

// Run serves the agent over t until the agent closes it or ctx ends, then
// stops every server it started. It returns only errors of the agent's
// connection.
func Run(ctx context.Context, o Options, t mcp.Transport) error {
	if o.Deadline == 0 {
		o.Deadline = Deadline
	}
	if o.Connect == nil {
		o.Connect = Dial
	}
	if o.Log == nil {
		o.Log = func(Record) {}
	}
	if o.Start.IsZero() {
		o.Start = time.Now()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := &proxy{o: o, ctx: ctx, ready: make(chan struct{}), tools: map[string]route{}, progress: map[any]*mcp.ServerSession{},
		latest: map[string][]*mcp.Tool{}}
	p.server = mcp.NewServer(&mcp.Implementation{Name: "ctxtra", Version: o.Version}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
	})
	p.server.AddReceivingMiddleware(p.waitForTools)
	p.wg.Go(func() { p.start(ctx) })
	// The servers stop as soon as the session ends: when the agent closes
	// the connection, or ctx ends. The SDK then still waits for running
	// calls, and a call that hangs in a server must not keep it running.
	stopped := make(chan struct{})
	go func() {
		<-ctx.Done()
		p.stop()
		close(stopped)
	}()
	ran := make(chan error, 1)
	go func() { ran <- p.server.Run(ctx, watch{t, cancel}) }()
	var err error
	select {
	case err = <-ran:
	case <-ctx.Done():
		// The SDK waits for the agent's connection to drain, for example a
		// large answer that the agent no longer reads: once the servers
		// stopped, the proxy waits for it only a short while.
		// Stopping is bounded too: a server's handler that writes to a
		// blocked agent connection can hold up its session's close.
		select {
		case <-stopped:
		case <-time.After(3 * stopGrace):
		}
		select {
		case err = <-ran:
		case <-time.After(stopGrace):
		}
	}
	cancel()
	o.Log(Record{Event: "exit"})
	// The agent ends a session by closing the connection.
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, mcp.ErrConnectionClosed) {
		return nil
	}
	return err
}

// watch is a transport that calls end when the agent's side of the
// connection ends.
type watch struct {
	mcp.Transport
	end func()
}

func (w watch) Connect(ctx context.Context) (mcp.Connection, error) {
	c, err := w.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return watchConn{c, w.end}, nil
}

type watchConn struct {
	mcp.Connection
	end func()
}

func (c watchConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	m, err := c.Connection.Read(ctx)
	if err != nil {
		c.end()
	}
	return m, err
}

// proxy is one session's proxy.
type proxy struct {
	o      Options
	ctx    context.Context // ends with the session
	server *mcp.Server
	wg     sync.WaitGroup // start and the connections it makes

	ready     chan struct{} // closed when the first tool list may be answered
	readyOnce sync.Once

	mu        sync.Mutex
	upstreams []*Upstream
	tools     map[string]route           // by shown name
	listed    map[string][]*mcp.Tool     // each connected server's tools, by server name
	progress  map[any]*mcp.ServerSession // progress tokens of running calls
	latest    map[string][]*mcp.Tool     // each server's last tool list before the first list was answered
	stopping  bool                       // no more upstreams are added
}

// route is where a shown tool goes.
type route struct {
	up   *Upstream
	name string // the server's own tool name
}

// waitForTools holds the agent's tool lists and calls until the first one may be
// answered: some agents keep only the first list they get.
func (p *proxy) waitForTools(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		// A call before the first list waits too: its tool may not be
		// there yet.
		if method == "tools/list" || method == "tools/call" {
			select {
			case <-p.ready:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return next(ctx, method, req)
	}
}

func (p *proxy) open() { p.readyOnce.Do(func() { close(p.ready) }) }

// start resolves the session's servers and connects to all of them at
// once. The first tool list waits for all of them, or until the deadline.
func (p *proxy) start(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			p.o.Log(Record{Event: "error", Error: fmt.Sprint("panic: ", r)})
			p.open()
		}
	}()
	// The deadline counts from the proxy's start, and covers resolving the
	// servers too: finding the folder's repository can be slow.
	deadline := time.NewTimer(time.Until(p.o.Start.Add(p.o.Deadline)))
	defer deadline.Stop()
	type resolved struct {
		servers  []upstream.Server
		problems []string
	}
	got := make(chan resolved, 1)
	go func() {
		servers, problems := p.o.Servers(p.o.Dir)
		got <- resolved{servers, problems}
	}()
	var servers []upstream.Server
	select {
	case r := <-got:
		servers = r.servers
		p.logStart(r.servers, r.problems)
	case <-deadline.C:
		p.o.Log(Record{Event: "deadline", Error: "resolving the servers"})
		p.open()
		select {
		case r := <-got:
			servers = r.servers
			p.logStart(r.servers, r.problems)
		case <-ctx.Done():
			return
		}
	case <-ctx.Done():
		return
	}
	type result struct {
		s     upstream.Server
		up    *Upstream
		tools []*mcp.Tool
		err   error
		took  time.Duration
	}
	results := make(chan result, len(servers))
	for _, s := range servers {
		p.wg.Go(func() {
			began := time.Now()
			up, tools, err := p.connect(ctx, s)
			if err == nil && !p.add(up) {
				// The session ended while it connected.
				up.Close()
				err = context.Canceled
			}
			if err == nil {
				p.wg.Go(func() { p.watchExit(up) })
			}
			results <- result{s, up, tools, err, time.Since(began)}
		})
	}
	waiting := map[string]bool{}
	for _, s := range servers {
		waiting[s.Name] = true
	}
	first := map[string][]*mcp.Tool{}
	for len(waiting) > 0 {
		select {
		case r := <-results:
			delete(waiting, r.s.Name)
			rec := Record{Event: "connect", Server: r.s.Name, Ms: r.took.Milliseconds(), Tools: len(r.tools)}
			if r.err != nil {
				rec.Error = errorKind(r.err)
				p.o.Log(rec)
				continue
			}
			p.o.Log(rec)
			if p.isOpen() {
				p.o.Log(Record{Event: "late", Server: r.s.Name, Ms: time.Since(p.o.Start).Milliseconds()})
				p.setTools(r.up, r.tools)
				continue
			}
			first[r.s.Name] = r.tools
		case <-deadline.C:
			var slow []string
			for n := range waiting {
				slow = append(slow, n)
			}
			slices.Sort(slow)
			p.o.Log(Record{Event: "deadline", Servers: slow})
			p.publish(first)
			p.open()
		case <-ctx.Done():
			return
		}
	}
	if !p.isOpen() {
		p.publish(first)
		p.open()
	}
}

func (p *proxy) isOpen() bool {
	select {
	case <-p.ready:
		return true
	default:
		return false
	}
}

// connect starts a session with s and lists its tools.
func (p *proxy) connect(ctx context.Context, s upstream.Server) (*Upstream, []*mcp.Tool, error) {
	var cur atomic.Pointer[Upstream] // set once connected
	client := mcp.NewClient(&mcp.Implementation{Name: "ctxtra", Version: p.o.Version}, &mcp.ClientOptions{
		// No roots: a server learns nothing about the session's folders.
		Capabilities: &mcp.ClientCapabilities{},
		ToolListChangedHandler: func(ctx context.Context, _ *mcp.ToolListChangedRequest) {
			up := cur.Load()
			if up == nil {
				return
			}
			tools, err := listTools(ctx, up.Session)
			if err != nil {
				p.o.Log(Record{Event: "list", Server: s.Name, Error: errorKind(err)})
				return
			}
			// Kept for when the server's tools are shown, or shown now
			// when they are: one lock, so no list falls in between.
			p.mu.Lock()
			defer p.mu.Unlock()
			p.latest[s.Name] = tools
			if _, shown := p.listed[s.Name]; shown {
				p.setToolsLocked(up, tools)
			}
		},
		ProgressNotificationHandler: func(ctx context.Context, req *mcp.ProgressNotificationClientRequest) {
			p.mu.Lock()
			agent := p.progress[req.Params.ProgressToken]
			p.mu.Unlock()
			// Not in the handler: a write to an agent that doesn't
			// read would hold up the server's session.
			if agent != nil {
				go func() { _ = agent.NotifyProgress(p.ctx, req.Params) }()
			}
		},
	})
	up, err := p.o.Connect(ctx, s, p.o.Dir, client)
	if err != nil {
		return nil, nil, err
	}
	cur.Store(up)
	tools, err := listTools(ctx, up.Session)
	if err != nil {
		up.Close()
		return nil, nil, err
	}
	return up, tools, nil
}

func listTools(ctx context.Context, cs *mcp.ClientSession) ([]*mcp.Tool, error) {
	var out []*mcp.Tool
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// add records up, to stop it with the session; false when the session
// is ending.
func (p *proxy) add(up *Upstream) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping {
		return false
	}
	p.upstreams = append(p.upstreams, up)
	return true
}

// logStart logs the bound servers. Problems are counted, not quoted: a
// config problem can quote a URL with a secret in it.
func (p *proxy) logStart(servers []upstream.Server, problems []string) {
	var bound []string
	for _, s := range servers {
		bound = append(bound, s.Name)
	}
	p.o.Log(Record{Event: "start", Dir: p.o.Dir, Servers: bound, Problems: len(problems)})
}

// publish shows the tools of the servers that connected in time.
func (p *proxy) publish(first map[string][]*mcp.Tool) {
	p.mu.Lock()
	ups := slices.Clone(p.upstreams)
	p.mu.Unlock()
	for _, up := range ups {
		if tools, ok := first[up.Server.Name]; ok {
			p.setTools(up, tools)
		}
	}
}

// setTools shows up's tools: tools, or the list that a change
// notification brought since.
func (p *proxy) setTools(up *Upstream, tools []*mcp.Tool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if newer, ok := p.latest[up.Server.Name]; ok {
		tools = newer
	}
	p.setToolsLocked(up, tools)
}

// setToolsLocked replaces up's tools, and renames the session's tools where
// a new name collides. The agent gets one list change notification. p.mu
// is held.
func (p *proxy) setToolsLocked(up *Upstream, tools []*mcp.Tool) {
	if !slices.Contains(p.upstreams, up) {
		return
	}
	if p.listed == nil {
		p.listed = map[string][]*mcp.Tool{}
	}
	p.listed[up.Server.Name] = tools
	byServer := map[string]*Upstream{}
	for _, u := range p.upstreams {
		byServer[u.Server.Name] = u
	}
	var all []names.Tool
	defs := map[names.Tool]*mcp.Tool{}
	for server, list := range p.listed {
		for _, t := range list {
			nt := names.Tool{Server: server, Name: t.Name}
			all = append(all, nt)
			defs[nt] = t
		}
	}
	named, dropped := names.Assign(all)
	for _, d := range dropped {
		p.o.Log(Record{Event: "dropped", Server: d.Server, Error: "name collision"})
	}
	want := map[string]route{}
	var add []*mcp.Tool
	for _, n := range named {
		want[n.Shown] = route{up: byServer[n.Server], name: n.Name}
		t := *defs[n.Tool]
		t.Name = n.Shown
		t.Description = names.Describe(n, t.Description)
		add = append(add, &t)
	}
	var remove []string
	for shown := range p.tools {
		if _, ok := want[shown]; !ok {
			remove = append(remove, shown)
		}
	}
	slices.Sort(remove)
	if len(remove) > 0 {
		p.server.RemoveTools(remove...)
	}
	p.tools = want
	for _, t := range add {
		if err := p.addTool(t); err != nil {
			p.o.Log(Record{Event: "tool", Server: want[t.Name].up.Server.Name, Error: err.Error()})
			delete(p.tools, t.Name)
			// An earlier definition of the tool would stay listed.
			p.server.RemoveTools(t.Name)
		}
	}
}

// addTool adds t to the agent's list. The SDK panics on a tool it can't
// serve, such as one whose input schema isn't an object: that tool is left
// out.
func (p *proxy) addTool(t *mcp.Tool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			// Not the SDK's message: it can quote the server's schema.
			err = errors.New("invalid tool definition; tool left out")
		}
	}()
	p.server.AddTool(t, p.call)
	return nil
}

// call forwards a tool call to its server, with the server's own name for
// the tool, and the agent's progress token.
func (p *proxy) call(ctx context.Context, req *mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
	shown := req.Params.Name
	p.mu.Lock()
	r, ok := p.tools[shown]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", shown)
	}
	server := r.up.Server.Name
	defer func() {
		if x := recover(); x != nil {
			err = fmt.Errorf("ctxtra: the call to %s failed", server)
			p.o.Log(Record{Event: "call", Server: server, Error: "panic"})
		}
	}()
	params := &mcp.CallToolParams{Meta: req.Params.Meta, Name: r.name, Arguments: req.Params.Arguments}
	if len(req.Params.Arguments) == 0 {
		params.Arguments = nil
	}
	if tok := req.Params.GetProgressToken(); tok != nil {
		p.mu.Lock()
		p.progress[tok] = req.Session
		p.mu.Unlock()
		defer func() {
			p.mu.Lock()
			delete(p.progress, tok)
			p.mu.Unlock()
		}()
	}
	// The call ends with the session too, not only when the agent cancels
	// it.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	defer context.AfterFunc(p.ctx, stop)()
	res, err = r.up.Session.CallTool(ctx, params)
	switch {
	case err != nil:
		p.o.Log(Record{Event: "call", Server: server, Error: errorKind(err)})
	case res.IsError:
		p.o.Log(Record{Event: "call", Server: server, Error: "tool error"})
	}
	return res, err
}

// stop ends every server session and stops the local servers, also those
// still connecting: their connections end with the session's context, and
// Dial stops a server whose connection failed.
func (p *proxy) stop() {
	p.open()
	p.mu.Lock()
	p.stopping = true
	ups := slices.Clone(p.upstreams)
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, up := range ups {
		wg.Go(func() {
			up.Close()
			if st := up.ExitState(); st != "" {
				p.o.Log(Record{Event: "stopped", Server: up.Server.Name, Error: st})
			}
		})
	}
	wg.Wait()
	p.wg.Wait()
}

// watchExit logs a server whose session ends before the proxy's.
func (p *proxy) watchExit(up *Upstream) {
	_ = up.Session.Wait()
	p.mu.Lock()
	stopping := p.stopping
	p.mu.Unlock()
	if stopping {
		return
	}
	st := up.ExitState()
	if st == "" {
		st = "connection closed"
	}
	p.o.Log(Record{Event: "server ended", Server: up.Server.Name, Error: st})
}
