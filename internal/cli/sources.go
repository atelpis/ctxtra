package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/atelpis/ctxtra/internal/term"
	"github.com/atelpis/ctxtra/internal/trust"
)

// sources implements the source and binding-file commands: the ones that
// change sources and the include list.
type sources struct {
	IO
	ctx    context.Context
	file   string // the config
	in     *bufio.Reader
	full   bool // source update --full: show every changed file in full
	prompt bool // source update may ask whether to accept a commit
	// registerErr is set when the config couldn't be registered.
	registerErr error
}

func newSources(stdio IO) *sources {
	return &sources{IO: stdio, ctx: context.Background(), file: paths.ConfigPath(), in: bufio.NewReader(stdio.In)}
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
	created, err := s.change(fn)
	if created {
		term.Fprintf(s.Out, term.Green, "created %s\n", paths.Tilde(s.file))
	}
	return err
}

// change changes the config, and reports whether it created it. The config
// is registered, so that cleaning the store keeps its accepted commits.
func (s *sources) change(fn func(old []byte) ([]byte, error)) (created bool, err error) {
	existed := source.Exists(s.file)
	if err := config.Edit(s.file, fn); err != nil {
		return false, err
	}
	s.register()
	return !existed, nil
}

// register records the config in configs.json, so that cleaning the store
// keeps its accepted commits. Without it, source update doesn't clean.
func (s *sources) register() {
	abs, err := filepath.Abs(s.file)
	if err == nil {
		// Under the config lock, which cleanStore holds while it reads the
		// list: a config registered meanwhile is never missed.
		var unlock func()
		if unlock, err = fsutil.Lock(filepath.Join(paths.StateDir(), "config.lock")); err == nil {
			err = state.RecordConfig(abs)
			unlock()
		}
	}
	if err != nil && s.registerErr == nil {
		s.registerErr = err
		fmt.Fprintf(s.Err, "warning: can't record %s in %s (%v); the store is not cleaned\n",
			paths.Tilde(s.file), paths.Tilde(state.ConfigsPath()), err)
	}
}

func (s *sources) fail(cmd string, err error) int {
	fmt.Fprintf(s.Err, "ctxtra %s: %v\n", cmd, err)
	return 1
}

// result is the RunE result of a command that ended with err.
func (s *sources) result(cmd string, err error) error {
	if err != nil {
		return exitCode(s.fail(cmd, err))
	}
	return nil
}

// addOptions are the options of source add.
type addOptions struct {
	name, branch, remote string
	includes             []string // binding files of the source to include, slash-separated
	dryRun               bool
}

// cleanIncludes checks the --include paths and cleans them, without
// repeats.
func cleanIncludes(in []string) ([]string, error) {
	var out []string
	for _, f := range in {
		f = path.Clean(filepath.ToSlash(f))
		if err := source.CheckPath(f); err != nil {
			return nil, err
		}
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out, nil
}

// cloneDir tells a clone's folder from a URL in the argument of source add
// git. A path that doesn't exist is an error, never tried as a URL.
func cloneDir(arg string) (dir string, isDir bool, err error) {
	wd, _ := os.Getwd()
	p := paths.Resolve(wd, arg)
	st, err := os.Stat(p)
	switch {
	case err == nil && st.IsDir():
		return p, true, nil
	case err == nil:
		return "", false, fmt.Errorf("%s is a file; give a repository URL or the folder of a clone", paths.Tilde(p))
	case strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, "~") || strings.HasPrefix(arg, "."):
		return "", false, err
	}
	if uerr := source.CheckURL(arg); uerr != nil {
		return "", false, fmt.Errorf("%s is not a folder, and %v", arg, uerr)
	}
	return "", false, nil
}

// checkFiles fails unless each of files is a regular file under root;
// where names root in the message.
func checkFiles(root string, files []string, where string, stat func(string) (fs.FileInfo, error)) error {
	for _, f := range files {
		if st, err := stat(filepath.Join(root, filepath.FromSlash(f))); err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("%s is not a file in %s; nothing added", f, where)
		}
	}
	return nil
}

// addSource writes the source name with kv, and an include entry for each
// of files, in one edit.
func (s *sources) addSource(name string, kv [][2]string, files []string) error {
	return s.edit(func(old []byte) ([]byte, error) {
		data, err := config.AddSource(old, name, kv)
		for _, f := range files {
			if err == nil {
				data, err = config.AddInclude(data, config.Scheme+name+"/"+f)
			}
		}
		return data, err
	})
}

// added shows the binding files just included from the source name at
// root, or, without any, the binding files it has.
func (s *sources) added(name, root string, files []string) {
	if len(files) == 0 {
		s.listSourceBindingFiles(name, root)
		return
	}
	for _, f := range files {
		fmt.Fprintf(s.Out, "included %s%s/%s\n", config.Scheme, name, f)
		s.showBindings(filepath.Join(root, filepath.FromSlash(f)), root)
	}
}

// wouldAdd is what a dry run of source add says after the source.
func (s *sources) wouldAdd(name string, files []string) {
	for _, f := range files {
		fmt.Fprintf(s.Out, "would include %s%s/%s\n", config.Scheme, name, f)
	}
	fmt.Fprintln(s.Out, "nothing changed (--dry-run)")
}

// A binding file has the ctxtra schema and none of the config's own keys.
var (
	reSchema    = regexp.MustCompile(`(?m)^schema:\s*['"]?ctxtra/`)
	reConfigKey = regexp.MustCompile(`(?m)^(sources|include|budget)\s*:`)
)

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
		return fmt.Errorf("%q can't be a source name (letters, digits, '.', '_', '-'); choose one with --name", name)
	}
	if old, ok := c.Source(name); ok {
		return fmt.Errorf("a source named %s already exists (%s); choose another name for %s with --name", name, describe(old), what)
	}
	return nil
}

func describe(s config.Source) string {
	switch {
	case s.Git != "":
		return s.Git
	case s.LocalGit != "":
		return "local-git " + paths.Tilde(s.LocalGit)
	}
	return "trusted-path " + paths.Tilde(s.TrustedPath)
}

// kind is the config key of the source's kind.
func kind(s config.Source) string {
	switch {
	case s.Git != "":
		return "git"
	case s.LocalGit != "":
		return "local-git"
	}
	return "trusted-path"
}

// addGitURL adds the repository at url as a pinned source.
func (s *sources) addGitURL(url string, o addOptions) error {
	if err := source.CheckURL(url); err != nil {
		return err
	}
	cur, err := s.current()
	if err != nil {
		return err
	}
	for _, src := range cur.Sources {
		if src.Git == url {
			return fmt.Errorf("%s is the source %s already; to include one of its binding files: ctxtra binding-file add %s%s/PATH",
				url, src.Name, config.Scheme, src.Name)
		}
	}
	name := o.name
	if name == "" {
		name = defaultName(url)
	}
	if err := checkName(cur, name, url); err != nil {
		return err
	}
	if o.dryRun {
		branch := o.branch
		if branch == "" {
			branch = "its default branch"
		}
		fmt.Fprintf(s.Out, "would add source %s: %s, %s, pinned to the commit fetched then (not fetched now)\n", name, url, branch)
		s.wouldAdd(name, o.includes)
		return nil
	}
	r := source.Open(url)
	branch := o.branch
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
	if err := checkFiles(gen, o.includes, url, os.Lstat); err != nil {
		return err
	}
	if err := s.addSource(name, [][2]string{{"git", url}, {"branch", branch}, {"commit", commit}}, o.includes); err != nil {
		return err
	}
	s.checked(config.Source{Name: name, Git: url, Branch: branch})
	fmt.Fprintf(s.Out, "added source %s (pinned: new commits apply after you accept them in ctxtra source update)\n", name)
	s.added(name, gen, o.includes)
	s.showExecutables(r, commit)
	return nil
}

// addGitClone adds the repository that dir is a clone of as a git source:
// the clone's remote and upstream branch, starting at the clone's committed
// HEAD. Plain paths under the clone are then served from the source (a
// mapping in the local state), also after the clone moves or is deleted.
// Later updates follow the remote branch, not the clone.
func (s *sources) addGitClone(dir string, o addOptions) error {
	c, err := source.InspectClone(s.ctx, dir)
	if err != nil {
		return err
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for n := range c.Remotes {
			if !yield(n) {
				return
			}
		}
	})
	remote, branch := o.remote, o.branch
	switch {
	case remote != "":
		if c.Remotes[remote] == "" {
			return fmt.Errorf("%s has no remote %q (remotes: %s)", paths.Tilde(c.Top), remote, strings.Join(names, ", "))
		}
	case len(names) == 1:
		remote = names[0]
	case len(names) == 0:
		return fmt.Errorf("%s has no remote; to read it from disk: ctxtra source add trusted-path %s, or to follow its commits: ctxtra source add local-git %s",
			paths.Tilde(c.Top), paths.Tilde(c.Top), paths.Tilde(c.Top))
	default:
		return fmt.Errorf("%s has several remotes (%s); choose one with --remote", paths.Tilde(c.Top), strings.Join(names, ", "))
	}
	if branch == "" {
		if c.Upstream != remote || c.Branch == "" {
			return fmt.Errorf("the current branch of %s has no upstream on %s; choose the branch to follow with --branch", paths.Tilde(c.Top), remote)
		}
		branch = c.Branch
	}
	url := c.Remotes[remote]
	if err := source.CheckURL(url); err != nil {
		return fmt.Errorf("the remote %s of %s: %w", remote, paths.Tilde(c.Top), err)
	}
	if err := source.CheckBranch(branch); err != nil {
		return err
	}
	cur, err := s.current()
	if err != nil {
		return err
	}
	// A source with the URL is there already: map the clone to it and use
	// its commit; it never moves.
	var same []config.Source
	for _, src := range cur.Sources {
		if src.Git == url {
			same = append(same, src)
		}
	}
	if len(same) > 0 {
		return s.mapToSource(same, c.Top, url, o)
	}
	name := o.name
	if name == "" {
		name = defaultName(url)
	}
	if err := checkName(cur, name, url); err != nil {
		return err
	}
	if o.dryRun {
		fmt.Fprintf(s.Out, "would add source %s: %s, %s/%s, starting at the committed HEAD of %s\n", name, url, remote, branch, paths.Tilde(c.Top))
		s.wouldAdd(name, o.includes)
		return nil
	}
	r := source.Open(url)
	commit, err := r.FetchHead(s.ctx, c.Top)
	if err != nil {
		return err
	}
	gen, err := r.Materialize(s.ctx, commit)
	if err != nil {
		return err
	}
	for _, f := range o.includes {
		if st, err := os.Lstat(filepath.Join(gen, filepath.FromSlash(f))); err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("%s is not committed in %s; ctxtra uses only committed files", f, paths.Tilde(c.Top))
		}
	}
	// After the fetch: the mapping and the config change together.
	unlock, err := transition()
	if err != nil {
		return err
	}
	defer unlock()
	mapped, err := state.Map(c.Top, name, url)
	if err != nil {
		return err
	}
	if err := s.addSource(name, [][2]string{{"git", url}, {"branch", branch}, {"commit", commit}}, o.includes); err != nil {
		if mapped {
			state.Unmap(c.Top)
		}
		return err
	}
	fmt.Fprintf(s.Out, "%s is a clone of %s, added as source %s (pinned)\n", paths.Tilde(c.Top), url, name)
	fmt.Fprintf(s.Out, "starts at your local commit %s; later updates follow %s/%s (remote not checked yet)\n", source.Short(commit), remote, branch)
	s.added(name, gen, o.includes)
	s.showExecutables(r, commit)
	fmt.Fprintf(s.Out, "if the repository is yours, read it from disk instead: ctxtra source trust %s\n", name)
	return nil
}

// mapToSource maps the clone top to the one source in same, which has the
// clone's URL, and includes the binding files of o from it, at the source's
// commit.
func (s *sources) mapToSource(same []config.Source, top, url string, o addOptions) error {
	if len(same) > 1 {
		return fmt.Errorf("several sources use %s; reference its files as ctxtra://SOURCE/PATH", url)
	}
	src := same[0]
	if _, err := checkIncludes(src, o.includes); err != nil {
		return err
	}
	if o.dryRun {
		fmt.Fprintf(s.Out, "would map %s to the source %s, which has its URL\n", paths.Tilde(top), src.Name)
		if err := s.includeFrom(src, o.includes, true); err != nil {
			return err
		}
		fmt.Fprintln(s.Out, "nothing changed (--dry-run)")
		return nil
	}
	unlock, err := transition()
	if err != nil {
		return err
	}
	defer unlock()
	mapped, err := state.Map(top, src.Name, url)
	if err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "%s is a clone of %s, the source %s: its paths are read from the source's commit\n", paths.Tilde(top), url, src.Name)
	if len(o.includes) > 0 {
		if err := s.includeFrom(src, o.includes, false); err != nil {
			if mapped {
				state.Unmap(top)
			}
			return err
		}
	}
	return nil
}

// addLocalGit adds a clone as a local-git source: pinned like a git source,
// but updates follow the clone's committed HEAD instead of a remote. It
// adds no mapping: its files are referenced as ctxtra://NAME/PATH.
func (s *sources) addLocalGit(dir string, o addOptions) error {
	c, err := source.InspectClone(s.ctx, dir)
	if err != nil {
		return err
	}
	cur, err := s.current()
	if err != nil {
		return err
	}
	for _, src := range cur.Sources {
		if src.LocalGit != "" && sameFile(src.LocalGit, c.Top) {
			return fmt.Errorf("%s is the source %s already", paths.Tilde(c.Top), src.Name)
		}
	}
	name := o.name
	if name == "" {
		name = defaultName(c.Top)
	}
	if err := checkName(cur, name, paths.Tilde(c.Top)); err != nil {
		return err
	}
	if o.dryRun {
		fmt.Fprintf(s.Out, "would add source %s: local-git %s, starting at its committed HEAD\n", name, paths.Tilde(c.Top))
		s.wouldAdd(name, o.includes)
		return nil
	}
	r := source.Open(c.Top)
	commit, err := r.FetchHead(s.ctx, c.Top)
	if err != nil {
		return err
	}
	gen, err := r.Materialize(s.ctx, commit)
	if err != nil {
		return err
	}
	if err := checkFiles(gen, o.includes, "the committed files of "+paths.Tilde(c.Top), os.Lstat); err != nil {
		return err
	}
	if err := s.addSource(name, [][2]string{{"local-git", paths.Tilde(c.Top)}, {"commit", commit}}, o.includes); err != nil {
		return err
	}
	s.checked(config.Source{Name: name, LocalGit: c.Top})
	fmt.Fprintf(s.Out, "added source %s (local-git: %s at %s); updates follow the clone's committed HEAD\n",
		name, paths.Tilde(c.Top), source.Short(commit))
	s.added(name, gen, o.includes)
	return nil
}

// addTrustedPath adds the folder dir as a trusted-path source.
func (s *sources) addTrustedPath(dir string, o addOptions) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", paths.Tilde(dir))
	}
	if err := checkFiles(dir, o.includes, paths.Tilde(dir), os.Stat); err != nil {
		return err
	}
	cur, err := s.current()
	if err != nil {
		return err
	}
	for _, src := range cur.Sources {
		if src.TrustedPath != "" && sameFile(src.TrustedPath, dir) {
			fmt.Fprintf(s.Out, "%s is the trusted source %s already\n", paths.Tilde(dir), src.Name)
			if len(o.includes) == 0 {
				return nil
			}
			return s.includeFrom(src, o.includes, o.dryRun)
		}
	}
	name := o.name
	if name == "" {
		name = defaultName(dir)
	}
	if err := checkName(cur, name, paths.Tilde(dir)); err != nil {
		return err
	}
	if o.dryRun {
		fmt.Fprintf(s.Out, "would add source %s: trusted-path %s, read from disk\n", name, paths.Tilde(dir))
		s.wouldAdd(name, o.includes)
		return nil
	}
	if err := s.addSource(name, [][2]string{{"trusted-path", paths.Tilde(dir)}}, o.includes); err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "added source %s (trusted-path: %s); changes apply right away\n", name, paths.Tilde(dir))
	ms, _ := state.LoadMappings()
	if m, _, ok := ms.Lookup(paths.Real(dir)); ok {
		if _, ok := cur.Source(m.Source); ok {
			fmt.Fprintf(s.Out, "note: %s/... paths in your bindings were served from the pinned source %s and are now read from disk\n",
				paths.Tilde(m.Path), m.Source)
		}
	}
	s.added(name, dir, o.includes)
	// Another repository inside the folder may still be untrusted.
	after := config.Load(s.file, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
	for _, f := range o.includes {
		if err := after.Trust.Check(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			fmt.Fprintf(s.Err, "warning: %v\n", err)
		}
	}
	return nil
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

// addBindingFile includes a binding file: on disk, in a trusted place or a
// clone mapped to a source, or ctxtra://SOURCE/PATH. It never adds, pins or
// trusts a source.
func (s *sources) addBindingFile(target string, dryRun bool) error {
	if strings.HasPrefix(target, config.Scheme) {
		return s.addRef(target, dryRun)
	}
	wd, _ := os.Getwd()
	p := paths.Resolve(wd, target)
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a folder; name a binding file", paths.Tilde(p))
	}
	cur, err := s.current()
	if err != nil {
		return err
	}
	ms, err := state.LoadMappings()
	if err != nil {
		return err
	}
	if m, rel, ok := ms.Lookup(paths.Real(p)); ok {
		i := slices.IndexFunc(cur.Sources, func(src config.Source) bool { return src.Name == m.Source && src.Git == m.URL })
		if i < 0 {
			return fmt.Errorf("%s was added as the source %s, which is not in the config any more; add it again: ctxtra source add git %s",
				paths.Tilde(m.Path), m.Source, paths.Tilde(m.Path))
		}
		return s.addRef(config.Scheme+cur.Sources[i].Name+"/"+rel, dryRun)
	}
	cfg := config.Load(s.file, fsutil.NewReader(config.MaxFileBytes, config.MaxSessionBytes))
	terr := cfg.Trust.Check(p)
	var te *trust.Error
	switch {
	case terr == nil && sameFile(p, s.file):
		return fmt.Errorf("%s is the config itself; its bindings apply already", paths.Tilde(p))
	case terr == nil:
		entry := paths.Tilde(p)
		if filepath.Dir(p) == filepath.Dir(paths.Real(s.file)) || filepath.Dir(p) == filepath.Dir(s.file) {
			entry = filepath.Base(p)
		}
		if err := s.addInclude(entry, dryRun); err != nil || dryRun {
			return err
		}
		fmt.Fprintln(s.Out, "  trusted: changes apply right away")
		s.showBindings(p, "")
		return nil
	case errors.As(terr, &te) && te.Err == nil && te.Via == "":
		repo, rel := paths.Tilde(te.Repo), filepath.ToSlash(must(filepath.Rel(te.Repo, p)))
		return fmt.Errorf(`%s is in %s, which isn't a source. Add the repository first:
  ctxtra source add git %s --include %s
      pinned: changes apply after you review them
  ctxtra source add trusted-path %s --include %s
      your own repository: read from disk, changes apply right away`, paths.Tilde(p), repo, repo, rel, repo, rel)
	default:
		return terr
	}
}

// must is v; the error is impossible.
func must[T any](v T, _ error) T { return v }

// addRef includes a binding file of a source that is in the config already.
func (s *sources) addRef(ref string, dryRun bool) error {
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
	}
	files, err := cleanIncludes([]string{file})
	if err != nil {
		return fmt.Errorf("%s: %w", ref, err)
	}
	return s.includeFrom(src, files, dryRun)
}

// sourceRoot is the folder that holds the files of src: its trusted folder,
// or its accepted commit in the store.
func sourceRoot(src config.Source) (string, error) {
	switch {
	case !src.Pinned():
		return src.TrustedPath, nil
	case src.Commit == "":
		return "", fmt.Errorf("the source %s has no accepted commit yet; run: ctxtra source update %s", src.Name, src.Name)
	case !source.Has(src.Remote(), src.Commit):
		return "", fmt.Errorf("%s %s is not in ctxtra's store; run: ctxtra source restore %s", src.Name, source.Short(src.Commit), src.Name)
	}
	return src.Gen(), nil
}

// checkIncludes fails unless each of files is a binding file that src has.
func checkIncludes(src config.Source, files []string) (string, error) {
	root, err := sourceRoot(src)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil || !st.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a file in the source %s; nothing added", f, src.Name)
		}
	}
	return root, nil
}

// includeFrom includes the binding files of src, all in one edit, or none
// when one of them isn't there.
func (s *sources) includeFrom(src config.Source, files []string, dryRun bool) error {
	root, err := checkIncludes(src, files)
	if err != nil {
		return err
	}
	var entries []string
	for _, f := range files {
		entries = append(entries, config.Scheme+src.Name+"/"+f)
	}
	if err := s.addIncludes(entries, dryRun); err != nil || dryRun {
		return err
	}
	for _, f := range files {
		s.showBindings(filepath.Join(root, filepath.FromSlash(f)), root)
	}
	return nil
}

// addInclude adds entry to include unless it is there already.
func (s *sources) addInclude(entry string, dryRun bool) error {
	return s.addIncludes([]string{entry}, dryRun)
}

// addIncludes adds the entries to include that aren't there already, in one
// edit.
func (s *sources) addIncludes(entries []string, dryRun bool) error {
	have := func(c *config.Config) map[string]bool {
		m := map[string]bool{}
		for _, e := range entries {
			m[e] = slices.Contains(c.Includes, e)
		}
		return m
	}
	var there map[string]bool
	if dryRun {
		cur, err := s.current()
		if err != nil {
			return err
		}
		there = have(cur)
	} else {
		err := s.edit(func(old []byte) ([]byte, error) {
			c, err := config.Parse(old)
			if err != nil {
				return nil, err
			}
			there = have(c)
			data := old
			for _, e := range entries {
				if err == nil && !there[e] {
					data, err = config.AddInclude(data, e)
				}
			}
			return data, err
		})
		if err != nil {
			return err
		}
	}
	for _, e := range entries {
		switch {
		case there[e]:
			fmt.Fprintf(s.Out, "%s is included already\n", e)
		case dryRun:
			fmt.Fprintf(s.Out, "would include %s\n", e)
		default:
			fmt.Fprintf(s.Out, "included %s\n", e)
		}
	}
	return nil
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
		if len(b.Instructions) > 0 {
			fmt.Fprintf(s.Out, "    instructions: %s\n", source.Sanitize(strings.Join(mapList(b.Instructions, show), ", ")))
		}
		if len(b.Skills) > 0 {
			fmt.Fprintf(s.Out, "    skills: %s\n", source.Sanitize(strings.Join(mapList(b.Skills, show), ", ")))
		}
	}
	prefix := paths.Tilde(file) + ": "
	for _, p := range cfg.Problems {
		if strings.HasPrefix(p, prefix) {
			fmt.Fprintf(s.Out, "  error: %s\n", source.Sanitize(strings.TrimPrefix(p, prefix)))
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

// listSourceBindingFiles lists the YAML files of a source at root that hold
// bindings.
func (s *sources) listSourceBindingFiles(name, root string) {
	var found []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")) {
			return nil
		}
		if b, err := fsutil.ReadBounded(p, config.MaxFileBytes); err == nil && reSchema.Match(b) && !reConfigKey.Match(b) {
			rel, _ := filepath.Rel(root, p)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if len(found) == 0 {
		fmt.Fprintf(s.Out, "no binding files in the source; use its files as ctxtra://%s/PATH in your bindings\n", name)
		return
	}
	fmt.Fprintf(s.Out, "binding files in the source (include one with: ctxtra binding-file add ctxtra://%s/PATH):\n", name)
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

// syncMode is what update does with a source.
type syncMode int

const (
	accept    syncMode = iota // review the new commit and ask whether to accept it
	checkOnly                 // review it; accept and dismiss nothing
	restore                   // fetch the accepted commit only
)

// updateResult is one source in the output of source update --check --json.
type updateResult struct {
	Name     string `json:"name"`
	Accepted string `json:"accepted,omitempty"`
	Latest   string `json:"latest,omitempty"`
	Pending  bool   `json:"pending"`
	Error    string `json:"error,omitempty"`
}

// update is source update and source restore for the pinned sources names,
// or all of them.
func (s *sources) update(names []string, mode syncMode, asJSON bool) int {
	cmd := "source update"
	if mode == restore {
		cmd = "source restore"
	}
	cur, err := s.current()
	if err != nil {
		return s.fail(cmd, err)
	}
	var todo []config.Source
	for _, n := range names {
		src, ok := cur.Source(n)
		switch {
		case !ok:
			return s.fail(cmd, fmt.Errorf("no source named %s", n))
		case !src.Pinned():
			return s.fail(cmd, fmt.Errorf("%s is a trusted-path source: it is read from disk and has no commits to fetch", n))
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
	if mode == accept && len(todo) > 0 && !s.prompt {
		return s.fail(cmd, errors.New("accepting a new commit needs a terminal (or leave out --no-input); to only look for new commits: ctxtra source update --check"))
	}
	out := s.Out
	if asJSON {
		s.Out = io.Discard
	}
	var results []updateResult
	if len(todo) == 0 {
		fmt.Fprintln(s.Out, "no pinned sources")
	} else {
		s.register()
	}
	n := 0
	for _, src := range todo {
		latest, err := s.sync(src, mode)
		res := updateResult{Name: src.Name, Accepted: src.Commit, Latest: latest, Pending: latest != "" && latest != src.Commit}
		if err != nil {
			fmt.Fprintf(s.Err, "%s: %v\n", src.Name, err)
			res.Error = err.Error()
			n = 1
		}
		results = append(results, res)
	}
	if len(todo) > 0 {
		s.cleanStore()
	}
	if asJSON {
		if results == nil {
			results = []updateResult{}
		}
		writeJSON(out, results, nil)
	}
	return n
}

// sync brings one source's accepted commit into the store and, unless
// restore, reviews the branch's new commit and, in accept mode, asks
// whether to accept it. It returns the newest commit it found.
func (s *sources) sync(src config.Source, mode syncMode) (string, error) {
	r := source.Open(src.Remote())
	if src.Commit == "" {
		if mode == restore {
			return "", fmt.Errorf("no accepted commit yet; to accept one, run in a terminal: ctxtra source update %s", src.Name)
		}
		return s.first(src, r, mode)
	}
	if !source.Has(src.Remote(), src.Commit) {
		if err := r.FetchCommit(s.ctx, src.Commit); err != nil {
			return "", err
		}
		if _, err := r.Materialize(s.ctx, src.Commit); err != nil {
			return "", err
		}
		fmt.Fprintf(s.Out, "%s: fetched %s (the commit in %s)\n", src.Name, source.Short(src.Commit), filepath.Base(s.file))
	} else if mode == restore {
		fmt.Fprintf(s.Out, "%s: %s is in the store\n", src.Name, source.Short(src.Commit))
	}
	if mode == restore {
		return src.Commit, nil
	}
	commit, err := s.latest(r, src)
	if err != nil {
		return "", err
	}
	s.checked(src)
	if commit == src.Commit {
		if mode == accept {
			// The branch is back at the accepted commit: nothing is dismissed.
			s.record(src, func(e *state.Source) { e.Dismissed = "" })
		}
		fmt.Fprintf(s.Out, "%s: up to date (%s at %s)\n", src.Name, follows(src), source.Short(commit))
		return commit, nil
	}
	// The new commit goes to the store before review: a commit over the
	// limits fails here, and the review reads its binding files.
	if _, err := r.Materialize(s.ctx, commit); err != nil {
		return "", err
	}
	fmt.Fprintf(s.Out, "%s: %s -> %s (%s)\n", src.Name, source.Short(src.Commit), source.Short(commit), where(src))
	if !r.IsAncestor(s.ctx, src.Commit, commit) {
		fmt.Fprintf(s.Out, "warning: %s doesn't continue %s: the branch was force-pushed, or %s was never pushed\n",
			source.Short(commit), source.Short(src.Commit), source.Short(src.Commit))
	}
	if err := s.review(r, src, commit, s.full); err != nil {
		return "", err
	}
	if mode == checkOnly {
		fmt.Fprintf(s.Out, "%s: not accepted (--check); to accept it, run in a terminal: ctxtra source update %s\n", src.Name, src.Name)
		return commit, nil
	}
	ok, err := s.ask("Accept? [y/N] ")
	if err != nil {
		return commit, err
	}
	if !ok {
		fmt.Fprintf(s.Out, "not accepted; you keep %s\n", source.Short(src.Commit))
		s.record(src, func(e *state.Source) { e.Dismissed = commit })
		return commit, nil
	}
	if err := s.setCommit(src, commit); err != nil {
		return commit, err
	}
	s.record(src, func(e *state.Source) { e.Dismissed = "" })
	fmt.Fprintf(s.Out, "accepted %s %s (commit: updated in %s)\n", src.Name, source.Short(commit), filepath.Base(s.file))
	return commit, nil
}

// ask asks a yes/no question on stderr; the answer is no unless it is y or
// yes. Input that ends before the end of a line decides nothing: an error.
func (s *sources) ask(q string) (bool, error) {
	fmt.Fprint(s.Err, q)
	answer, err := s.in.ReadString('\n')
	if err != nil {
		fmt.Fprintln(s.Err)
		return false, errors.New("no answer; nothing accepted or dismissed")
	}
	a := strings.ToLower(strings.TrimSpace(answer))
	return a == "y" || a == "yes", nil
}

// latest fetches the commit that src follows: the branch of a git source,
// the committed HEAD of a local-git source's clone.
func (s *sources) latest(r *source.Repo, src config.Source) (string, error) {
	if src.LocalGit != "" {
		return r.FetchHead(s.ctx, src.LocalGit)
	}
	return r.FetchBranch(s.ctx, src.Branch)
}

// follows is what src follows, for messages.
func follows(src config.Source) string {
	if src.LocalGit != "" {
		return "local HEAD"
	}
	return src.Branch
}

// where is what src follows and where, for messages.
func where(src config.Source) string {
	if src.LocalGit != "" {
		return "local HEAD, " + paths.Tilde(src.LocalGit)
	}
	return src.Branch + ", " + src.Git
}

// record changes the local state of src. The state only feeds notices, so
// an error is a warning.
func (s *sources) record(src config.Source, change func(*state.Source)) {
	if err := state.Update(source.ID(src.Remote()), src.Remote(), src.Branch, change); err != nil {
		fmt.Fprintf(s.Err, "warning: can't write %s: %v\n", paths.Tilde(state.Path()), err)
	}
}

// checked records a fetch of the branch that worked.
func (s *sources) checked(src config.Source) {
	s.record(src, func(e *state.Source) { e.LastCheck = time.Now() })
}

// first offers the first commit of a source written by hand without one,
// reviewed as a change from nothing: the files the config would use from it
// in full, its bindings' match blocks, and its problems.
func (s *sources) first(src config.Source, r *source.Repo, mode syncMode) (string, error) {
	commit, err := s.latest(r, src)
	if err != nil {
		return "", err
	}
	s.checked(src)
	if _, err := r.Materialize(s.ctx, commit); err != nil {
		return "", err
	}
	fmt.Fprintf(s.Out, "%s: no accepted commit yet; %s is at %s (%s)\n", src.Name, follows(src), source.Short(commit), describe(src))
	if err := s.review(r, src, commit, s.full); err != nil {
		return "", err
	}
	if mode == checkOnly {
		fmt.Fprintf(s.Out, "%s: not accepted (--check); to accept it, run in a terminal: ctxtra source update %s\n", src.Name, src.Name)
		return commit, nil
	}
	ok, err := s.ask(fmt.Sprintf("Accept %s as the first version of %s? [y/N] ", source.Short(commit), src.Name))
	if err != nil || !ok {
		if err == nil {
			fmt.Fprintf(s.Out, "not accepted; %s has no accepted commit yet\n", src.Name)
		}
		return commit, err
	}
	if err := s.setCommit(src, commit); err != nil {
		return commit, err
	}
	fmt.Fprintf(s.Out, "accepted %s %s, the first version (commit: updated in %s)\n", src.Name, source.Short(commit), filepath.Base(s.file))
	return commit, nil
}

// cleanStore deletes the generations no session used for a while, except
// the accepted commits of this config and of every other config that a
// command changed. It holds the config lock, so that no commit is accepted
// meanwhile.
func (s *sources) cleanStore() {
	if s.registerErr != nil {
		return
	}
	unlock, err := fsutil.Lock(filepath.Join(paths.StateDir(), "config.lock"))
	if err != nil {
		return
	}
	defer unlock()
	configs, err := state.LoadConfigs()
	if err != nil {
		fmt.Fprintf(s.Err, "warning: %v; the store is not cleaned\n", err)
		return
	}
	keep := map[string]bool{}
	files := append([]string{s.file}, configs...)
	for _, f := range files {
		data, err := fsutil.ReadBounded(f, config.MaxFileBytes)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		c, perr := config.Parse(data)
		if err != nil || perr != nil {
			// A config that can't be read may pin anything: keep it all.
			fmt.Fprintf(s.Err, "warning: %s can't be read; the store is not cleaned\n", paths.Tilde(f))
			return
		}
		for _, src := range c.Sources {
			if src.Pinned() && src.Commit != "" {
				keep[src.Gen()] = true
			}
		}
	}
	if n := source.Clean(keep, genAge); n > 0 {
		fmt.Fprintf(s.Out, "deleted %d old commits from ctxtra's store\n", n)
	}
}

// genAge is how long a generation stays after the last session that used
// it started.
const genAge = 30 * 24 * time.Hour

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
		if !ok || now.Remote() != src.Remote() || now.Branch != src.Branch || now.Commit != src.Commit {
			return nil, fmt.Errorf("the source %s changed in %s while ctxtra worked; nothing accepted, run ctxtra source update again",
				src.Name, paths.Tilde(s.file))
		}
		if !source.Has(src.Remote(), commit) {
			return nil, fmt.Errorf("%s left ctxtra's store meanwhile; nothing accepted, run ctxtra source update again", source.Short(commit))
		}
		return config.SetCommit(old, src.Name, commit)
	})
}
