package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/atelpis/ctxtra"
	"github.com/atelpis/ctxtra/internal/agent/registry"
	"github.com/atelpis/ctxtra/internal/config"
	"github.com/atelpis/ctxtra/internal/paths"
	"github.com/atelpis/ctxtra/internal/setup"
)

// quiet are the commands after which setupNotices doesn't run: setup and
// onboard, which just ran setup, doctor, which shows the same lines under each agent, and
// the ones that only print help, the version or a completion script.
var quiet = []string{"setup", "onboard", "doctor", "help", "version", "bash", "zsh", "fish", "powershell"}

// root is the command tree.
func (a *app) root() *cobra.Command {
	root := &cobra.Command{
		Use:   "ctxtra",
		Short: "Add your instruction files and skills to agent sessions",
		Long: `ctxtra adds your instruction files and skills to coding-agent sessions, by
folder and repository, through the agents' session-start hooks.

Agents: ` + strings.Join(registry.Names(), ", ") + `.
Config: $CTXTRA_CONFIG or ~/.config/ctxtra/config.yaml (format: ctxtra help config).`,
		Version:           a.version,
		SilenceErrors:     true,
		SilenceUsage:      true,
		PersistentPreRunE: a.global,
	}
	root.SetIn(a.In)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	root.SetVersionTemplate("{{.Version}}\n")
	// Defined here, so that cobra doesn't add -v for it.
	root.Flags().Bool("version", false, "print the version")
	pf := root.PersistentFlags()
	pf.StringVar(&a.config, "config", "", "use the config `FILE` instead of $CTXTRA_CONFIG or ~/.config/ctxtra/config.yaml")
	pf.BoolVar(&a.noInput, "no-input", false, "never ask; a step that needs an answer fails instead")
	root.AddGroup(
		&cobra.Group{ID: "setup", Title: "Set up:"},
		&cobra.Group{ID: "config", Title: "Change the config:"},
		&cobra.Group{ID: "inspect", Title: "See what sessions get:"},
		&cobra.Group{ID: "help", Title: "Help:"},
	)
	root.AddCommand(
		a.onboardCmd(), a.setupCmd(), a.importCmd(),
		a.sourceCmd(), a.bindingFileCmd(), a.bindingCmd(), a.skillCmd(),
		a.contextCmd(), a.doctorCmd(),
		a.versionCmd(), hookCmd(),
	)
	root.SetHelpCommand(a.helpCmd(root))
	root.SetHelpCommandGroupID("help")
	root.SetCompletionCommandGroupID("help")
	help := root.HelpFunc()
	root.SetHelpFunc(func(c *cobra.Command, args []string) {
		help(c, args)
		if c == root {
			fmt.Fprintf(c.OutOrStdout(), "\nHelp topics (ctxtra help TOPIC):\n%s", helpTopics())
		}
	})
	return root
}

// Positional arguments. Their errors are usage errors.

func noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}
	return nil
}

// exactly accepts the arguments named in what, one each.
func exactly(what ...string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		switch {
		case len(args) < len(what):
			return fmt.Errorf("expected %s", strings.Join(what[len(args):], " "))
		case len(args) > len(what):
			return fmt.Errorf("unexpected argument %q", args[len(what)])
		}
		return nil
	}
}

// atMost accepts up to n arguments.
func atMost(n int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) > n {
			return fmt.Errorf("unexpected argument %q", args[n])
		}
		return nil
	}
}

// group runs a command that only groups others: its help, or a usage
// error for an unknown subcommand.
func group(c *cobra.Command, args []string) error {
	if len(args) == 0 {
		return c.Help()
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], c.CommandPath())
	c.SuggestionsMinimumDistance = 2
	if s := c.SuggestionsFor(args[0]); len(s) > 0 {
		msg += "\n\nDid you mean this?\n\t" + strings.Join(s, "\n\t")
	}
	return errors.New(msg)
}

// dirArg is the folder argument of the commands that show what a session
// gets: the current folder by default.
func dirArg(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return "."
}

func (a *app) onboardCmd() *cobra.Command {
	var agents []string
	c := &cobra.Command{
		Use:   "onboard",
		Short: "Get started with guided hook setup, skills and instruction import",
		Long: `Set up hooks for your agents, learn about ctxtra's skills and choose which
ones to install. Then optionally start an installed agent to help move your
existing instruction files into ctxtra. Each step explains what it changes.

Needs an interactive terminal. For scripts and agents, use setup, skill install
and import separately. Without --agent, sets up every agent found on this machine.`,
		Example: `  ctxtra onboard
  ctxtra onboard --agent claude --agent codex`,
		Args:    noArgs,
		GroupID: "setup",
		RunE: a.run(func(*cobra.Command, []string) error {
			return code(onboard(a.IO, agents, a.prompts()))
		}),
	}
	c.Flags().StringArrayVar(&agents, "agent", nil, "set up only `AGENT` (repeatable)")
	return c
}

func (a *app) setupCmd() *cobra.Command {
	var o setup.Options
	c := &cobra.Command{
		Use:   "setup",
		Short: "Add ctxtra's hooks to your agents' user-level configs",
		Long: `Add hooks that run this binary to the user-level config of each agent, so
that new sessions get your instruction files and skills. Without --agent:
every agent found on this machine. Run it again after you move the binary.

Agents: ` + strings.Join(registry.Names(), ", ") + `.`,
		Example: `  ctxtra setup
  ctxtra setup --agent claude --agent codex --dry-run
  ctxtra setup --remove --agent cursor`,
		Args:    noArgs,
		GroupID: "setup",
		RunE: a.run(func(*cobra.Command, []string) error {
			return code(setupCmd(a.IO, o))
		}),
	}
	c.Flags().StringArrayVar(&o.Agents, "agent", nil, "change only `AGENT` (repeatable)")
	c.Flags().BoolVar(&o.Remove, "remove", false, "remove ctxtra's hook entries, and nothing else")
	c.Flags().BoolVar(&o.DryRun, "dry-run", false, "show what would change; change nothing")
	return c
}

func (a *app) importCmd() *cobra.Command {
	var start string
	var dryRun bool
	c := &cobra.Command{
		Use:   "import",
		Short: "Back up your agents' own instruction files, and move them into ctxtra",
		Long: `Back up the instruction files that your agents have of their own (such as
~/.claude/CLAUDE.md) and list them. With --start, then start that agent with
the ctxtra skill to move them into ctxtra, so that all your agents get them.
The agent shows you the result and empties the files only if you agree.

--start needs a terminal and the ctxtra skill (ctxtra skill install ctxtra).`,
		Example: `  ctxtra import
  ctxtra import --start claude`,
		Args:    noArgs,
		GroupID: "setup",
		RunE: a.run(func(*cobra.Command, []string) error {
			return code(runImport(a.IO, start, dryRun, a.prompts()))
		}),
	}
	c.Flags().StringVar(&start, "start", "", "start `AGENT` to move the files into ctxtra")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "list the files; back up and start nothing")
	return c
}

func (a *app) sourceCmd() *cobra.Command {
	c := &cobra.Command{
		Use:                   "source",
		RunE:                  group,
		DisableFlagsInUseLine: true,
		Short:                 "Add, update and remove sources: repositories and folders of files",
		Long: `A source is where instruction files, skills and binding files come from:

  git           a team repository, pinned to a commit you accepted
  local-git     your clone of a repository, pinned like git, following its
                committed HEAD
  trusted-path  a folder of your own files, read from disk; changes apply
                right away

Files of a source are referenced as ctxtra://SOURCE/PATH.`,
		Example: `  ctxtra source add git https://github.com/acme/agent-context --include bindings/work.yaml
  ctxtra source update`,
		GroupID: "config",
	}
	add := &cobra.Command{
		Use:   "add",
		Short: "Add a source",
		Example: `  ctxtra source add git https://github.com/acme/agent-context
  ctxtra source add trusted-path ~/dev/dotfiles --include ctxtra/work.yaml`,
		RunE:                  group,
		DisableFlagsInUseLine: true,
	}
	add.AddCommand(a.sourceAddCmd("git"), a.sourceAddCmd("local-git"), a.sourceAddCmd("trusted-path"))
	c.AddCommand(add, a.sourceListCmd(), a.sourceShowCmd(), a.sourceUpdateCmd(), a.sourceRestoreCmd(),
		a.sourceTrustCmd(), a.sourceRemoveCmd())
	return c
}

func (a *app) sourceAddCmd(kind string) *cobra.Command {
	var o addOptions
	c := &cobra.Command{RunE: a.run(func(_ *cobra.Command, args []string) error {
		s := newSources(a.IO)
		var err error
		if o.includes, err = cleanIncludes(o.includes); err != nil {
			return err
		}
		wd, _ := os.Getwd()
		switch kind {
		case "git":
			dir, isDir, err := cloneDir(args[0])
			switch {
			case err != nil:
				return code(s.fail("source add git", err))
			case isDir:
				err = s.addGitClone(dir, o)
			case o.remote != "":
				return errors.New("--remote picks a remote of a clone: it needs a clone's folder, not a URL")
			default:
				err = s.addGitURL(args[0], o)
			}
			return s.result("source add git", err)
		case "local-git":
			return s.result("source add local-git", s.addLocalGit(paths.Resolve(wd, args[0]), o))
		default:
			return s.result("source add trusted-path", s.addTrustedPath(paths.Resolve(wd, args[0]), o))
		}
	})}
	switch kind {
	case "git":
		c.Use = "git (URL | DIR)"
		c.Short = "Add a team repository as a pinned source"
		c.Long = `Add a git repository as a pinned source: ctxtra fetches it and records the
current commit, and sessions use exactly that commit until you accept a newer
one with ctxtra source update.

DIR is your clone of the repository: its remote's URL is used, and plain paths
under the clone are read from the pinned commit, not from disk. The source then
starts at your local commit.`
		c.Example = `  ctxtra source add git https://github.com/acme/agent-context --include bindings/work.yaml
  ctxtra source add git ~/dev/agent-context --name team`
		c.Args = exactly("URL or DIR")
		c.Flags().StringVar(&o.branch, "branch", "", "follow `BRANCH` (default: the remote's default branch, or the clone's upstream)")
		c.Flags().StringVar(&o.remote, "remote", "", "use the clone's remote `REMOTE` (with DIR; needed when it has several)")
	case "local-git":
		c.Use = "local-git DIR"
		c.Short = "Add your clone as a pinned source that follows its committed HEAD"
		c.Long = `Add your clone of a repository as a pinned source that follows the clone's
committed HEAD, to try changes before you push them. Uncommitted changes are
not used; ctxtra source update offers each new commit for review.`
		c.Example = `  ctxtra source add local-git ~/dev/agent-context --include bindings/work.yaml`
		c.Args = exactly("DIR")
	default:
		c.Use = "trusted-path DIR"
		c.Short = "Add a folder of your own files as a trusted source"
		c.Long = `Add a folder of your own files as a trusted source: its files are read from
disk, and changes apply to new sessions right away, without review. Use it
only for files that you alone write.`
		c.Example = `  ctxtra source add trusted-path ~/dev/my-context
  ctxtra source add trusted-path ~/dev/my-context --include bindings.yaml`
		c.Args = exactly("DIR")
	}
	c.Flags().StringVar(&o.name, "name", "", "name the source `NAME` (default: from the URL or folder)")
	c.Flags().StringArrayVar(&o.includes, "include", nil, "also include the binding file `PATH` of the source (repeatable)")
	c.Flags().BoolVar(&o.dryRun, "dry-run", false, "check and show the change; change nothing and fetch nothing")
	return c
}

func (a *app) sourceListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "list",
		Short:   "List the sources",
		Example: "  ctxtra source list\n  ctxtra source list --json",
		Args:    noArgs,
		RunE: a.run(func(*cobra.Command, []string) error {
			return code(newSources(a.IO).list(asJSON))
		}),
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return c
}

func (a *app) sourceShowCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "show NAME",
		Short:   "Show a source: its commit, last check, binding files and clones",
		Example: `  ctxtra source show team`,
		Args:    exactly("NAME"),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			return code(newSources(a.IO).show(args[0], asJSON))
		}),
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return c
}

func (a *app) sourceUpdateCmd() *cobra.Command {
	var full, check, asJSON bool
	c := &cobra.Command{
		Use:   "update [NAME]...",
		Short: "Fetch pinned sources, review what changed, and accept it",
		Long: `Fetch the pinned sources (all of them without NAME), show what changed in
the files your bindings use, and ask whether to accept the new commit. New
sessions get the new files only after you accept them.

Accepting needs a terminal. --check fetches and shows the same review but
accepts and dismisses nothing; it works anywhere, for example in an agent.`,
		Example: `  ctxtra source update
  ctxtra source update team --full
  ctxtra source update --check --json`,
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			if asJSON && !check {
				return errors.New("--json needs --check")
			}
			mode := accept
			if check {
				mode = checkOnly
			}
			s := newSources(a.IO)
			s.full, s.prompt = full, a.prompts()
			return code(s.update(args, mode, asJSON))
		}),
	}
	c.Flags().BoolVar(&full, "full", false, "show every changed file in full, also the ones you don't use")
	c.Flags().BoolVar(&check, "check", false, "fetch and review only; accept and dismiss nothing")
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON (with --check)")
	return c
}

func (a *app) sourceRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore [NAME]...",
		Short: "Fetch exactly the commits that the config pins",
		Long: `Fetch the commits that the config pins into ctxtra's store, for example on a
new machine or after the store was deleted. It accepts nothing new and changes
nothing in the config.`,
		Example: `  ctxtra source restore`,
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			return code(newSources(a.IO).update(args, restore, false))
		}),
	}
}

func (a *app) sourceTrustCmd() *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:   "trust NAME [DIR]",
		Short: "Convert a pinned source into a trusted one, read from your clone",
		Long: `Convert the pinned source NAME into a trusted-path source that reads DIR, your
clone of it, from disk: it keeps its name, so every ctxtra://NAME/ reference
now reads your files, and changes apply right away, without review.

DIR may be left out when ctxtra knows exactly one clone of the source.`,
		Example: `  ctxtra source trust team
  ctxtra source trust team ~/dev/agent-context`,
		Args: func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("expected NAME")
			}
			return atMost(2)(c, args)
		},
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			dir := ""
			if len(args) == 2 {
				wd, _ := os.Getwd()
				dir = paths.Resolve(wd, args[1])
			}
			s := newSources(a.IO)
			return s.result("source trust", s.trust(args[0], dir, dryRun))
		}),
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show the change; change nothing")
	return c
}

func (a *app) sourceRemoveCmd() *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:     "remove NAME",
		Short:   "Remove a source and the binding-file entries that point into it",
		Example: `  ctxtra source remove team`,
		Args:    exactly("NAME"),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			s := newSources(a.IO)
			return s.result("source remove", s.removeSource(args[0], dryRun))
		}),
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show the change; change nothing")
	return c
}

func (a *app) bindingFileCmd() *cobra.Command {
	c := &cobra.Command{
		Use:                   "binding-file",
		RunE:                  group,
		DisableFlagsInUseLine: true,
		Short:                 "Include, list and remove binding files",
		Long: `A binding file is a YAML file of bindings, yours or a team's. The config's
include list names the binding files whose bindings apply.`,
		Example: `  ctxtra binding-file add ctxtra://team/bindings/work.yaml
  ctxtra binding-file list`,
		GroupID: "config",
	}
	var dryRun bool
	add := &cobra.Command{
		Use:   "add (FILE | ctxtra://SOURCE/PATH)",
		Short: "Include a binding file",
		Long: `Include a binding file: one on your disk, in a trusted place, or one of a
source you have. It never adds, pins or trusts a source: for a file in a
repository that isn't a source yet, add the repository first with ctxtra
source add, and its --include.`,
		Example: `  ctxtra binding-file add ~/agent-context/bindings.yaml
  ctxtra binding-file add ctxtra://team/bindings/work.yaml`,
		Args: exactly("FILE or ctxtra://SOURCE/PATH"),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			s := newSources(a.IO)
			return s.result("binding-file add", s.addBindingFile(args[0], dryRun))
		}),
	}
	add.Flags().BoolVar(&dryRun, "dry-run", false, "check and show the change; change nothing")
	var asJSON bool
	list := &cobra.Command{
		Use:     "list",
		Short:   "List the included binding files",
		Example: "  ctxtra binding-file list",
		Args:    noArgs,
		RunE: a.run(func(*cobra.Command, []string) error {
			return code(newSources(a.IO).listBindingFiles(asJSON))
		}),
	}
	list.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	var removeDry bool
	remove := &cobra.Command{
		Use:     "remove ENTRY",
		Short:   "Remove an entry from the include list; the file stays",
		Example: `  ctxtra binding-file remove ctxtra://team/bindings/work.yaml`,
		Args:    exactly("ENTRY"),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			s := newSources(a.IO)
			return s.result("binding-file remove", s.removeBindingFile(args[0], removeDry))
		}),
	}
	remove.Flags().BoolVar(&removeDry, "dry-run", false, "show the change; change nothing")
	c.AddCommand(add, list, remove)
	return c
}

func (a *app) bindingCmd() *cobra.Command {
	c := &cobra.Command{
		Use:                   "binding",
		RunE:                  group,
		DisableFlagsInUseLine: true,
		Short:                 "Add, list and remove bindings: which sessions get which files",
		Long: `A binding adds instruction files and skills to the sessions it matches: every
session, sessions in matching folders, or sessions in matching repositories.
binding add and remove change the bindings in your config; binding list also
shows the bindings of the included binding files.`,
		Example: `  ctxtra binding add me --global --instruction ~/agent-context/profile.md
  ctxtra binding list`,
		GroupID: "config",
	}
	var b config.NewBinding
	var dryRun bool
	add := &cobra.Command{
		Use:   "add NAME (--global | {--dir PATTERN | --repo PATTERN}...) {--instruction FILE | --skill DIR}...",
		Short: "Add a binding to the config",
		Long: `Add a binding to the config. It is written only when it works: its files
exist and are trusted, as a session would read them. --dir and --repo may be
combined; a session matches when either does.

Quote patterns, or the shell expands them: --dir '~/dev/work/**'. x/** also
matches x itself.`,
		Example: `  ctxtra binding add me --global --instruction ~/agent-context/profile.md
  ctxtra binding add work --dir '~/dev/work/**' --instruction ~/agent-context/work.md --skill ~/skills/deploy
  ctxtra binding add acme --repo 'github.com/acme/**' --instruction ctxtra://team/acme.md`,
		Args: exactly("NAME"),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			b.Name = args[0]
			return bindingAdd(a.IO, b, dryRun)
		}),
	}
	f := add.Flags()
	f.BoolVar(&b.Global, "global", false, "add it to every session")
	f.StringArrayVar(&b.Dirs, "dir", nil, "add it to sessions in folders that match `PATTERN` (repeatable)")
	f.StringArrayVar(&b.Repos, "repo", nil, "add it to sessions in repositories whose remote matches `PATTERN` (repeatable), such as github.com/acme/**")
	f.StringArrayVar(&b.Instructions, "instruction", nil, "add the instruction file `FILE` (repeatable); !FILE leaves files out")
	f.StringArrayVar(&b.Skills, "skill", nil, "add the skill folder `DIR`, with a SKILL.md (repeatable); !DIR leaves skills out")
	f.BoolVar(&dryRun, "dry-run", false, "check the binding and show it; change nothing")
	var asJSON bool
	list := &cobra.Command{
		Use:     "list",
		Short:   "List the bindings, with the file each one is in",
		Example: "  ctxtra binding list\n  ctxtra binding list --json",
		Args:    noArgs,
		RunE: a.run(func(*cobra.Command, []string) error {
			return code(bindingList(a.IO, asJSON))
		}),
	}
	list.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	var removeDry bool
	remove := &cobra.Command{
		Use:     "remove NAME",
		Short:   "Remove a binding from the config",
		Example: `  ctxtra binding remove work`,
		Args:    exactly("NAME"),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			return code(bindingRemove(a.IO, args[0], removeDry))
		}),
	}
	remove.Flags().BoolVar(&removeDry, "dry-run", false, "show the change; change nothing")
	c.AddCommand(add, list, remove)
	return c
}

func (a *app) skillCmd() *cobra.Command {
	c := &cobra.Command{
		Use:                   "skill",
		RunE:                  group,
		DisableFlagsInUseLine: true,
		Short:                 "Show and install ctxtra's own skills",
		Long: `ctxtra comes with skills for your agents:

  ctxtra         lets your agents set up and change ctxtra for you
  ctxtra-follow  when an agent's work moves to another folder, it gets your
                 bindings for that folder too`,
		Example: `  ctxtra skill install ctxtra`,
		GroupID: "config",
	}
	var asJSON bool
	list := &cobra.Command{
		Use:     "list",
		Short:   "List ctxtra's skills and whether each is installed",
		Example: "  ctxtra skill list",
		Args:    noArgs,
		RunE: a.run(func(*cobra.Command, []string) error {
			return code(skillList(a.IO, asJSON))
		}),
	}
	list.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	show := &cobra.Command{
		Use:     "show NAME",
		Short:   "Print a skill, to give it to any agent",
		Example: `  ctxtra skill show ctxtra`,
		Args:    exactly("NAME"),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			return code(skillShow(a.IO, args[0]))
		}),
	}
	var dryRun bool
	install := &cobra.Command{
		Use:   "install NAME",
		Short: "Install a skill and add it to every session",
		Long: `Write the skill to ~/.ctxtra/skills/NAME and add a binding of the same name
that adds it to every session, unless a binding has it already.`,
		Example: `  ctxtra skill install ctxtra
  ctxtra skill install ctxtra-follow`,
		Args: exactly("NAME"),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			return code(skillInstall(a.IO, args[0], dryRun))
		}),
	}
	install.Flags().BoolVar(&dryRun, "dry-run", false, "show the change; change nothing")
	c.AddCommand(list, show, install)
	return c
}

func (a *app) contextCmd() *cobra.Command {
	c := &cobra.Command{
		Use:                   "context",
		RunE:                  group,
		DisableFlagsInUseLine: true,
		Short:                 "See what a session in a folder gets, and why",
		Long: `See what a new session in DIR (default: the current folder) gets from your
bindings. These commands only read: they fetch nothing and change nothing.`,
		Example: `  ctxtra context explain ~/dev/work/api
  ctxtra context show`,
		GroupID: "inspect",
	}
	var filesJSON bool
	files := &cobra.Command{
		Use:     "files [DIR]",
		Short:   "List the instruction files and skills a session gets",
		Example: `  ctxtra context files ~/dev/work/api`,
		Args:    atMost(1),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			return code(report{a.IO}.files(dirArg(args), filesJSON))
		}),
	}
	files.Flags().BoolVar(&filesJSON, "json", false, "print JSON")
	var o showOptions
	show := &cobra.Command{
		Use:   "show [DIR]",
		Short: "Print the exact text a session gets",
		Long: `Print the exact text that ctxtra adds to a new session in DIR.

--exclude-session prints only what DIR adds to a session that runs in another
folder already: ctxtra resolves that folder again now, it doesn't read the
running session. --part prints one part of a long text, for agents that cut
long command output.`,
		Example: `  ctxtra context show
  ctxtra context show ~/dev/api --exclude-session ~/dev/web --part 1`,
		Args: atMost(1),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			return code(report{a.IO}.show(dirArg(args), o))
		}),
	}
	show.Flags().StringArrayVar(&o.exclude, "exclude-session", nil, "leave out what a session in `DIR` gets (repeatable)")
	show.Flags().IntVar(&o.part, "part", 0, fmt.Sprintf("print part `N` of the text, in parts of at most %d bytes", showPart))
	var explainJSON bool
	explain := &cobra.Command{
		Use:     "explain [DIR]",
		Short:   "Show why each binding matches or not, and the size of each file",
		Example: `  ctxtra context explain ~/dev/work/api`,
		Args:    atMost(1),
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			return code(report{a.IO}.explain(dirArg(args), explainJSON))
		}),
	}
	explain.Flags().BoolVar(&explainJSON, "json", false, "print JSON")
	c.AddCommand(files, show, explain)
	return c
}

func (a *app) doctorCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "doctor [DIR]",
		Short: "Check the config, the agents' setup and recent hook runs",
		Long: `Check config problems in every binding, what a session in DIR (default: the
current folder) gets, the setup of every agent (including Codex's hook trust),
and the last hook runs there. It exits with 1 when it finds a problem.`,
		Example: `  ctxtra doctor`,
		Args:    atMost(1),
		GroupID: "inspect",
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			return code(report{a.IO}.doctor(dirArg(args), asJSON))
		}),
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return c
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the version",
		Example: "  ctxtra version\n  ctxtra --version",
		Args:    noArgs,
		GroupID: "help",
		RunE: a.run(func(*cobra.Command, []string) error {
			fmt.Fprintln(a.Out, a.version)
			return nil
		}),
	}
}

// helpCmd shows help for a command, or a docs topic of this version.
func (a *app) helpCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "help [COMMAND... | TOPIC]",
		Short: "Help for a command, or a docs topic",
		Long: `Help for a command, the same as COMMAND --help, or a topic of the docs of
this version:

` + helpTopics(),
		Example: `  ctxtra help source add git
  ctxtra help config`,
		RunE: a.run(func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				if t, ok := findTopic(args[0]); ok {
					text, err := t.read()
					if err != nil {
						fmt.Fprintf(a.Err, "ctxtra help: %v\n", err)
						return exitCode(1)
					}
					fmt.Fprint(a.Out, text)
					return nil
				}
			}
			c, rest, err := root.Find(args)
			if err != nil || len(rest) > 0 {
				return fmt.Errorf("no command or topic %q; topics:\n%s", strings.Join(args, " "), helpTopics())
			}
			return c.Help()
		}),
	}
}

// hookCmd documents the hook entry point. Run calls the hook before the
// command tree, so this command never runs.
func hookCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hook AGENT [EVENT [--slot K --of N]]",
		Short: "The entry point of the agents' hooks",
		Long: `The command that the hooks written by ctxtra setup run: it reads the agent's
hook input (JSON) on stdin and answers with the text for the session, in the
agent's format. EVENT defaults to SessionStart. Agents with a size limit per
hook run several hooks, each with its slot K of N. It never fails a session:
problems go into the answer and the log. You don't run it yourself.`,
		Hidden:                true,
		DisableFlagParsing:    true,
		DisableFlagsInUseLine: true,
		Run:                   func(*cobra.Command, []string) {},
	}
}

// helpTopics lists the help topics, for help texts.
func helpTopics() string {
	var b strings.Builder
	for _, t := range topics {
		fmt.Fprintf(&b, "  %-10s %s\n", t.name, t.about)
	}
	return b.String()
}

// skillNames lists ctxtra's skills, for messages.
func skillNames() string { return strings.Join(ctxtra.SkillNames(), ", ") }
