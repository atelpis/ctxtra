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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ctxtra/ctxtra/internal/mcp/creds"
	"github.com/ctxtra/ctxtra/internal/mcp/names"
	"github.com/ctxtra/ctxtra/internal/mcp/status"
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
	// Connect starts a session with one server; nil uses a Dialer with
	// Secrets.
	Connect func(ctx context.Context, s upstream.Server, dir string, c *mcp.Client) (*Upstream, error)
	// Secrets has the servers' secret values, Tokens their OAuth tokens.
	Secrets Secrets
	Tokens  Tokens
	// SignInState is a server's sign-in generation and whether it is
	// signed in, from local files: a server missing because it needs
	// sign-in is connected when they change. nil doesn't wait.
	SignInState func(upstream.Server) (int64, bool)
	// SignInPoll is how often SignInState is read; 0 is 3 seconds.
	SignInPoll time.Duration
	// Log records what happened; nil logs nothing.
	Log func(Record)
	// Report gets the session start's status record for the folder, for
	// ctxtra mcp status; nil keeps none.
	Report func(status.Record)
	// NeedsSignIn reports whether a server's state says it needs sign-in,
	// from local files, for the instructions.
	NeedsSignIn func(upstream.Server) bool
	// KeepsFirstList is set for an agent that ignores tool list changes,
	// such as Codex and Muse: tools that come later reach a new session
	// only.
	KeepsFirstList bool
}

// Run serves the agent over t until the agent closes it or ctx ends, then
// stops every server it started. It returns only errors of the agent's
// connection.
func Run(ctx context.Context, o Options, t mcp.Transport) error {
	if o.Deadline == 0 {
		o.Deadline = Deadline
	}
	if o.Connect == nil {
		o.Connect = Dialer{Secrets: o.Secrets, Tokens: o.Tokens}.Dial
	}
	if o.SignInPoll == 0 {
		o.SignInPoll = 3 * time.Second
	}
	if o.Log == nil {
		o.Log = func(Record) {}
	}
	if o.Start.IsZero() {
		o.Start = time.Now()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := &proxy{o: o, ctx: ctx, ready: make(chan struct{}), tools: map[string]route{}, progress: map[string]*progressQueue{},
		latest: map[string][]*mcp.Tool{}, resolved: make(chan struct{})}
	p.server = mcp.NewServer(&mcp.Implementation{Name: "ctxtra", Version: o.Version}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
	})
	p.server.AddReceivingMiddleware(p.waitForTools, p.instruct)
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
	tools     map[string]route          // by shown name
	listed    map[string][]*mcp.Tool    // each connected server's tools, by server name
	progress  map[string]*progressQueue // running calls, by their progress tokens
	latest    map[string][]*mcp.Tool    // each server's last tool list before the first list was answered
	stopping  bool                      // no more upstreams are added
	ended     map[*Upstream]bool        // servers that ended during the session

	report *report

	resolved chan struct{}     // closed when bound is set
	bound    []upstream.Server // the session's servers
}

// initWait is how long the answer to initialize waits for the session's
// servers, to name in the instructions those that need sign-in. Resolving
// is quick unless the folder's repository is slow to read.
const initWait = 500 * time.Millisecond

// instruct adds the instructions to the answer to initialize: Claude Code
// shows them in the model's system prompt, Codex in the description of
// the server's tools. They say how to learn why a tool is missing, and
// name the servers whose state says they need sign-in.
func (p *proxy) instruct(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "initialize" && method != "server/discover" {
			return next(ctx, method, req)
		}
		select {
		case <-p.resolved:
		case <-time.After(initWait):
		case <-ctx.Done():
		}
		res, err := next(ctx, method, req)
		if err != nil {
			return res, err
		}
		// initialize, or discover in protocol versions from 2026-07-28.
		switch r := res.(type) {
		case *mcp.InitializeResult:
			r.Instructions = p.instructions()
		case *mcp.DiscoverResult:
			r.Instructions = p.instructions()
		}
		return res, err
	}
}

func (p *proxy) instructions() string {
	select {
	case <-p.resolved:
	default:
		return ""
	}
	if len(p.bound) == 0 {
		return ""
	}
	var names []string
	for _, s := range p.bound {
		names = append(names, s.Name)
	}
	text := "This server gives the session the tools of the MCP servers that ctxtra binds to this folder (" +
		strings.Join(names, ", ") + "). When an expected tool of theirs is missing or a call fails, run " +
		"`ctxtra mcp status` in the session folder: it says why."
	if p.o.NeedsSignIn == nil {
		return text
	}
	for _, s := range p.bound {
		if p.o.NeedsSignIn(s) {
			then := ", and its tools follow."
			if p.o.KeepsFirstList {
				then = "; its tools come in a new session."
			}
			text += "\n" + s.Name + " needs sign-in and has no tools now: the user runs `ctxtra mcp login " + s.Name +
				"` in a terminal" + then
		}
	}
	return text
}

// report is the session start's status record, written once the first
// tool list was answered and again for each later change.
type report struct {
	mu      sync.Mutex
	rec     status.Record
	write   func(status.Record)
	written bool
	ended   map[string]bool // servers that ended: later startup news doesn't revive them
}

func newReport(o Options, servers []upstream.Server) *report {
	if len(servers) == 0 {
		// A folder without servers keeps no record.
		return nil
	}
	r := &report{write: o.Report, rec: status.Record{Dir: o.Dir, Agent: o.Agent, Started: o.Start.UTC(),
		DeadlineMs: o.Deadline.Milliseconds()}}
	for _, s := range servers {
		r.rec.Servers = append(r.rec.Servers, status.Server{Name: s.Name, State: status.Missing})
	}
	return r
}

// set records what happened to the server name. A server that ended stays
// missing, whatever its start reports after that.
func (r *report) set(name, state, reason string, ms int64) {
	r.update(name, state, reason, ms, false)
}

// ended records that name ended during the session.
func (r *report) end(name, reason string) {
	r.update(name, status.Missing, reason, 0, true)
}

// restart records a server that connected again, after a sign-in.
func (r *report) restart(name, reason string, ms int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.ended, name)
	r.mu.Unlock()
	r.set(name, status.Late, reason, ms)
}

func (r *report) update(name, state, reason string, ms int64, ended bool) {
	if r == nil || r.write == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended[name] && !ended {
		return
	}
	if ended {
		if r.ended == nil {
			r.ended = map[string]bool{}
		}
		r.ended[name] = true
	}
	for i := range r.rec.Servers {
		if r.rec.Servers[i].Name == name {
			r.rec.Servers[i] = status.Server{Name: name, State: state, Reason: reason, Ms: ms}
		}
	}
	if r.written {
		r.write(r.copyLocked())
	}
}

// flush writes the record, the first time when the first tool list was
// answered.
func (r *report) flush() {
	if r == nil || r.write == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.written = true
	r.write(r.copyLocked())
}

func (r *report) copyLocked() status.Record {
	c := r.rec
	c.Servers = slices.Clone(r.rec.Servers)
	return c
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
	// Tracked: stop waits for it, and for the git processes it runs.
	p.wg.Go(func() {
		servers, problems := p.o.Servers(p.o.Dir)
		got <- resolved{servers, problems}
	})
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
	p.bound = servers
	close(p.resolved)
	p.report = newReport(p.o, servers)
	if p.isOpen() {
		// The first list was answered while the servers were being found:
		// each is missing from it, and may follow.
		for _, s := range servers {
			p.report.set(s.Name, status.Missing, "too slow for the first tool list: finding the folder's servers took too long", 0)
		}
		p.report.flush()
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
				p.report.set(r.s.Name, status.Missing, rec.Error, 0)
				var login *creds.LoginError
				if errors.As(r.err, &login) && p.o.SignInState != nil {
					p.wg.Go(func() { p.awaitSignIn(ctx, r.s) })
				}
				continue
			}
			p.o.Log(rec)
			if p.isOpen() {
				late := time.Since(p.o.Start).Milliseconds()
				p.o.Log(Record{Event: "late", Server: r.s.Name, Ms: late})
				p.report.set(r.s.Name, status.Late, "too slow for the first tool list", late)
				p.setTools(r.up, r.tools)
				continue
			}
			p.report.set(r.s.Name, status.Ready, "", time.Since(p.o.Start).Milliseconds())
			first[r.s.Name] = r.tools
		case <-deadline.C:
			var slow []string
			for n := range waiting {
				slow = append(slow, n)
			}
			slices.Sort(slow)
			p.o.Log(Record{Event: "deadline", Servers: slow})
			for _, n := range slow {
				p.report.set(n, status.Missing, "too slow for the first tool list", 0)
			}
			p.publish(first)
			p.open()
			p.report.flush()
		case <-ctx.Done():
			return
		}
	}
	if !p.isOpen() {
		p.publish(first)
		p.open()
	}
	// Also when the first list was answered while the servers were still
	// being resolved: they all came late.
	p.report.flush()
}

// awaitSignIn connects to s, which needs sign-in, once its state file
// says it was signed in again, and adds its tools with a list change.
// The session's set of servers doesn't change: s is one of them.
func (p *proxy) awaitSignIn(ctx context.Context, s upstream.Server) {
	// A sign-in may have finished between the failed connection and now:
	// the first check connects whenever the state says signed in.
	gen := int64(-1)
	tick := time.NewTicker(p.o.SignInPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		g, ok := p.o.SignInState(s)
		if g == gen || !ok && gen != -1 {
			continue
		}
		first := gen == -1
		gen = g
		if !ok && first {
			continue
		}
		up, tools, err := p.connect(ctx, s)
		var login *creds.LoginError
		if errors.As(err, &login) {
			continue
		}
		if err != nil {
			p.o.Log(Record{Event: "connect", Server: s.Name, Error: errorKind(err)})
			return
		}
		if !p.add(up) {
			up.Close()
			return
		}
		p.wg.Go(func() { p.watchExit(up) })
		p.o.Log(Record{Event: "signed-in", Server: s.Name, Tools: len(tools)})
		p.report.restart(s.Name, "signed in during the session", time.Since(p.o.Start).Milliseconds())
		select {
		case <-p.ready:
		case <-ctx.Done():
			return
		}
		p.setTools(up, tools)
		return
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
	// A list change only marks the list as changed: one worker lists the
	// tools again, so a server that sends many changes, or doesn't answer
	// the list, costs one pending list at most.
	changed := make(chan struct{}, 1)
	client := mcp.NewClient(&mcp.Implementation{Name: "ctxtra", Version: p.o.Version}, &mcp.ClientOptions{
		// No roots: a server learns nothing about the session's folders.
		Capabilities: &mcp.ClientCapabilities{},
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			select {
			case changed <- struct{}{}:
			default:
			}
		},
		ProgressNotificationHandler: func(ctx context.Context, req *mcp.ProgressNotificationClientRequest) {
			key, ok := tokenKey(req.Params.ProgressToken)
			if !ok {
				return
			}
			p.mu.Lock()
			q := p.progress[key]
			p.mu.Unlock()
			if q != nil {
				q.send(req.Params)
			}
		},
	})
	up, err := p.o.Connect(ctx, s, p.o.Dir, client)
	if err != nil {
		return nil, nil, err
	}
	p.wg.Go(func() { p.relist(ctx, up, changed) })
	tools, err := listTools(ctx, up.Session)
	if err != nil {
		up.Close()
		return nil, nil, err
	}
	return up, tools, nil
}

// Limits of a server's tool list: a server that pages without end, or
// lists far more tools than any agent can use, mustn't exhaust the proxy,
// which serves the session's other servers too.
const (
	maxToolPages = 100
	maxTools     = 2000
	maxToolBytes = 8 << 20 // of the tools as JSON
)

// ErrTooManyTools is the error of a tool list over the limits.
var ErrTooManyTools = errors.New("the server lists too many tools, or pages without end")

func listTools(ctx context.Context, cs *mcp.ClientSession) ([]*mcp.Tool, error) {
	return ListTools(ctx, cs)
}

// ListTools is every tool of the server, page by page, within the limits.
func ListTools(ctx context.Context, cs *mcp.ClientSession) ([]*mcp.Tool, error) {
	var out []*mcp.Tool
	seen := map[string]bool{}
	cursor := ""
	size := 0
	for range maxToolPages {
		res, err := cs.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Tools...)
		for _, t := range res.Tools {
			b, err := json.Marshal(t)
			if err != nil {
				return nil, err
			}
			size += len(b)
		}
		if len(out) > maxTools || size > maxToolBytes {
			return nil, ErrTooManyTools
		}
		if res.NextCursor == "" {
			return out, nil
		}
		if seen[res.NextCursor] {
			return nil, ErrTooManyTools
		}
		seen[res.NextCursor] = true
		cursor = res.NextCursor
	}
	return nil, ErrTooManyTools
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
	if p.ended[up] {
		tools = nil
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
	params := &mcp.CallToolParams{Meta: forwardMeta(req.Params.Meta), Name: r.name, Arguments: req.Params.Arguments}
	if len(req.Params.Arguments) == 0 {
		params.Arguments = nil
	}
	if key, ok := tokenKey(req.Params.GetProgressToken()); ok {
		q := newProgress(p.ctx, req.Session)
		p.mu.Lock()
		p.progress[key] = q
		p.mu.Unlock()
		defer func() {
			p.mu.Lock()
			delete(p.progress, key)
			p.mu.Unlock()
			q.close()
		}()
	}
	// The call ends with the session too, not only when the agent cancels
	// it.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	defer context.AfterFunc(p.ctx, stop)()
	res, err = r.up.Session.CallTool(ctx, params)
	var login *creds.LoginError
	switch {
	case errors.As(err, &login):
		// The agent and the user see what to do; never a browser.
		p.o.Log(Record{Event: "call", Server: server, Error: errorKind(err)})
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: login.Error()}}}, nil
	case err != nil:
		p.o.Log(Record{Event: "call", Server: server, Error: errorKind(err)})
	case res.IsError:
		p.o.Log(Record{Event: "call", Server: server, Error: "tool error"})
	}
	return res, err
}

// forwardMeta is a call's _meta without the protocol's own keys
// (io.modelcontextprotocol/...): they describe the agent's session with
// the proxy, not the proxy's with the server, and a server can refuse a
// request that carries another session's protocol version.
func forwardMeta(m mcp.Meta) mcp.Meta {
	if m == nil {
		return nil
	}
	out := mcp.Meta{}
	for k, v := range m {
		if !strings.HasPrefix(k, "io.modelcontextprotocol/") {
			out[k] = v
		}
	}
	return out
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
	// Its tools go first, so that no list has them while it is being
	// stopped. Then it is stopped now, with the processes it started: a
	// process group ID kept until the session ends could belong to other
	// processes by then.
	p.mu.Lock()
	if p.ended == nil {
		p.ended = map[*Upstream]bool{}
	}
	p.ended[up] = true
	delete(p.latest, up.Server.Name)
	if _, shown := p.listed[up.Server.Name]; shown {
		p.setToolsLocked(up, nil)
	}
	p.mu.Unlock()
	up.Close()
	st := up.ExitState()
	if st == "" {
		st = "connection closed"
	}
	p.o.Log(Record{Event: "server ended", Server: up.Server.Name, Error: st})
	p.report.end(up.Server.Name, "ended during the session: "+st)
	// A remote server whose sign-in ended with it comes back after a login.
	if !up.Server.Local() && p.o.SignInState != nil {
		if _, ok := p.o.SignInState(up.Server); !ok {
			p.wg.Go(func() { p.awaitSignIn(p.ctx, up.Server) })
		}
	}
}

// tokenKey is a progress token as a map key. The spec allows strings and
// numbers; anything else isn't forwarded.
func tokenKey(tok any) (string, bool) {
	switch t := tok.(type) {
	case string:
		return "s:" + t, true
	case float64, int, int64, int32, json.Number:
		return fmt.Sprintf("n:%v", t), true
	}
	return "", false
}

// progressQueue forwards one call's progress to the agent, in order, in a
// goroutine of its own: a write to an agent that doesn't read must not
// hold up the server's session. When the agent falls behind, progress is
// dropped: a later notification says more than an earlier one.
type progressQueue struct {
	mu     sync.Mutex
	ch     chan *mcp.ProgressNotificationParams
	closed bool
}

func newProgress(ctx context.Context, agent *mcp.ServerSession) *progressQueue {
	q := &progressQueue{ch: make(chan *mcp.ProgressNotificationParams, 16)}
	go func() {
		for params := range q.ch {
			if ctx.Err() == nil {
				_ = agent.NotifyProgress(ctx, params)
			}
		}
	}()
	return q
}

func (q *progressQueue) send(params *mcp.ProgressNotificationParams) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	select {
	case q.ch <- params:
	default:
	}
}

func (q *progressQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
}

// listTimeout bounds a list of a server's tools after a change.
const listTimeout = 10 * time.Second

// relist lists up's tools again after each change, until the session
// ends. Changes that come while it lists are one more list.
func (p *proxy) relist(ctx context.Context, up *Upstream, changed <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
		lctx, cancel := context.WithTimeout(ctx, listTimeout)
		tools, err := listTools(lctx, up.Session)
		cancel()
		if err != nil {
			p.o.Log(Record{Event: "list", Server: up.Server.Name, Error: errorKind(err)})
			continue
		}
		// Kept for when the server's tools are shown, or shown now when
		// they are: one lock, so no list falls in between.
		p.mu.Lock()
		p.latest[up.Server.Name] = tools
		if _, shown := p.listed[up.Server.Name]; shown {
			p.setToolsLocked(up, tools)
		}
		p.mu.Unlock()
	}
}
