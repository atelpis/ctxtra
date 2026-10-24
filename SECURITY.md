# Security

Report a vulnerability privately, with GitHub's
[private vulnerability reporting](https://github.com/atelpis/ctxtra/security/advisories/new), not in an
issue. ctxtra is maintained by one person: I aim to answer within a week, and to fix confirmed problems
in the next release.

Only the latest release gets fixes.

Examples of what to report:

- a package (a source added with `ctxtra source add`) that gets ctxtra to read, bind or show a file
  outside its own repository, or to run anything;
- a way around `ctxtra source trust`, or around the review in `ctxtra source update`;
- text from a package or a repo that reaches your terminal as escape sequences;
- a change that `ctxtra setup` makes outside its own hook entries, or without a backup.

Not a vulnerability: the text of a package you added reaching your agent. That's what a package does, so
read one before you add it (README, "Share packages with your team").
