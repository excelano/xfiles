# xfiles reference

Complete reference for the `xfiles` commands. Load this when `SKILL.md` isn't
specific enough — per-tool flags, the `xftp` session command set, transfer and
large-file behavior, auth/consent details, and token-cache locations.

Every tool takes a SharePoint URL (site, library, folder, or file), a OneDrive for work
or school URL included. A file's Office for the web address (`_layouts/15/Doc.aspx?...`)
binds that file; a URL that names nothing on the site is an error. Every tool accepts
`--library "Display Name"` to force a library regardless of the URL, and the standard
`--version` / `-V` and `--help` / `-h`. Single-quote any "Copy link" URL — its `?`/`&`
are shell metacharacters.

## xcp — one-shot copy (scp)

```
xcp [flags] <source> <destination>
```

Exactly one of source/destination is a SharePoint URL; that side sets the direction.

| Behavior | Rule |
|---|---|
| Upload dest is a **folder** URL | file copied in under its own name |
| Upload dest is an **existing file** URL | that file is overwritten |
| Upload dest is any other path | taken as the new remote name |
| Download dest is an **existing directory** | file written there under its remote name |
| Download dest is any other path | taken as the path to write |
| `-` as the **destination** | remote file cat'd to stdout (clean byte stream for pipes) |
| `-` as the **source** | upload from stdin — the URL must name the target file (stdin has no name) |

An upload prints the stored file's browser URL as the one line on stdout; the
`src -> dst` status line and any progress go to stderr. No recursive copy — that's
`xsync`.

## xftp — interactive session (ftp)

```
xftp [--library NAME] <url>
```

Drops you at a prompt showing your remote position. Paths may be relative to the
current folder or absolute (leading `/`); `.` and `..` work; names with spaces are
quoted (`"Phase 2"`, `'Phase 2'`) or escaped (`Phase\ 2`) as in a shell.

| Command | Effect |
|---|---|
| `ls [path]` (alias `dir`) | list a remote folder (default: current) |
| `cd [path]` | change remote folder; **no arg prints the current folder** |
| `pwd` | print the current remote folder |
| `get <remote> [local]` | download; local name defaults to the remote's |
| `put <local> [remote]` | upload; remote name defaults to the local's; >250 MB chunks with progress |
| `mkdir <path>` | create a remote folder |
| `rm [-r] <path>` (alias `delete`) | delete a file (→ recycle bin); a **folder** asks first, or goes straight through with `-r` |
| `mv <src> <dst>` (alias `rename`) | move or rename a remote item |
| `lcd [dir]` | change the local working dir for get/put; no arg prints it |
| `lpwd` | print the local working dir |
| `lls [dir]` | list a local folder |
| `help` (alias `?`) | command list |
| `quit` (aliases `exit`, `bye`) | exit |

Commands can be piped on stdin, one per line, and xftp exits 1 if any of them failed. A
folder `rm` is recursive; in a terminal it asks first, and with no terminal it refuses
unless given as `rm -r`, so the answer is never read from the next line of the script.

## xsync — recursive mirror (rsync)

```
xsync [flags] <source> <destination>
```

Exactly one side is a URL; that side sets direction. Only new/changed files transfer.

| Flag | Meaning |
|---|---|
| `--dry-run`, `-n` | print the full plan, change nothing (the safe way to preview `--delete`) |
| `--delete` | make the destination an exact mirror, removing what's gone from the source; asks in a terminal, and with no terminal refuses (exit 2) unless `--yes` |
| `--yes`, `-y` | apply `--delete` without asking |
| `--library <NAME>` | force the library |
| `--ignore-times`, `-I` | transfer every file, skipping the timestamp comparison |
| `--itemize-changes`, `-i` | label each line with why it was picked: `new`, `time`, `content`, `forced` |
| `-V` / `--version`, `-h` / `--help` | standard |

**Change detection:** compare by **mtime**; size is consulted only after the timestamp
has moved. Libraries rewrite Office files on upload so their stored size and hash never
match the source again — a size test would re-send every `.docx/.xlsx/.pptx` forever.
xsync records each uploaded file's mtime on the SharePoint side and restores the local
mtime on download so the comparison holds across runs; a browser edit moves that
timestamp too, so remote changes are still caught. On a **same-size,
disagreeing-timestamp** file it falls back to comparing **QuickXorHash** on both sides.
The gap: contents changing without the mtime moving reads as unchanged — use
`--ignore-times`. Default is add/update only; deletion requires `--delete`.

**Case, when uploading.** SharePoint preserves case but does not distinguish by it, so
`D1` and `d1` are one folder there. Uploads compare with case folded: a local `d1/`
matching an existing `D1/` is the same folder, and `--delete` will not remove a
destination item a source path matches under another spelling. Two local paths differing
only by case are a reported conflict, since the library can hold only one. Downloads stay
case-sensitive, matching the local filesystem.

## xfind — recursive path listing (find)

```
xfind [flags] <url>
```

Prints one path per line, **relative to the folder the URL points at**, on stdout — so
it pipes into `wc`, `grep`, `xargs`, etc. Read-only.

| Flag | Meaning |
|---|---|
| `--name <glob>` | print only names matching the glob (e.g. `'*.xlsx'`) |
| `--iname <glob>` | like `--name`, case-insensitive (mutually exclusive with `--name`) |
| `--type f` \| `--type d` | restrict to files or folders |
| `--maxdepth <n>` | descend at most `n` folder levels (`0` = unlimited) |
| `--url` | print each match's browser URL instead of its path |
| `--search <terms>` | ask the search index instead of walking; matches names, contents, and metadata |
| `--library <NAME>` | force the library |

Without `--search`, xfind walks the folders below the URL and matches by name, so the
result is complete and current. With it, xfind queries SharePoint's search index for the
library, keeps only hits below the URL's folder, and prints them in path order; `--name`,
`--iname`, `--type`, `--maxdepth`, and `--url` still apply. The index trails uploads,
often by many minutes and unevenly from file to file, and returns what it ranks rather
than a complete listing, so an empty result is not proof of absence. `--search ''` is a
bad invocation (exit 2).

A walk with no depth limit fetches everything below the URL in a few requests, so a
large tree costs seconds; a `--maxdepth` (or xtree `-L`) walk lists one folder per
request instead, which is quicker only while the limit stays shallow.

## xtree — indented tree (tree)

```
xtree [flags] <url>
```

Prints the walk as an indented tree with `├──`/`└──` guides, then an
`N directories, M files` summary (`N directories` alone under `-d`). Read-only.

| Flag | Meaning |
|---|---|
| `-L <n>` | show at most `n` levels (`0` = unlimited) |
| `-d` | folders only |
| `--library <NAME>` | force the library |

## Invocation

- **Flag position is free.** Flags are read wherever they appear, so `xfind <url> --name
  '*.xlsx'` and `xfind --name '*.xlsx' <url>` are the same command. Operand order still
  carries meaning for `xcp` and `xsync`, where the URL side sets the direction.
- **`--` ends flag parsing**, which is how a local path beginning with a dash is passed.
- **`--help`, `-h`, `--version`, `-V`** print to stdout and exit 0. The version line
  names its tool so a log mixing several stays readable.
- **Exit codes:** `0` success, `1` bad input (unreachable site, missing file, incomplete
  sign-in, failed transfer), `2` bad invocation (unknown flag, wrong argument count,
  contradictory options). A walk that matches nothing is 0, not 1.

## Auth, consent, and tenants

- **Flow:** device-code OAuth against a multi-tenant Entra app registration ("Excelano
  SharePoint tools"), client ID `13be0775-ed76-4407-bb2c-b7a07a189bf6`, shared with
  [xql](https://github.com/excelano/xql). One consent covers the whole family.
- **Scope:** a single delegated scope, `Sites.ReadWrite.All`, covering both metadata and
  file-content operations. (MSAL appends `openid`/`offline_access`/`profile`
  automatically.)
- **First contact with a new tenant** raises a one-time consent prompt a human clears
  interactively — either the user or an admin, per that tenant's policy — after which
  everyone in the tenant is covered and runs go silent. You cannot complete a fresh
  device-code login unattended.
- **Unattended behavior:** when a sign-in is needed and stderr is not a terminal, the
  tool exits 1 before requesting a device code, with `no cached token, and no terminal
  is attached to complete device-code sign-in`. Only the device-code path is gated — a
  cached refresh token still renews silently under cron or an agent, which is why the
  remedy is to run the command once interactively and then re-run it unattended.
- **One shared cache:** every tool, and `xql sp`, reads and writes
  `~/.config/excelano/sp-token.json` (`XDG_CONFIG_HOME` respected), so a login with any
  one of them covers the rest. A per-tool cache at `~/.config/<tool>/sp-token.json` is
  adopted on first run: its contents are stored at the shared path, and the per-tool
  file is deleted once the shared copy is confirmed encrypted. Where no encryption
  backend is reachable, it is left in place for the uninstaller's purge step to remove.
- **State command:** `<tool> auth [--json]` reports that session (account, tenant, token
  expiry, scopes) and exits 0 whether or not one exists. Exit 1 only when the state could
  not be determined (cache unreadable, sign-in server unreachable); 2 for a bad
  invocation. It never starts a sign-in.
- To self-host with your own app registration, change the `ClientID` of
  `graphauth.SharePoint` in the [graphauth](https://github.com/excelano/graphauth)
  module and rebuild. Admin-consent guidance for IT is in
  [ADMINS.md](https://github.com/excelano/xfiles/blob/main/ADMINS.md).

## Transfers and large files

- Files up to **250 MB** upload in a single request; above that, an upload session
  streams the file in 10 MiB chunks. Downloads stream straight to a temp file that's
  renamed into place only on completion — an interrupted download never leaves a corrupt
  file at the real name.
- Either direction reads/writes directly to disk, so transfer size is bounded by quota
  and local disk, **not RAM**.
- Transfers over **50 MB** print a progress line. **Ctrl-C** cleans up: a partial
  download is discarded and an aborted upload session is cancelled server-side.

## The boundary

`xfiles` is **file-and-folder** operations over a document library. It is **not**:

- **SharePoint list query/edit** — rows, columns, filtering, aggregation, cell edits →
  `xql sp`.
- **A backup/versioning/retention suite** — it moves and mirrors *live* content;
  `xsync --delete` deletes for real. Preview with `--dry-run` first.

When a task crosses from "move/find/sync/list these files" into list data or data
lifecycle, switch tools.
