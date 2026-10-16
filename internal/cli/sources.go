package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/fsutil"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/source"
	"github.com/atelpis/ctxtra/internal/state"
	"github.com/atelpis/ctxtra/internal/trust"
)

// sources implements add and sync: the commands that change the config.
type sources struct {
	IO
	ctx  context.Context
	file string // the config
	in   *bufio.Reader
	full bool // sync --full: show every changed file in full
}

func newSources(stdio IO) *sources {
	return &sources{IO: stdio, ctx: context.Background(), file: paths.ConfigPath(), in: bufio.NewReader(stdio.In)}
}

// parseArgs splits args into flags and other arguments. Flags may come
// anywhere, as --name value, --name=value, or --name for the bools.
func parseArgs(args, values, bools []string) (map[string]string, []string, error) {
	flags := map[string]string{}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return flags, append(rest, args[i+1:]...), nil
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			rest = append(rest, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch {
		case slices.Contains(bools, name) && !hasVal:
			flags[name] = "true"
		case slices.Contains(values, name) && hasVal:
			flags[name] = val
		case slices.Contains(values, name) && i+1 < len(args):
			i++
			flags[name] = args[i]
		default:
			return nil, nil, fmt.Errorf("unknown flag or missing value: %s", a)
		}
	}
	return flags, rest, nil
}

// current is the config as it is now, without its binding files. A missing
// config is an empty one.
func (s *sources) current() (*config.Config, error) {
	data, err := fsutil.ReadBounded(s.file, config.MaxFileBytes)
	if errors.Is(err, os.ErrNotExist) {
		return &config.Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := config.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %v; fix it first", paths.Tilde(s.file), err)
	}
	return c, nil
}

// edit changes the config, and says so when it creates it.
func (s *sources) edit(fn func(old []byte) ([]byte, error)) error {
	existed := source.Exists(s.file)
	if err := config.Edit(s.file, fn); err != nil {
		return err
	}
	if !existed {
		fmt.Fprintf(s.Out, "created %s\n", paths.Tilde(s.file))
	}
	return nil
}

func (s *sources) fail(cmd string, err error) int {
	fmt.Fprintf(s.Err, "ctxtra %s: %v\n", cmd, err)
	return 1
}

const addUsage = `usage:
  ctxtra add <git URL> [<binding file in the repo>] [--as NAME] [--branch BRANCH]
  ctxtra add ctxtra://<source>/<binding file>
  ctxtra add <local binding file> [--trust] [--as NAME]
  ctxtra add --trust <folder> [--as NAME]
`

func runAdd(args []string, stdio IO) int {
	f, pos, err := parseArgs(args, []string{"as", "branch"}, []string{"trust"})
	if err != nil || len(pos) < 1 || len(pos) > 2 {
		if err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra add: %v\n", err)
		}
		fmt.Fprint(stdio.Err, addUsage)
		return 2
	}
	s := newSources(stdio)
	target := pos[0]
	local := strings.HasPrefix(target, "/") || strings.HasPrefix(target, "~") || strings.HasPrefix(target, ".")
	if _, err := os.Stat(paths.Expand(target)); err == nil {
		local = true
	}
	switch {
	case strings.HasPrefix(target, config.Scheme):
		if len(pos) > 1 || len(f) > 0 {
			fmt.Fprint(stdio.Err, addUsage)
			return 2
		}
		err = s.addRef(target)
	case local:
		if len(pos) > 1 || f["branch"] != "" {
			fmt.Fprint(stdio.Err, addUsage)
			return 2
		}
		wd, _ := os.Getwd()
		err = s.addLocal(paths.Resolve(wd, target), f["trust"] != "", f["as"])
	default:
		if f["trust"] != "" {
			fmt.Fprintln(stdio.Err, "ctxtra add: --trust is for files and folders on your disk; a source added by URL is pinned")
			return 2
		}
		file := ""
		if len(pos) > 1 {
			file = pos[1]
		}
		err = s.addGit(target, file, f["as"], f["branch"])
	}
	if err != nil {
		return s.fail("add", err)
	}
	return 0
}

var reBindingKind = regexp.MustCompile(`(?m)^kind:\s*Binding\s*$`)

var reRepoName = regexp.MustCompile(`([^/:]+?)(\.git)?/?$`)

// defaultName is a source name from the last part of a URL or folder.
func defaultName(s string) string {
	m := reRepoName.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimLeft(m[1], ".-_")
}

func checkName(c *config.Config, name, what string) error {
	if !config.ValidName(name) {
		return fmt.Errorf("%q can't be a source name (letters, digits, '.', '_', '-'); choose one with --as", name)
	}
	if old, ok := c.Source(name); ok {
		return fmt.Errorf("a source named %s already exists (%s); choose another name for %s with --as", name, describe(old), what)
	}
	return nil
}

func describe(s config.Source) string {
	if s.Pinned() {
		return s.Git
	}
	return "trusted-path " + paths.Tilde(s.TrustedPath)
}

func (s *sources) addGit(url, file, name, branch string) error {
	if err := source.CheckURL(url); err != nil {
		return err
	}
	if file != "" {
		file = path.Clean(filepath.ToSlash(file))
		if err := source.CheckPath(file); err != nil {
			return err
		}
	}
	cur, err := s.current()
	if err != nil {
		return err
	}
	// The source is already there: add the binding file at its commit,
	// without fetching.
	for _, src := range cur.Sources {
		if src.Git == url {
			if file == "" {
				return fmt.Errorf("%s is already the source %s", url, src.Name)
			}
			return s.addRef(config.Scheme + src.Name + "/" + file)
		}
	}
	if name == "" {
		name = defaultName(url)
	}
	if err := checkName(cur, name, url); err != nil {
		return err
	}
	r := source.Open(url)
	if branch == "" {
		if branch, err = r.DefaultBranch(s.ctx); err != nil {
			return err
		}
	}
	commit, err := r.FetchBranch(s.ctx, branch)
	if err != nil {
		return err
	}
	gen, err := r.Materialize(s.ctx, commit)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "fetched %s: %s at %s (%s)\n", name, branch, source.Short(commit), url)
	if file != "" {
		if st, err := os.Lstat(filepath.Join(gen, filepath.FromSlash(file))); err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("%s is not a file in %s; nothing added", file, url)
		}
	}
	ref := config.Scheme + name + "/" + file
	err = s.edit(func(old []byte) ([]byte, error) {
		data, err := config.AddSource(old, name, [][2]string{{"git", url}, {"branch", branch}, {"commit", commit}})
		if err == nil && file != "" {
			data, err = config.AddBinding(data, ref)
		}
		return data, err
	})
	if err != nil {
		return err
	}
	s.checked(config.Source{Name: name, Git: url, Branch: branch})
	fmt.Fprintf(s.Out, "added source %s (pinned: changes apply after `ctxtra sync`)\n", name)
	if file != "" {
		fmt.Fprintf(s.Out, "added %s\n", ref)
		s.showBindings(filepath.Join(gen, filepath.FromSlash(file)), gen)
	} else {
		s.listBindingFiles(name, gen)
	}
	s.showExecutables(r, commit)
	return nil
}

// addRef adds a binding file of a source that is in the config already.
func (s *sources) addRef(ref string) error {
	cur, err := s.current()
	if err != nil {
		return err
	}
	name, file, _ := strings.Cut(strings.TrimPrefix(ref, config.Scheme), "/")
	src, ok := cur.Source(name)
	switch {
	case !ok:
		return fmt.Errorf("no source named %s in %s", name, paths.Tilde(s.file))
	case file == "":
		return fmt.Errorf("%s: name a binding file in the source", ref)
	case src.Pinned() && src.Commit == "":
		return fmt.Errorf("the source %s has no accepted commit yet; run: ctxtra sync %s", name, name)
	case src.Pinned() && !source.Has(src.Git, src.Commit):
		return fmt.Errorf("%s %s is not in ctxtra's store; run: ctxtra sync --restore", name, source.Short(src.Commit))
	}
	root := src.TrustedPath
	if src.Pinned() {
		root = src.Gen()
	}
	p := filepath.Join(root, filepath.FromSlash(file))
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a file in the source %s", file, name)
	}
	if err := s.addBinding(ref); err != nil {
		return err
	}
	s.showBindings(p, root)
	return nil
}

// addBinding adds entry to bindings unless it is there already.
func (s *sources) addBinding(entry string) error {
	added := false
	err := s.edit(func(old []byte) ([]byte, error) {
		c, err := config.Parse(old)
		if err == nil && slices.Contains(c.BindingEntries, entry) {
			return old, nil
		}
		added = true
		return config.AddBinding(old, entry)
	})
	switch {
	case err != nil:
		return err
	case added:
		fmt.Fprintf(s.Out, "added %s\n", entry)
	default:
		fmt.Fprintf(s.Out, "%s is in bindings already\n", entry)
	}
	return nil
}

// addLocal adds a binding file on disk, or with --trust a folder or the
// repository of a file as a trusted-path source.
func (s *sources) addLocal(p string, trustIt bool, name string) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	cfg := config.Load(s.file, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
	cur, err := s.current()
	if err != nil {
		return err
	}
	if st.IsDir() {
		if !trustIt {
			return fmt.Errorf("%s is a folder: add a binding file, or trust the folder with --trust", paths.Tilde(p))
		}
		_, err := s.addTrusted(cur, p, name)
		return err
	}
	terr := cfg.Trust.Check(p)
	var te *trust.Error
	switch {
	case terr == nil:
		entry := paths.Tilde(p)
		if filepath.Dir(p) == filepath.Dir(paths.Real(s.file)) || filepath.Dir(p) == filepath.Dir(s.file) {
			entry = filepath.Base(p)
		}
		if err := s.addBinding(entry); err != nil {
			return err
		}
		fmt.Fprintln(s.Out, "  trusted: changes apply right away")
	case trustIt && errors.As(terr, &te) && te.Err == nil && te.Via == "":
		src, err := s.addTrusted(cur, te.Repo, name)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(te.Repo, p)
		if err := s.addBinding(config.Scheme + src + "/" + filepath.ToSlash(rel)); err != nil {
			return err
		}
		// Another repository around the file may still be untrusted.
		after := config.Load(s.file, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
		if err := after.Trust.Check(p); err != nil {
			fmt.Fprintf(s.Out, "WARNING: %v\n", err)
		}
	case trustIt:
		return terr
	default:
		return fmt.Errorf("%v\nIf the repository is yours: ctxtra add --trust %s\nTo pin it instead, add it by its URL: ctxtra add <git URL> <path in the repository>",
			terr, paths.Tilde(p))
	}
	s.showBindings(p, "")
	return nil
}

// addTrusted adds the folder dir as a trusted-path source and returns its
// name.
func (s *sources) addTrusted(cur *config.Config, dir, name string) (string, error) {
	for _, src := range cur.Sources {
		if src.TrustedPath != "" && sameFile(src.TrustedPath, dir) {
			fmt.Fprintf(s.Out, "%s is the trusted source %s already\n", paths.Tilde(dir), src.Name)
			return src.Name, nil
		}
	}
	if name == "" {
		name = defaultName(dir)
	}
	if err := checkName(cur, name, paths.Tilde(dir)); err != nil {
		return "", err
	}
	err := s.edit(func(old []byte) ([]byte, error) {
		return config.AddSource(old, name, [][2]string{{"trusted-path", paths.Tilde(dir)}})
	})
	if err != nil {
		return "", err
	}
	fmt.Fprintf(s.Out, "added source %s (trusted-path: %s); changes apply right away\n", name, paths.Tilde(dir))
	return name, nil
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

// showBindings prints the bindings of a binding file and its problems, as
// the config now loads it. Paths under root are shown relative to it.
func (s *sources) showBindings(file, root string) {
	cfg := config.Load(s.file, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
	show := func(p string) string {
		if root != "" {
			if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
		}
		return paths.Tilde(p)
	}
	for _, b := range cfg.Bindings {
		if b.Source != file {
			continue
		}
		fmt.Fprintf(s.Out, "  binding %s: for %s\n", b.Name, b.Selector)
		if len(b.Files) > 0 {
			fmt.Fprintf(s.Out, "    files: %s\n", source.Sanitize(strings.Join(mapList(b.Files, show), ", ")))
		}
		if len(b.Skills) > 0 {
			fmt.Fprintf(s.Out, "    skills: %s\n", source.Sanitize(strings.Join(mapList(b.Skills, show), ", ")))
		}
	}
	prefix := paths.Tilde(file) + ": "
	for _, p := range cfg.Problems {
		if strings.HasPrefix(p, prefix) {
			fmt.Fprintf(s.Out, "  ERROR: %s\n", source.Sanitize(strings.TrimPrefix(p, prefix)))
		}
	}
}

func mapList(l []string, f func(string) string) []string {
	out := make([]string, len(l))
	for i, x := range l {
		out[i] = f(x)
	}
	return out
}

// listBindingFiles lists the YAML files of a generation that hold bindings.
func (s *sources) listBindingFiles(name, gen string) {
	var found []string
	filepath.WalkDir(gen, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")) {
			return nil
		}
		if b, err := fsutil.ReadBounded(p, config.MaxFileBytes); err == nil && reBindingKind.Match(b) {
			rel, _ := filepath.Rel(gen, p)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if len(found) == 0 {
		fmt.Fprintf(s.Out, "no binding files in the source; use its files as ctxtra://%s/<path> in your bindings\n", name)
		return
	}
	fmt.Fprintf(s.Out, "binding files in the source (add one with: ctxtra add ctxtra://%s/<file>):\n", name)
	for _, f := range found {
		fmt.Fprintf(s.Out, "  %s\n", source.Sanitize(f))
	}
}

// showExecutables lists the executable files of a commit, and what the
// store leaves out.
func (s *sources) showExecutables(r *source.Repo, commit string) {
	entries, skipped, err := r.Tree(s.ctx, commit)
	if err != nil {
		return
	}
	var exec []string
	for _, e := range entries {
		if e.Exec {
			exec = append(exec, e.Path)
		}
	}
	if len(exec) > 0 {
		fmt.Fprintf(s.Out, "executable files: %s\n", source.Sanitize(strings.Join(exec, ", ")))
	}
	for _, sk := range skipped {
		fmt.Fprintf(s.Out, "left out (%s): %s\n", sk.Kind, source.Sanitize(sk.Path))
	}
}

const syncUsage = "usage: ctxtra sync [--full] [SOURCE]...\n       ctxtra sync --restore [SOURCE]...\n"

func runSync(args []string, stdio IO) int {
	f, names, err := parseArgs(args, nil, []string{"restore", "full"})
	if err != nil || f["restore"] != "" && f["full"] != "" {
		if err != nil {
			fmt.Fprintf(stdio.Err, "ctxtra sync: %v\n", err)
		}
		fmt.Fprint(stdio.Err, syncUsage)
		return 2
	}
	s := newSources(stdio)
	s.full = f["full"] != ""
	cur, err := s.current()
	if err != nil {
		return s.fail("sync", err)
	}
	var todo []config.Source
	for _, n := range names {
		src, ok := cur.Source(n)
		switch {
		case !ok:
			return s.fail("sync", fmt.Errorf("no source named %s", n))
		case !src.Pinned():
			return s.fail("sync", fmt.Errorf("%s is a trusted-path source: it is read from disk and needs no sync", n))
		}
		todo = append(todo, src)
	}
	if len(names) == 0 {
		for _, src := range cur.Sources {
			if src.Pinned() {
				todo = append(todo, src)
			}
		}
	}
	if len(todo) == 0 {
		fmt.Fprintln(s.Out, "no pinned sources")
		return 0
	}
	code := 0
	for _, src := range todo {
		if err := s.sync(src, f["restore"] != ""); err != nil {
			fmt.Fprintf(s.Err, "%s: %v\n", src.Name, err)
			code = 1
		}
	}
	return code
}

// sync brings one source's accepted commit into the store and, unless
// restore, reviews and accepts the branch's new commit.
func (s *sources) sync(src config.Source, restore bool) error {
	r := source.Open(src.Git)
	if src.Commit == "" {
		if restore {
			return fmt.Errorf("no accepted commit yet; run: ctxtra sync %s", src.Name)
		}
		return s.first(src, r)
	}
	if !source.Has(src.Git, src.Commit) {
		if err := r.FetchCommit(s.ctx, src.Commit); err != nil {
			return err
		}
		if _, err := r.Materialize(s.ctx, src.Commit); err != nil {
			return err
		}
		fmt.Fprintf(s.Out, "%s: fetched %s (the commit in config.yaml)\n", src.Name, source.Short(src.Commit))
	} else if restore {
		fmt.Fprintf(s.Out, "%s: %s is in the store\n", src.Name, source.Short(src.Commit))
	}
	if restore {
		return nil
	}
	commit, err := r.FetchBranch(s.ctx, src.Branch)
	if err != nil {
		return err
	}
	s.checked(src)
	if commit == src.Commit {
		// The branch is back at the accepted commit: nothing is dismissed.
		s.record(src, func(e *state.Source) { e.Dismissed = "" })
		fmt.Fprintf(s.Out, "%s: up to date (%s at %s)\n", src.Name, src.Branch, source.Short(commit))
		return nil
	}
	// The new commit goes to the store before review: a commit over the
	// limits fails here, and the review reads its binding files.
	if _, err := r.Materialize(s.ctx, commit); err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "%s: %s -> %s (%s, %s)\n", src.Name, source.Short(src.Commit), source.Short(commit), src.Branch, src.Git)
	if !r.IsAncestor(s.ctx, src.Commit, commit) {
		fmt.Fprintf(s.Out, "WARNING: %s doesn't continue %s: the branch was force-pushed, or %s was never pushed\n",
			source.Short(commit), source.Short(src.Commit), source.Short(src.Commit))
	}
	if err := s.review(r, src, commit, s.full); err != nil {
		return err
	}
	fmt.Fprint(s.Out, "Accept? [y/N] ")
	answer, _ := s.in.ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		fmt.Fprintf(s.Out, "not accepted; you keep %s\n", source.Short(src.Commit))
		s.record(src, func(e *state.Source) { e.Dismissed = commit })
		return nil
	}
	if err := s.setCommit(src, commit); err != nil {
		return err
	}
	s.record(src, func(e *state.Source) { e.Dismissed = "" })
	fmt.Fprintf(s.Out, "accepted %s %s (commit: updated in %s)\n", src.Name, source.Short(commit), filepath.Base(s.file))
	return nil
}

// record changes the local state of src. The state only feeds notices, so
// an error is a warning.
func (s *sources) record(src config.Source, change func(*state.Source)) {
	if err := state.Update(source.ID(src.Git), src.Git, src.Branch, change); err != nil {
		fmt.Fprintf(s.Err, "warning: can't write %s: %v\n", paths.Tilde(state.Path()), err)
	}
}

// checked records a fetch of the branch that worked.
func (s *sources) checked(src config.Source) {
	s.record(src, func(e *state.Source) { e.LastCheck = time.Now() })
}

// first accepts the first commit of a source written by hand without one,
// as add does: without review.
func (s *sources) first(src config.Source, r *source.Repo) error {
	commit, err := r.FetchBranch(s.ctx, src.Branch)
	if err != nil {
		return err
	}
	s.checked(src)
	if _, err := r.Materialize(s.ctx, commit); err != nil {
		return err
	}
	if err := s.setCommit(src, commit); err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "%s: accepted %s at %s, the first version (%s)\n", src.Name, src.Branch, source.Short(commit), src.Git)
	s.showExecutables(r, commit)
	return nil
}

// setCommit writes commit to the source in the config, unless the source
// changed in the file since it was read: then what was reviewed may not be
// what the config now names.
func (s *sources) setCommit(src config.Source, commit string) error {
	return s.edit(func(old []byte) ([]byte, error) {
		c, err := config.Parse(old)
		if err != nil {
			return nil, err
		}
		now, ok := c.Source(src.Name)
		if !ok || now.Git != src.Git || now.Branch != src.Branch || now.Commit != src.Commit {
			return nil, fmt.Errorf("the source %s changed in %s while ctxtra worked; nothing accepted, run ctxtra sync again",
				src.Name, paths.Tilde(s.file))
		}
		return config.SetCommit(old, src.Name, commit)
	})
}
