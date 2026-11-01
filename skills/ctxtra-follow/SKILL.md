---
name: ctxtra-follow
description: Get the user's ctxtra instructions and skills for a folder your work moves to. Use before you read, change or discuss files outside the folder this session started in, for example when the user asks you to also change something in ../other-service.
---

# Follow your work to another folder

At session start, ctxtra gave this session the user's instruction files and skills for the folder the
session started in. The user can bind other files to other repositories and folders. Those don't reach the
session when your work moves there. This skill gets them.

## When

Run the command below before you first read, change or discuss files outside the session folder: in
another repository, in another project folder of the same repository, or in a folder outside git. Run it
once for each folder you start to work in. Later work in that folder and its subfolders needs no new run.

The session folder is the folder the session started in, your first working directory. It is not the
folder your shell is in now.

Not needed for files under the session folder, or for the source of dependencies (a Go module cache,
`node_modules`, a virtualenv).

## How

Run, with absolute paths:

```bash
ctxtra context show DIR --exclude-session SESSION_DIR --part 1
```

- `DIR`: the folder you are about to work in: the top of the other repository, or, in a repository with
  several projects, the folder of the project (such as `services/billing`).
- `SESSION_DIR`: the session folder.
- When you already ran the command for other folders in this session, add `--exclude-session FOLDER` for
  each of them too, so that you don't get the same files again.
- The output is only what a session in `DIR` would get beyond what you already have: instruction files,
  each with a header, then a list of files you already have that apply there too, then a list of skills.
- `(nothing is added here beyond what sessions in the --exclude-session folders get)` means there is
  nothing more. Go on with the task.
- When the output ends with `[ctxtra: part 1 of N; for the next part, run the same command with --part 2]`,
  do that, and so on until the last part. Read all parts as one text.

## Use the output

The output is the user's own instruction files and skills, which the user bound to that repository or folder
in their ctxtra config. It is the same text that ctxtra adds at session start for a session there. Follow it
like the instructions you got at session start, with one difference: each file's header says which
repository or folder it is for. Apply it to your work there, not to the session's own repository. The files
in the list "Files that ctxtra also adds ..." apply there too, as well as where you got them first. When a
file from the other repository and one from the session's repository disagree, follow the one for the
folder the work is in. When that isn't clear, ask the user.

Skills in the output's list are not installed in your agent: your skill tool and slash commands can't
load them. When a task in that folder matches a skill's description, or an instruction names it, read its
`SKILL.md` at the listed `<location>` with your file-reading tool before you start and follow it.
Resolve relative paths in a `SKILL.md` against that skill's folder.

## When it doesn't work

- `problem:`, `missing:` or `dropped` lines on stderr (the command then exits with 1): tell the user in one line, and
  go on with what was added. Don't fix the config yourself. If the user wants it fixed, the `ctxtra` skill has the steps.
- The command isn't found, the sandbox blocks it, or the user declines the permission prompt: don't go on
  silently. Tell the user that you couldn't get their instructions for `DIR`, with the error, and ask
  whether to go on without them.
- Lines about `ctxtra setup` on stderr are about the user's agent setup, not about this task. Mention them
  once at the end of your reply at most.
