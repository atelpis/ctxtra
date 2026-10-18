# @ctxtra-managed
# binary: CTXTRA_BIN
# Written by ctxtra setup. Adds the instruction files and skills that
# ctxtra binds to the session's folder. Safe to delete; `ctxtra setup --remove` removes it.
import json
import os
import subprocess

BIN = "CTXTRA_BIN"


def _cwd():
    # Every session runs in one gateway, whose own folder is another one.
    # When Hermes can't tell the session's folder, it raises, and the
    # plugin adds nothing rather than files bound to the wrong folder.
    from agent.runtime_cwd import resolve_agent_cwd

    return str(resolve_agent_cwd())


def _run(event):
    try:
        out = subprocess.run(
            [BIN, "hook", "hermes", event],
            input=json.dumps({"cwd": _cwd()}),
            capture_output=True,
            text=True,
            timeout=10,
        ).stdout
        context = json.loads(out).get("context")
    except Exception:
        return None
    return {"context": context} if isinstance(context, str) and context else None


def _pre_llm_call(is_first_turn=False, parent_session_id="", **kwargs):
    if not is_first_turn:
        return None
    return _run("subagent" if parent_session_id else "pre_llm_call")


def register(ctx):
    ctx.register_hook("pre_llm_call", _pre_llm_call)
