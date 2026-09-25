#!/usr/bin/env python3
"""eredo relay hook (Claude Code PreToolUse, matcher Bash).

Commands whose program is on the relay list (az, gh, kubectl...) need the
user's logins, which exist only on the host. This hook stops such a Bash call
before it runs, appends it to a queue the host reads with `eredo relay`, and
tells Claude why. Nothing ever runs on the host automatically.

It fails open: on any error it exits 0 without output and the call proceeds
(the command is then simply not found or not logged in). It is a convenience,
not an isolation boundary; the sandbox has no credentials either way.

    eredo-relay-hook                 hook mode, reads the hook JSON on stdin
    eredo-relay-hook --heads CMD     print the programs CMD would run (tests)
"""
import json
import os
import re
import sys
import time

DIR = os.environ.get("EREDO_RELAY_DIR") or os.path.join(
    os.environ.get("CLAUDE_CONFIG_DIR") or os.path.expanduser("~/.claude"), "relay")
NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._+-]*$")
ASSIGN_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*\+?=")
SHELLS = {"sh", "bash", "zsh", "dash", "ksh"}
RESERVED = {"!", "{", "}", "if", "then", "elif", "else", "fi", "do", "done",
            "while", "until", "time", "coproc"}
NOT_COMMANDS = {"for", "select", "case", "function", "[[", "]]"}
# wrapper -> options that take a value
WRAPPERS = {
    "command": set(), "builtin": set(), "exec": {"-a"}, "nohup": set(),
    "nice": {"-n"}, "stdbuf": {"-i", "-o", "-e"}, "noglob": set(),
    "timeout": {"-s", "-k", "--signal", "--kill-after"},
    "env": {"-u", "-C", "--unset", "--chdir", "-S"},
    "xargs": {"-I", "-n", "-L", "-P", "-d", "-s", "-E", "-a", "-i"},
    "sudo": {"-u", "-g", "-h", "-p", "-C", "-D", "-R", "-T", "-U"},
    "doas": {"-u", "-C"},
}


# ---------------------------------------------------------------------------
# command scanner

def _find_close(s, i, close):
    """Index of the ')' or '`' that closes a substitution starting at s[i]."""
    depth, n = 0, len(s)
    while i < n:
        c = s[i]
        if c == "\\":
            i += 2
            continue
        if close == "`":
            if c == "`":
                return i
            i += 1
            continue
        if c == "'":
            j = s.find("'", i + 1)
            i = n if j < 0 else j + 1
            continue
        if c == '"':
            i += 1
            while i < n and s[i] != '"':
                if s[i] == "\\":
                    i += 1
                elif s.startswith("$(", i):
                    i = _find_close(s, i + 2, ")")
                i += 1
            i += 1
            continue
        if c == "(":
            depth += 1
        elif c == ")":
            if depth == 0:
                return i
            depth -= 1
        i += 1
    return n


def _heredoc_end(s, i, delim, strip_tabs):
    """Index after the heredoc body that starts at s[i] (a line start)."""
    n = len(s)
    while i < n:
        j = s.find("\n", i)
        line = s[i:] if j < 0 else s[i:j]
        if (line.lstrip("\t") if strip_tabs else line) == delim:
            return n if j < 0 else j
        if j < 0:
            return n
        i = j + 1
    return n


def heads(cmd, depth=0):
    """Programs (basenames) a shell command line would run, in order."""
    out = []
    if depth > 5:
        return out
    n = len(cmd)
    words, word, in_word = [], [], False
    skip_next = False          # next word is a redirection target
    pending_heredocs = []      # (delimiter, strip_tabs)

    def end_word():
        nonlocal word, in_word, skip_next
        if in_word:
            w = "".join(word)
            if skip_next:
                skip_next = False
            else:
                words.append(w)
        word, in_word = [], False

    def end_command():
        end_word()
        if words:
            out.extend(_simple(words, depth))
        words.clear()

    i = 0
    while i < n:
        c = cmd[i]
        if c == "\\":
            if i + 1 < n and cmd[i + 1] == "\n":
                i += 2
                continue
            if i + 1 < n:
                word.append(cmd[i + 1])
                in_word = True
            i += 2
            continue
        if c == "'":
            j = cmd.find("'", i + 1)
            j = n if j < 0 else j
            word.append(cmd[i + 1:j])
            in_word = True
            i = j + 1
            continue
        if c == '"':
            i += 1
            in_word = True
            while i < n and cmd[i] != '"':
                if cmd[i] == "\\" and i + 1 < n:
                    word.append(cmd[i + 1])
                    i += 2
                    continue
                if cmd.startswith("$(", i) and not cmd.startswith("$((", i):
                    j = _find_close(cmd, i + 2, ")")
                    out.extend(heads(cmd[i + 2:j], depth + 1))
                    i = j + 1
                    continue
                if cmd[i] == "`":
                    j = _find_close(cmd, i + 1, "`")
                    out.extend(heads(cmd[i + 1:j], depth + 1))
                    i = j + 1
                    continue
                word.append(cmd[i])
                i += 1
            i += 1
            continue
        if cmd.startswith("$((", i):
            j = cmd.find("))", i)
            j = n if j < 0 else j + 1
            word.append("0")
            in_word = True
            i = j + 1
            continue
        if cmd.startswith("$(", i) or cmd.startswith("<(", i) or cmd.startswith(">(", i):
            if cmd[i] != "$":
                end_word()
            j = _find_close(cmd, i + 2, ")")
            out.extend(heads(cmd[i + 2:j], depth + 1))
            if cmd[i] == "$":
                word.append("x")
                in_word = True
            i = j + 1
            continue
        if c == "`":
            j = _find_close(cmd, i + 1, "`")
            out.extend(heads(cmd[i + 1:j], depth + 1))
            word.append("x")
            in_word = True
            i = j + 1
            continue
        if c == "#" and not in_word:
            j = cmd.find("\n", i)
            i = n if j < 0 else j
            continue
        if c in "<>" or (c == "&" and i + 1 < n and cmd[i + 1] == ">"):
            # a word of digits right before is a file descriptor, not a word
            if in_word and "".join(word).isdigit():
                word, in_word = [], False
            end_word()
            if cmd.startswith("<<<", i):
                i += 3
                skip_next = True
                continue
            if cmd.startswith("<<", i):
                i += 2
                strip = False
                if i < n and cmd[i] == "-":
                    strip, i = True, i + 1
                while i < n and cmd[i] in " \t":
                    i += 1
                m = re.match(r"""(['"]?)([^\s;&|<>()'"]+)\1""", cmd[i:])
                if m:
                    pending_heredocs.append((m.group(2), strip))
                    i += m.end()
                continue
            j = i + 1
            while j < n and cmd[j] in "<>&|":
                j += 1
            op = cmd[i:j]
            i = j
            # >&2, <&0, >&- duplicate a descriptor: no filename follows
            if op.endswith("&") and i < n and (cmd[i].isdigit() or cmd[i] == "-"):
                while i < n and (cmd[i].isdigit() or cmd[i] == "-"):
                    i += 1
                continue
            skip_next = True
            continue
        if c == "\n":
            end_command()
            i += 1
            for delim, strip in pending_heredocs:
                i = _heredoc_end(cmd, i, delim, strip) + 1
            pending_heredocs = []
            continue
        if c in ";&|()":
            end_command()
            i += 1
            continue
        if c in " \t":
            end_word()
            i += 1
            continue
        word.append(c)
        in_word = True
        i += 1
    end_command()
    return out


def _simple(words, depth):
    """Heads of one simple command, given its words."""
    i, n = 0, len(words)
    while i < n:
        w = words[i]
        if w in NOT_COMMANDS:
            return []
        if w in RESERVED or ASSIGN_RE.match(w):
            i += 1
            continue
        name = os.path.basename(w)
        if name in WRAPPERS:
            takes = WRAPPERS[name]
            i += 1
            if name == "command" and i < n and words[i] in ("-v", "-V"):
                return []
            while i < n and words[i].startswith("-") and words[i] != "-":
                opt = words[i]
                i += 1
                if opt in takes and "=" not in opt:
                    i += 1
            if name == "env":
                while i < n and ASSIGN_RE.match(words[i]):
                    i += 1
            if name == "timeout" and i < n:
                i += 1  # the duration
            if name == "nice" and i < n and re.match(r"^-?\d+$", words[i]):
                i += 1
            continue
        found = [name]
        rest = words[i + 1:]
        if name in SHELLS:
            for k, a in enumerate(rest):
                if a.startswith("-") and not a.startswith("--") and "c" in a[1:]:
                    if k + 1 < len(rest):
                        found += heads(rest[k + 1], depth + 1)
                    break
                if not a.startswith("-"):
                    break
        elif name == "eval":
            found += heads(" ".join(rest), depth + 1)
        return found
    return []


# ---------------------------------------------------------------------------
# hook

def relay_list():
    try:
        with open(os.path.join(DIR, "commands")) as f:
            text = f.read()
    except OSError:
        return []
    names = []
    for line in text.splitlines():
        line = line.split("#", 1)[0]
        for w in line.split():
            if NAME_RE.match(w) and w not in names:
                names.append(w)
    return names


def watcher_active():
    try:
        with open(os.path.join(DIR, "watching")) as f:
            parts = f.read().split()
        return len(parts) == 2 and parts[0] == "copy" and time.time() - int(parts[1]) < 5
    except (OSError, ValueError):
        return False


def enqueue(entry):
    """Append entry to the queue; return its 1-based number."""
    os.makedirs(DIR, exist_ok=True)
    path = os.path.join(DIR, "queue.jsonl")
    with open(path, "a+") as f:
        try:
            import fcntl
            fcntl.flock(f, fcntl.LOCK_EX)
        except (ImportError, OSError):
            pass
        f.seek(0)
        lines = [l for l in f.read().splitlines() if l.strip()]
        if lines:
            try:
                last = json.loads(lines[-1])
                if last.get("session") == entry["session"] and last.get("command") == entry["command"]:
                    return len(lines)  # an immediate retry: same entry
            except ValueError:
                pass
        f.write(json.dumps(entry, separators=(",", ":")) + "\n")
        return len(lines) + 1


def reason(n, cmds, cwd):
    project = os.environ.get("CLAUDE_PROJECT_NAME", "")
    tool = " ".join(cmds)
    if watcher_active():
        clip = "the `eredo relay --watch` running on the host has copied it to their clipboard"
    else:
        target = (project + " ") if project else ""
        clip = ("in a host terminal, `eredo relay %s--copy %d` copies it to the clipboard "
                "(`eredo relay %s` lists the queue)" % (target, n, project)).replace("  ", " ").replace(" `)", "`)")
    return (
        "eredo relay: not run. `%s` needs the user's credentials, which exist only on the host; "
        "this sandbox has none. Nothing in this Bash call ran, including any sandbox steps in it. "
        "The command is queued for the user as #%d.\n\n"
        "Tell the user, briefly:\n"
        "- it is queued as #%d; %s\n"
        "- to run it on the host in %s and paste the output here if you need it.\n\n"
        "Then:\n"
        "- Do not try to run it here another way (sh -c, bash -c, eval, a full path, a script, an alias, "
        "installing the CLI). It cannot work here, and the relay catches those forms too.\n"
        "- If you need the output, stop and wait for the user to paste it; do not guess it. "
        "Otherwise carry on with work that does not depend on it.\n"
        "- Keep sandbox steps in their own Bash calls, so a queued command is exactly what the user should run.\n"
        "- If the user wants this command to run inside the sandbox instead, it is on the relay list "
        "(relay.txt in the host's eredo config; `eredo config show` prints where): they remove it there "
        "and run `eredo reload`."
    ) % (tool, n, n, clip, cwd)


def main():
    names = relay_list()
    if not names:
        return
    data = json.load(sys.stdin)
    cmd = (data.get("tool_input") or {}).get("command") or ""
    if not cmd or len(cmd) > 65536:
        return
    pre = re.compile(r"(^|[^A-Za-z0-9_.+-])(" + "|".join(map(re.escape, names)) + r")([^A-Za-z0-9_.+-]|$)")
    if not pre.search(cmd):
        return
    found = [h for h in heads(cmd) if h in names]
    matched = list(dict.fromkeys(found))
    if not matched:
        return
    cwd = data.get("cwd") or os.getcwd()
    entry = {
        "v": 1,
        "id": data.get("tool_use_id") or "t%d" % int(time.time()),
        "time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "session": data.get("session_id") or "",
        "cwd": cwd,
        "cmds": matched,
        "command": cmd,
    }
    n = enqueue(entry)
    print(json.dumps({"hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "permissionDecision": "deny",
        "permissionDecisionReason": reason(n, matched, cwd),
    }}))


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--heads":
        print("\n".join(heads(sys.argv[2])))
        sys.exit(0)
    try:
        main()
    except Exception:  # fail open
        pass
    sys.exit(0)
