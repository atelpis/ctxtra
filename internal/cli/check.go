package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ctxtra/ctxtra/internal/config"
	"github.com/ctxtra/ctxtra/internal/paths"
	"github.com/ctxtra/ctxtra/internal/resolve"
	"github.com/ctxtra/ctxtra/internal/source"
)

// checkResult is the data of source check --json.
type checkResult struct {
	Dir          string   `json:"dir"`
	Name         string   `json:"name"`
	BindingFiles []string `json:"binding_files"`
	Bindings     int      `json:"bindings"`
	Errors       int      `json:"errors"`
	Warnings     int      `json:"warnings"`
}

// checkSource is source check: it reads the binding files of the folder dir
// the way sessions read them from a pinned source called name, and reports
// what would be broken for someone who includes them. files are binding
// files of the folder, relative to it, slash-separated and cleaned; without
// them, every YAML file with a ctxtra schema is checked. It reads no config
// and runs and fetches nothing. 1 when it finds an error.
func checkSource(stdio IO, dir, name string, files []string, asJSON bool) int {
	fail := func(err error) int {
		fmt.Fprintf(stdio.Err, "ctxtra source check: %v\n", err)
		return 1
	}
	if info, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return fail(fmt.Errorf("%s does not exist", paths.Tilde(dir)))
	} else if err != nil {
		return fail(err)
	} else if !info.IsDir() {
		return fail(fmt.Errorf("%s is not a folder", paths.Tilde(dir)))
	}
	dir = paths.Real(dir)
	if name == "" {
		name = defaultName(dir)
	}
	if !config.ValidName(name) {
		return fail(fmt.Errorf("%q can't be a source name (letters, digits, '.', '_', '-'); choose one with --name", name))
	}
	var errs []string
	var abs []string
	if len(files) == 0 {
		found, unreadable, err := findBindingFiles(dir)
		if err != nil {
			return fail(err)
		}
		errs = append(errs, unreadable...)
		if len(found) == 0 && len(unreadable) == 0 {
			return fail(fmt.Errorf("no binding files in %s (YAML files with schema: %s)", paths.Tilde(dir), config.Schema))
		}
		abs = found
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if why := config.LeftOut(dir, p); why != "" {
			errs = append(errs, fmt.Sprintf("%s %s", f, why))
			continue
		}
		abs = append(abs, p)
	}

	cfg := config.LoadSource(dir, name, abs)
	errs = append(errs, cfg.Problems...)
	for _, b := range cfg.Bindings {
		var gone []string
		drop := func(p string) bool {
			why := config.LeftOut(dir, p)
			if why != "" {
				gone = append(gone, fmt.Sprintf("%s: %s %s", b.ID(), p, why))
			}
			return why != ""
		}
		b.Instructions = slices.DeleteFunc(slices.Clone(b.Instructions), drop)
		b.Skills = slices.DeleteFunc(slices.Clone(b.Skills), func(p string) bool { return drop(filepath.Join(p, "SKILL.md")) })
		errs = append(errs, gone...)
		errs = append(errs, resolve.CheckBinding(cfg, b)...)
	}
	warns := cfg.Warnings

	short := func(msgs []string) []string {
		out := make([]string, len(msgs))
		for i, m := range msgs {
			m = strings.ReplaceAll(m, `"`+paths.Tilde(dir)+`"`, `"."`)
			m = strings.ReplaceAll(m, paths.Tilde(dir)+"/", "")
			out[i] = strings.ReplaceAll(m, dir+string(filepath.Separator), "")
		}
		return out
	}
	errs, warns = short(errs), short(warns)
	rel := make([]string, len(abs))
	for i, p := range abs {
		rel[i], _ = paths.Rel(dir, p)
	}

	if asJSON {
		writeJSON(stdio.Out, checkResult{dir, name, rel, len(cfg.Bindings), len(errs), len(warns)},
			slices.Concat(diagnostics("error", errs), diagnostics("warning", warns)))
	} else {
		fmt.Fprintf(stdio.Out, "checked %s as the source %s\n", paths.Tilde(dir), name)
		for _, f := range rel {
			n := 0
			for _, b := range cfg.Bindings {
				if b.Source == filepath.Join(dir, filepath.FromSlash(f)) {
					n++
				}
			}
			fmt.Fprintf(stdio.Out, "  %s: %s\n", f, plural(n, "binding"))
		}
		for _, e := range errs {
			fmt.Fprintf(stdio.Out, "error: %s\n", e)
		}
		for _, w := range warns {
			fmt.Fprintf(stdio.Out, "warning: %s\n", w)
		}
		fmt.Fprintf(stdio.Out, "%s, %s\n", plural(len(errs), "error"), plural(len(warns), "warning"))
	}
	if len(errs) > 0 {
		return 1
	}
	return 0
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// findBindingFiles lists the YAML files under dir that have a ctxtra schema,
// in lexical order, and the YAML files it can't read. It skips what a pinned
// source leaves out: symlinks and nested repositories. A file over the size
// limit is listed too, so that loading reports it.
func findBindingFiles(dir string) (found, unreadable []string, err error) {
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			if p != dir && (d.Name() == ".git" || source.Exists(filepath.Join(p, ".git"))) {
				return filepath.SkipDir
			}
			return nil
		case !d.Type().IsRegular() || !strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml"):
			return nil
		}
		ok, err := hasSchema(p)
		switch {
		case err != nil:
			unreadable = append(unreadable, fmt.Sprintf("%s: %v", p, err))
		case ok:
			found = append(found, p)
		}
		return nil
	})
	return found, unreadable, err
}

// hasSchema reports whether a line of the file p starts like a ctxtra
// schema. It reads the whole file, a line start at a time, so a large file is
// classified too.
func hasSchema(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	start := true
	for {
		line, more, err := r.ReadLine()
		if start && reSchema.Match(line) {
			return true, nil
		}
		start = !more
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}
