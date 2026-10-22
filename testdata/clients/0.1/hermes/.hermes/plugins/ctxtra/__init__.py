# @ctxtra-managed
# binary: $HOME/bin/ctxtra
# protocol: 3
# Written by ctxtra setup. Adds the instruction files and skills that
# ctxtra binds to the session's folder. Safe to delete; `ctxtra setup --remove` removes it.
import json
import os
import select
import signal
import subprocess
import time

BIN = os.path.join(os.path.expanduser("~"), "bin/ctxtra")

# Hermes cuts a callback's text above hooks.output_spill.max_chars, so ctxtra
# gives the text in parts, and callback K adds part K. A session's parts are
# kept from its first callback to its last.
SLOTS = 125
TIMEOUT = 10
MAX_OUTPUT = 64 * 1024 * 1024
_parts = {}


def _cwd():
    # Every session runs in one gateway, whose own folder is another one.
    # When Hermes can't tell the session's folder, it raises, and the
    # plugin adds nothing rather than files bound to the wrong folder.
    from agent.runtime_cwd import resolve_agent_cwd

    return str(resolve_agent_cwd())


def _run(event):
    try:
        data = json.dumps(
            {"cwd": _cwd(), "ctxtra_plugin": 3, "ctxtra_slots": SLOTS}, ensure_ascii=False
        ).encode("utf-8")
        proc = subprocess.Popen(
            [BIN, "hook", "hermes", event],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
    except Exception:
        return []
    deadline = time.monotonic() + TIMEOUT
    try:
        # A binary that doesn't read its input, or a process that it started
        # and that keeps stdout open, can block a write or a read: the
        # deadline holds for each.
        in_fd, out_fd = proc.stdin.fileno(), proc.stdout.fileno()
        os.set_blocking(in_fd, False)
        sent = 0
        out = bytearray()
        while True:
            left = deadline - time.monotonic()
            if left <= 0:
                return []
            writing = [] if proc.stdin.closed else [in_fd]
            readable, writable, _ = select.select([out_fd], writing, [], left)
            if not readable and not writable:
                return []
            if writable:
                try:
                    sent += os.write(in_fd, data[sent:])
                except BlockingIOError:
                    pass
                except BrokenPipeError:
                    sent = len(data)
                if sent >= len(data):
                    proc.stdin.close()
            if readable:
                b = os.read(out_fd, 1 << 16)
                if not b:
                    break
                out += b
                if len(out) > MAX_OUTPUT:
                    return []
        if proc.wait(timeout=max(deadline - time.monotonic(), 0.001)) != 0:
            return []
        parts = json.loads(bytes(out)).get("chunks")
    except Exception:
        return []
    finally:
        try:
            proc.stdin.close()
        except Exception:
            pass
        # Before the binary is reaped, its process group can't be another's.
        if proc.returncode is None:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except Exception:
                pass
            proc.wait()
        proc.stdout.close()
    if not isinstance(parts, list) or not all(isinstance(p, str) for p in parts):
        return []
    return parts


def _callback(k):
    def _pre_llm_call(is_first_turn=False, parent_session_id="", session_id="", **kwargs):
        if not is_first_turn:
            return None
        if k == 0:
            _parts[session_id] = _run("subagent" if parent_session_id else "pre_llm_call")
        parts = _parts.pop(session_id, []) if k == SLOTS - 1 else _parts.get(session_id, [])
        return {"context": parts[k]} if k < len(parts) and parts[k] else None

    return _pre_llm_call


def register(ctx):
    for k in range(SLOTS):
        ctx.register_hook("pre_llm_call", _callback(k))
