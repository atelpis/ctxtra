# ctxtra tasks

Step by step, for the tasks of the ctxtra skill. The rules and the warnings in its `SKILL.md` apply to
every step.

## First setup

For a user who wants to walk through setup in their own terminal, suggest `ctxtra onboard`.
It sets up hooks, offers the bundled skills, then offers to start an installed agent to help
import existing instructions. When you perform setup for the user, use the steps below.

1. `ctxtra version`. If it fails, ctxtra isn't installed: show the user the install steps from the
   project's README and stop.
2. `ctxtra doctor`: shows the config path, whether a config exists, and which agents have hooks.
3. Ask what the user wants in their sessions, and where: their own notes in every session, rules for some
   repositories, a team repository. Ask for the files. Don't invent instruction files for them.
4. Add the first binding with `ctxtra binding add` (see "Add or change a binding" below). It creates the
   config.
   Start with one binding and add more later.
5. `ctxtra setup --dry-run`, then `ctxtra setup` (rule 8 in `SKILL.md`). Tell the user the extra steps that
   setup prints, for example trusting the hooks in Codex or enabling the Hermes plugin. Setup also prints
   next steps: if the user wants this skill in their sessions, run `ctxtra skill install ctxtra`.
6. Check with `ctxtra context files` and `ctxtra context explain` in a folder where the files should apply.
7. Tell the user: new sessions get the files. A running session, also this one, keeps what it got at its
   start.

## Import existing instructions

The user's agents can have instruction files of their own. Move their rules into files that ctxtra adds,
so that every agent gets them. Do every step; don't skip the files of an agent.

1. Find the files. `ctxtra import --start AGENT` starts an agent with a prompt that lists them and the
   folder with their backup. Without that prompt, run `ctxtra import`: it backs up the files of every
   agent that is set up and lists them. Don't look for other files: the list is complete.
2. Read every file in full, and the files they import (a line `@path` in Claude Code's files). Rules 6 and
   7 in `SKILL.md` apply.
3. Ask the user where to keep the new file. Suggest `~/.config/ctxtra/instructions.md`, or their notes or
   dotfiles folder if they have one. A folder in a git repository needs a trusted source (rule 5).
4. Draft one file with every rule of every file. Don't write it yet:
   - Rules that say the same thing become one rule. Keep the user's words; don't add rules of your own.
   - Drop no rule. If two rules disagree, ask the user which one to keep.
   - A rule for one agent only (it names that agent's tools, commands or settings) doesn't fit every
     agent. List these rules and ask the user: keep them in that agent's file, or move them.
   - A Claude Code rule file with `paths` in its frontmatter applies only when Claude works on matching
     files; ctxtra adds a file to the whole session. Keep such a file in Claude Code, unless the user
     wants its rules in every session.
5. Show the user the new file and, for each old file, which rules moved, which were merged, and which stay.
   Wait for their OK.
6. Write the file, and bind it to every session: `ctxtra binding add NAME --global --instruction FILE`
   (rule 1). Check with `ctxtra context files` that a session gets it.
7. Ask whether to remove the moved rules from the old files. Recommend it, and say why in short: it saves
   tokens, and more importantly the agents don't get two copies that drift apart once the user changes
   only the new file. Say where ctxtra's backup is, and suggest that they make a backup of their own too.
   Remove nothing unless they agree; if they don't, tell them that these agents get the rules twice.
8. If they agree, remove the moved rules from the old files. Show each change and wait for their OK
   (rule 1). Empty a file that has nothing left; don't delete it. Codex reads `AGENTS.md` when
   `AGENTS.override.md` has no text: emptying the override makes Codex read `AGENTS.md` again, so clean
   both. A file that is a symlink changes at its target: say so first. Other tools can read the same file
   (Gemini CLI reads `~/.gemini/GEMINI.md`): say so first, too. Don't change the files that the old files
   import: removing the import line is enough.
9. Tell the user where the backup is, and that new sessions get the new file.

## Add or change a binding

1. Run `ctxtra doctor` to find the config and the binding files it includes. A binding can be in the
   config or in an included binding file. Edit the file where it is.
2. Choose `match` by what the user means: `global: true` for every session; `repos` for a repository
   wherever it is cloned, in every worktree; `dirs` for a folder on this computer. Check the pattern
   rules in `ctxtra help config`: `x/**` also matches `x`, and a pattern without `/**` matches only that
   folder.
3. List files and skill folders by name or use quoted glob patterns. Use `ctxtra context files` or
   `ctxtra context show` to check the expansion. Local globs include new matching files immediately; pinned-source globs change
   after accepting a commit. A skill folder needs a `SKILL.md` with a `description` in its frontmatter.
4. A new binding in the config: `ctxtra binding add NAME --global --instruction FILE --skill DIR`, with
   `--dir PATTERN` or `--repo PATTERN` instead of `--global` (the flags can repeat). Quote patterns, or the
   shell expands them. It writes only a
   binding whose files exist and are trusted, and says what is wrong otherwise. For anything else, edit
   the YAML: read `ctxtra help config` first.
5. Run `ctxtra context explain DIR` in a folder that should match, and in one that shouldn't.
6. To remove a binding of the config: `ctxtra binding remove NAME`.

## Add a team repository

1. Give the warning from "Remote repositories" in `SKILL.md`, and offer to read the repository first.
2. With the user's OK, `ctxtra source add git URL --include PATH`. Without `--include`, it adds the source
   and lists the binding files in it; nothing reaches sessions until one is included
   (`ctxtra binding-file add ctxtra://SOURCE/PATH`), or the user's own bindings reference its files as
   `ctxtra://SOURCE/PATH`.
3. If the user doesn't want all of the maintainer's binding (a `match` that is too wide, a skill they don't
   want), they can bind only some files themselves. See case 4 in `ctxtra help scenarios`.
4. Tell the user to run `ctxtra source update` in their terminal from time to time: ctxtra never checks
   for new commits by itself. `ctxtra source update --check` shows what changed without accepting it.

## A session didn't get a file, or got the wrong one

1. `ctxtra context explain DIR` in the session folder: does the binding match, and why not.
2. `ctxtra doctor DIR`: problems (missing files, untrusted repositories, pinned commits not in the store),
   hook setup for the agent, and whether the hook ran in that folder.
3. Check the limits of the agent in `ctxtra help agents`: some show the model only the start of a long
   text, some don't add the text again in a resumed session.
4. If the file is bound to another repository than the session folder's, for example a session started in
   one repository that then worked in another: the session gets only what its own folder matches. The
   optional `ctxtra-follow` skill gets the other repository's files when the agent works there. Offer
   `ctxtra skill install ctxtra-follow`, and say that the agent decides when to use it.

## Remove something

- `ctxtra source remove NAME` removes a source and its binding files, and lists the lines of the user's
  bindings that still use it. Fix those lines with the user.
- `ctxtra binding-file remove ENTRY` removes one binding file from the config.
- `ctxtra binding remove NAME` removes one binding of the config.
- To stop ctxtra in all agents: `ctxtra setup --remove`.

## Write an instruction file or a skill for the user

Instruction files are plain Markdown. Skills are folders with a `SKILL.md` that starts with YAML
frontmatter with a `description`. Neither needs anything specific to ctxtra. Keep the files where the user
keeps their own files, not in a repository other people push to, unless that is what the user wants.
Keep them short: everything bound is added to every matching session, and `ctxtra context explain` shows
the size.
