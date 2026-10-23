---
name: xfiles
description: >-
  Upload, download, mirror, and delete files in a SharePoint document library
  from the command line: `xcp` copies one file, `xsync` mirrors a tree, `xftp`
  runs an FTP-style session, `xfind` lists or searches a location, and `xtree`
  shows its shape. Use it when a file has to be put on SharePoint, pulled to disk
  for another tool, or kept in sync. For SharePoint list rows and columns, use
  `xql sp`.
---

# xfiles

Each command's `--help` and the [README](https://github.com/excelano/xfiles/blob/main/README.md)
are authoritative; where this skill disagrees with them, they win. If `xsync --help`
does not list `--yes`, the installed copy predates the unattended-delete guard described
below, and a `--delete` run with no terminal deletes without asking; upgrade it through
whichever channel installed it. Exact flags, the `xftp` command set, and transfer details
are in `reference.md` beside this file. Read it when you need them.

## When it is the right tool

xfiles writes to SharePoint. It uploads, downloads to disk so another tool can process
the file, mirrors a tree, and deletes, which search connectors and read-only integrations
cannot do. List rows and columns belong to `xql sp`, which shares the sign-in. xfiles works
on live content and is not a backup or versioning tool.

## Finding files

Plain `xfind` walks the live folders below the URL and matches by name, so its answer is
complete and current. `xfind --search 'terms'` asks SharePoint's search index instead and
matches contents and metadata too, which is what "find the document about X" needs; a
Microsoft 365 search tool reads the same index. The index trails uploads, often by many
minutes, and returns what it ranks rather than everything, so look for a file you just
uploaded by name, not by search, and don't read an empty search result as proof that
nothing exists.

## Addressing

The SharePoint side is always a URL: a site, a library, or a folder, including one copied
from the browser. For `xcp` and `xsync`, whichever operand is the URL is the SharePoint
side, so operand order sets the direction. Single-quote any "Copy link" URL, because the
shell acts on its `?` and `&` before the tool sees them.

## Handing back a link

A person who works in the web apps needs a URL, not a library path. `xcp` prints the
uploaded file's browser URL as its only line on stdout, and `xfind --url` prints browser
URLs in place of paths. Give the person that URL.

```sh
xcp report.xlsx 'https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports'
xfind --url --name 'report.xlsx' 'https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports'
```

## Mirroring

`xsync` decides what changed by modification time and settles a same-size file with a
diverged time by QuickXorHash. SharePoint rewrites Office files on upload, so their stored
size never matches the local copy; do not add a size check of your own. `--itemize-changes`
(`-i`) shows why each file was picked, and `--ignore-times` (`-I`) forces a transfer when
an edit left the mtime alone.

## Deleting

`xsync` deletes nothing unless given `--delete`. Run it with `--dry-run` first and show
the person the plan before the real run:

```sh
xsync --dry-run --delete ./reports 'https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports'
xsync --delete --yes ./reports 'https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports'
```

With no terminal on stdin, which is how an agent runs it, `--delete` refuses with exit 2
unless `--yes` is given. Pass `--yes` only once the plan has been approved.

`xftp` reads commands piped on stdin, one per line. `rm` deletes a file at once; a folder
needs `rm -r`, because with no terminal a plain `rm` on a folder refuses. Piped `xftp`
exits 1 if any command failed.

## Signing in

Sign-in is device-code and needs a person at a browser. `xcp auth --json` reports the
shared session without starting a sign-in; branch on `signed_in`. With no terminal, a
command that needs a sign-in exits 1 at once instead of waiting, so ask the person to run
the same command once in their own terminal. The session is shared with the other xfiles
commands and with `xql sp`.
