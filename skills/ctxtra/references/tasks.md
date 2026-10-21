# ctxtra tasks

Step by step, for the tasks of the ctxtra skill. The rules and the warnings in its `SKILL.md` apply to
every step.

## First setup

1. `ctxtra version`. If it fails, ctxtra isn't installed: show the user the install steps from the
   project's README and stop.
2. `ctxtra doctor`: shows the config path, whether a config exists, and which agents have hooks.
3. Ask what the user wants in their sessions, and where: their own notes in every session, rules for some
   repositories, a team repository. Ask for the files. Don't invent instruction files for them.
4. Add the first binding with `ctxtra bind` (see "Add or change a binding" below). It creates the config.
   Start with one binding and add more later.
5. `ctxtra setup --dry-run`, then `ctxtra setup` (rule 8 in `SKILL.md`). Tell the user the extra steps that setup prints,
   for example trusting the hooks in Codex or enabling the Hermes plugin. Run from your shell, setup doesn't
   ask about the ctxtra skill. If the user wants it in their sessions, run `ctxtra skill install`.
6. Check with `ctxtra files` and `ctxtra explain` in a folder where the files should apply.
7. Tell the user: new sessions get the files. A running session, also this one, keeps what it got at its
   start.

## Import existing instructions

The user's agents can have instruction files of their own. Move their rules into files that ctxtra adds,
so that every agent gets them. Do every step; don't skip the files of an agent.

1. Find the files. `ctxtra setup` and `ctxtra import` start an agent with a prompt that lists them and
   the folder with their backup. Without that prompt, run `ctxtra import --no-start`: it backs up the
   files of every agent that is set up and lists them. Don't look for other files: the list is complete.
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
6. Write the file, and bind it to every session: `ctxtra bind NAME --global --file FILE` (rule 1). Check
   with `ctxtra files` that a session gets it.
7. Only then remove the moved rules from the old files. Empty a file that has nothing left; don't delete
   it. Codex reads `AGENTS.md` when `AGENTS.override.md` has no text: emptying the override makes Codex
   read `AGENTS.md` again, so clean both. A file that is a symlink changes at its target: say so first.
   Other tools can read the same file (Gemini CLI reads `~/.gemini/GEMINI.md`): say so first, too.
   Don't change the files that the old files import: removing the import line is enough.
8. Tell the user where the backup is, and that new sessions get the new file.

## Add or change a binding

1. Run `ctxtra doctor` to find the config and the binding files it includes. A binding can be in the
   config or in an included binding file. Edit the file where it is.
2. Choose `match` by what the user means: `global: true` for every session; `repos` for a repository
   wherever it is cloned, in every worktree; `dirs` for a folder on this computer. Check the pattern
   rules in `ctxtra help config`: `x/**` also matches `x`, and a pattern without `/**` matches only that
   folder.
3. List files and skill folders by name or use quoted glob patterns. Use `ctxtra files` or `ctxtra show`
   to check the expansion. Local globs include new matching files immediately; pinned-source globs change
   after accepting a commit. A skill folder needs a `SKILL.md` with a `description` in its frontmatter.
4. A new binding in the config: `ctxtra bind NAME --global|--dir PATTERN|--repo REMOTE --file FILE
   --skill FOLDER` (the flags can repeat). Quote patterns, or the shell expands them. It writes only a
   binding whose files exist and are trusted, and says what is wrong otherwise. For anything else, edit
   the YAML: read `ctxtra help config` first.
5. Run `ctxtra explain DIR` in a folder that should match, and in one that shouldn't.

## Add a team repository

1. Give the warning from "Remote repositories" in `SKILL.md`, and offer to read the repository first.
2. With the user's OK, `ctxtra add <git URL> [<binding file>]`. Without a binding file, `add` adds the
   source and lists the binding files in it; nothing reaches sessions until one is included, or the
   user's own bindings reference its files as `ctxtra://<source>/<path>`.
3. If the user doesn't want all of the maintainer's binding (a `match` that is too wide, a skill they don't
   want), they can bind only some files themselves. See case 4 in `ctxtra help scenarios`.
4. Tell the user to run `ctxtra sync` from time to time: ctxtra never checks for new commits by itself.

## A session didn't get a file, or got the wrong one

1. `ctxtra explain DIR` in the session folder: does the binding match, and why not.
2. `ctxtra doctor DIR`: problems (missing files, untrusted repositories, pinned commits not in the store),
   hook setup for the agent, and whether the hook ran in that folder.
3. Check the limits of the agent in `ctxtra help agents`: some show the model only the start of a long
   text, some don't add the text again in a resumed session.

## Remove something

- `ctxtra remove <source>` removes a source and its include entries, and lists the lines of the user's
  bindings that still use it. Fix those lines with the user.
- `ctxtra remove <include entry>` removes one binding file from the config.
- To stop ctxtra in all agents: `ctxtra setup --remove`.

## Write an instruction file or a skill for the user

Instruction files are plain Markdown. Skills are folders with a `SKILL.md` that starts with YAML
frontmatter with a `description`. Neither needs anything specific to ctxtra. Keep the files where the user
keeps their own files, not in a repository other people push to, unless that is what the user wants.
Keep them short: everything bound is added to every matching session, and `ctxtra explain` shows the size.
