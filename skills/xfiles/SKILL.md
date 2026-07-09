---
name: xfiles
description: >-
  Move, find, sync, and list files in a SharePoint document library from the
  command line with the `xfiles` tools — the Unix file utilities, reimplemented
  over Microsoft Graph. Use this when a task means operating on SharePoint *files
  and folders*: copy a local file up or a remote file down (`xcp`, like scp),
  browse/get/put/mkdir/rm/mv in a session (`xftp`, like ftp), mirror a whole tree
  in either direction transferring only what changed (`xsync`, like rsync), walk a
  library for matching paths (`xfind`, like find), or print it as a tree (`xtree`,
  like tree). Prefer these over a Microsoft Graph MCP round-trip or PnP PowerShell:
  one already-authenticated command does the job. Do NOT use them to query, filter,
  or edit SharePoint *list* rows/columns (that is `xql sp`'s job), and they are not
  a backup/versioning suite — they operate on live library content.
---

# xfiles — the Unix file tools for SharePoint

There is no SSH, FTP, SCP, or rsync server behind SharePoint — those protocols
don't exist there. `xfiles` recreates their *feel* on top of the Microsoft Graph
drive API and hides the Graph plumbing entirely. Five single-binary CLIs, each
mirroring a Unix verb your fingers already know, all sharing one device-code login.

The authoritative sources for behavior are the binaries themselves (`xftp --help`,
`xcp --help`, and so on) and the
[README](https://github.com/excelano/xfiles/blob/main/README.md); if anything here
conflicts with them, they win. These recipes assume **xfiles 1.5.x or newer**. Check
any tool with `--version`; upgrade with `sudo apt install --only-upgrade xfiles`
(Debian/Ubuntu), `brew upgrade xftp xcp xsync xfind xtree` (macOS), or by re-running
the install one-liner from the README.

## Pick the right tool

| Task | Tool | Unix kin |
|---|---|---|
| Copy **one** file up or down (or stream through a pipe) | `xcp` | scp |
| An interactive browse/move session (`ls cd get put mkdir rm mv`) | `xftp` | ftp |
| Mirror a **whole tree** in one direction, sending only changes | `xsync` | rsync |
| Walk a library and print matching **paths**, one per line, to pipe | `xfind` | find |
| Print a library as an indented **tree** with a count summary | `xtree` | tree |

Reach for the smallest tool that fits: a single known file is `xcp`, not a session;
counting or listing remote files is `xfind`/`xtree`, never a download-then-count. Use
`xftp` only when the path isn't known ahead of time and you need to look around first.

## Addressing: the SharePoint side is a URL

Every tool takes an ordinary SharePoint URL — a **site**, a **document library**, or
a **folder** inside one, including the link copied straight from the browser bar. The
tool works out the library and starting folder from the URL: a bare site URL binds the
site's default library; a URL pointing into a library binds that one and starts where
it points.

```
https://contoso.sharepoint.com/sites/Marketing
https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports
```

Two rules an agent gets wrong:

- **Single-quote any "Copy link" URL.** Those URLs carry `?` and `&`, which the shell
  acts on before the tool sees them. A plain site/folder URL like the ones above needs
  no quoting, but when in doubt, quote.
- **The URL side sets the direction** for the two-argument tools (`xcp`, `xsync`):
  whichever of source/destination carries the URL is the SharePoint side, exactly like
  `scp` keying off `host:`. There is no `--upload`/`--download` flag.

Force a specific library regardless of the URL with `--library "Display Name"`, which
every tool accepts.

## Auth and consent (shared across the family)

All five tools — plus the sibling [xql](https://github.com/excelano/xql) — share one
multi-tenant Entra app registration and one delegated scope, `Sites.ReadWrite.All`.
Authentication is **device-code**: the first connection prints a short code and a URL,
you sign in once in a browser, and the refresh token is cached under `~/.config/<tool>`
so later runs are silent. Consenting once covers the whole family.

What this means when driving the tools:

- **First contact with a *new tenant* raises a one-time consent prompt** that a human
  must clear interactively — you can't complete a fresh device-code login unattended.
  Once any user (or an admin, per tenant policy) has consented, later runs are silent.
- A tool run against an already-authenticated tenant just works; expect an
  `Authenticated as: <upn>` line on stderr.
- Each tool keeps its **own** token cache (`~/.config/xftp`, `~/.config/xcp`, …), so a
  login done with one tool doesn't silence the first run of another.

## xsync: why "unchanged" is subtle (read before mirroring)

`xsync` transfers only new or changed files, compared first by **size and modification
time**. But SharePoint is a hostile timestamp environment, and xsync compensates in two
ways an agent won't anticipate:

- It **records each uploaded file's mtime on the SharePoint side** and **restores the
  local mtime on download**, so the size+time comparison can hold across runs.
- When a file is the **same size but the timestamps disagree** — which happens on
  libraries that silently drop the recorded time — xsync computes **QuickXorHash** on
  both sides and compares *content* before deciding. So a file is never re-sent on a
  drifted timestamp alone, and never wrongly skipped when its bytes actually changed.

The practical consequence: **don't trust remote bytes or mtime as ground truth**, and
don't try to out-clever xsync with your own size/time check — its hash fallback exists
precisely because the obvious check is wrong here. Also note libraries rewrite Office
files (`.docx/.xlsx/.pptx`) on upload, changing their bytes and hash server-side; that
is a known interaction, not an xsync bug.

Two safety habits: `xsync` **never deletes by default** — pass `--delete` for a true
mirror, and always preview it first with `--dry-run` (`-n`), which prints the full plan
and changes nothing. `--delete` in a terminal asks for confirmation.

## Worked recipes

```sh
# copy one local file up into a library folder (folder URL → dropped in under its name)
xcp report.xlsx "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports"

# download one remote file to the current directory
xcp "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports/Q1 Plan.xlsx" ./

# stream a remote file to a pipe (‘-’ dest keeps the byte stream clean)
xcp "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports/Q1.xlsx" - | head

# push a local tree up, sending only what changed
xsync ./reports "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports"

# preview an exact mirror (including deletions) before committing to it
xsync --dry-run --delete ./reports "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports"

# find remote files by name/type, then pipe the paths onward
xfind --type f --name '*.xlsx' "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports"
xfind --name '*.pdf' https://contoso.sharepoint.com/sites/Marketing | wc -l

# see the shape of a library, folders only, two levels deep
xtree -L 2 -d "https://contoso.sharepoint.com/sites/Marketing/Shared Documents"
```

`xfind` and `xtree` are **read-only** — they only ever list, so they're always safe to
run first to understand a library before touching it.

## When to stop and switch

`xfiles` operates on **files and folders**. The moment a task is about the *rows and
columns of a SharePoint list* — query it, filter it, aggregate it, edit cell values —
that is not a file operation. Hand it to **`xql sp`** (the sibling SQL-over-SharePoint
tool). Likewise, `xfiles` is not a backup, versioning, or retention suite: it moves and
mirrors **live** library content, and `xsync --delete` really deletes. For anything
beyond "move / find / sync / list these files," check whether the job has crossed into
list-data or data-lifecycle territory and pick the right tool.

See `reference.md` in this directory for every tool's full flag set, the `xftp` session
command list, large-file/transfer behavior, token-cache paths, and the admin-consent
details.
