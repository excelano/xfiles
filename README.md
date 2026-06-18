# xfiles

xfiles is a family of command-line tools that give a SharePoint document library the feel of the Unix file utilities your fingers already know. There is no SSH, FTP, SCP, or rsync server behind SharePoint to connect to — those protocols simply don't exist there — so each tool recreates the experience on top of the Microsoft Graph drive API and hides the Graph plumbing entirely.

| Tool | Unix kin | What it does |
|---|---|---|
| `xftp` | ftp | Interactive session: browse and move files with `ls`, `cd`, `get`, `put`, `mkdir`, `rm`, `mv`. |
| `xcp` | scp | One-shot copy of a single file to or from a library; streams through `-` for pipes. |
| `xsync` | rsync | Recursively mirror a directory tree to or from a library, transferring only what changed. |
| `xfind` | find | Walk a library and print matching paths, filtered by name, type, or depth. |
| `xtree` | tree | Print a library as an indented tree with a directory and file count. |

Each is a single static Go binary with no daemon and no mounted filesystem. Authentication is device-code OAuth shared across the whole suite: the first time a tool connects it prints a short code and a URL, you sign in once in a browser, and the refresh token is cached under `~/.config` so later runs are silent. All five share one app registration, so a single consent covers the family (and the sibling tool [xql](https://github.com/excelano/xql)).

## Install

On Debian or Ubuntu, add the [Excelano apt repository](https://excelano.com/apt/) once, then install the whole suite as a single metapackage so `apt upgrade` keeps everything current:

```sh
curl -fsSL https://excelano.com/apt/setup.sh | sudo sh
sudo apt install xfiles
```

`xfiles` is a metapackage that pulls in all five tools. To install just one, name it instead — `sudo apt install xsync`, for example.

On other platforms, the install script fetches prebuilt binaries (Linux and macOS, x86_64 and arm64) and drops all five into one directory:

```
curl -fsSL https://raw.githubusercontent.com/excelano/xfiles/main/install.sh | sh
```

If the installer needs to write to a root-owned directory like `/usr/local/bin`, wrap `sh`, not `curl`:

```
curl -fsSL https://raw.githubusercontent.com/excelano/xfiles/main/install.sh | sudo sh
```

Pin a version with `XFILES_VERSION=v1.5.0`, or install elsewhere with `XFILES_INSTALL_DIR=$HOME/bin`. To uninstall, run the matching `uninstall.sh` the same way, which removes all five binaries.

From source (Go 1.24 or later):

```
go install github.com/excelano/xfiles/cmd/xftp@latest
go install github.com/excelano/xfiles/cmd/xcp@latest
go install github.com/excelano/xfiles/cmd/xsync@latest
go install github.com/excelano/xfiles/cmd/xfind@latest
go install github.com/excelano/xfiles/cmd/xtree@latest
```

## Pointing at a library

Every tool takes a SharePoint URL — a site, a document library, or a folder, including the link you copy straight from the browser address bar.

```
https://contoso.sharepoint.com/sites/Marketing
https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports
```

Wrap the URL in single quotes if it contains `?` or `&`, which the "Copy link" button's URLs always do — otherwise the shell splits the command on the `&` before the tool ever sees it. A plain site or folder URL like the ones above needs no quoting.

The library is worked out from the URL. A bare site URL binds the site's default document library. A URL that points into a specific library binds that one, and where it points at a folder within the library, the tool starts there. To force a particular library regardless of the URL, name it by its display name with `--library`, which every tool accepts:

```
xftp --library "Project Files" https://contoso.sharepoint.com/sites/Marketing
```

## xftp — interactive sessions

`xftp` connects to a library and drops you at a prompt that shows your position, where you move files with the verbs your fingers already know:

```
xftp:/> ls
xftp:/> cd Reports
xftp:/Reports> get "Q1 Plan.xlsx"
xftp:/Reports> put report.pdf Archive/report.pdf
```

Paths may be relative to the current folder or absolute with a leading `/`, and `.`/`..` work as you'd expect. Names containing spaces can be quoted (`"Phase 2"` or `'Phase 2'`) or escaped (`Phase\ 2`), the same way you would in a shell.

| Command | What it does |
|---|---|
| `ls [path]` | List a remote folder. Defaults to the current folder. |
| `cd [path]` | Change remote folder. With no argument, prints the current folder. |
| `pwd` | Print the current remote folder. |
| `get <remote> [local]` | Download a file. Defaults the local name to the remote's. |
| `put <local> [remote]` | Upload a file. Files over 250 MB upload in chunks, with progress. Defaults the remote name to the local's. |
| `mkdir <path>` | Create a remote folder. |
| `rm <path>` | Delete a file. Folders are recursive, so they prompt for confirmation first. |
| `mv <src> <dst>` | Move or rename a remote item. |
| `lcd [dir]` | Change the local working folder for `get`/`put`. With no argument, prints it. |
| `lpwd` | Print the local working folder. |
| `lls [dir]` | List a local folder. |
| `help` | Show the command list. |
| `quit` | Exit. |

Deleting a single file goes straight through, since SharePoint routes it to the recycle bin and it can be recovered there. Deleting a folder is recursive and irreversible from xftp's side, so it asks first.

## xcp — one-shot copies

When you just need to move a single file and don't want a session, use `xcp`. It mirrors `scp`: two arguments, a source and a destination, where exactly one of them is a SharePoint URL. Which side carries the URL decides the direction, the same way `scp` keys off which side carries `host:`.

Upload a local file to a library folder:

```
xcp report.xlsx "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports"
```

Download a file from a library to the current directory:

```
xcp "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports/Q1 Plan.xlsx" ./
```

The destination follows `cp`/`scp` habits. On upload, a URL that points at a folder copies the file into it under its own name, a URL that points at an existing file overwrites it, and any other path is taken as the new name. On download, a destination that is an existing directory receives the file under its remote name, and otherwise the destination is the path to write.

Use `-` as the local side to stream instead of naming a file. A `-` destination cats the remote file to stdout, which keeps the byte stream clean for piping; a `-` source uploads from stdin, in which case the URL must name the target file since stdin has no name of its own:

```
xcp "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports/Q1.xlsx" - | in2csv | head
generate-report | xcp - "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports/report.csv"
```

Recursive directory copies aren't part of xcp — that job belongs to `xsync` below, which moves whole trees and transfers only what changed. xcp keeps its own token cache under `~/.config/xcp`.

## xsync — recursive mirror

`xsync` mirrors a whole directory tree between the local filesystem and a library, the way `rsync` does. Two arguments, a source and a destination, exactly one of them a SharePoint URL; which side carries the URL sets the direction.

Push a local folder up to a library:

```
xsync ./reports "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports"
```

Pull a library folder down to a local directory:

```
xsync "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports" ./reports
```

Only files that are new or changed are transferred, compared by size and modification time, so a second run with nothing changed transfers nothing. To make that comparison hold across runs, xsync records each uploaded file's modification time on the SharePoint side and restores the local modification time on download, so unchanged files aren't re-sent.

By default xsync only adds and updates; it never deletes. Pass `--delete` to make the destination an exact mirror, removing items that no longer exist in the source — when run in a terminal it asks for confirmation first. Pass `--dry-run` (`-n`) to print the full plan and change nothing, which is the safe way to preview a `--delete`:

```
xsync --dry-run --delete ./reports "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports"
xsync --delete ./reports "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports"
```

xsync keeps its own token cache under `~/.config/xsync`.

## xfind and xtree — recursive listing

Where `ls` shows one folder, `xfind` and `xtree` walk a whole library. Both take a single SharePoint URL — site, library, or folder — and recurse from there, the same URL shapes the other tools accept, copy links included. Both are read-only: they only ever list.

`xfind` prints one path per line, relative to the folder the URL points at, the way `find` prints paths relative to its starting directory. With no flags it lists everything; `--name` (or case-insensitive `--iname`) filters by a glob on the file or folder name, `--type f` or `--type d` restricts to files or folders, and `--maxdepth` limits how deep it descends.

```
xfind https://contoso.sharepoint.com/sites/Marketing
xfind --type f --name '*.xlsx' "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports"
xfind --type d --maxdepth 2 https://contoso.sharepoint.com/sites/Marketing
```

Because the output is plain paths on stdout, it pipes into the usual tools — `xfind --name '*.pdf' <url> | wc -l` counts the PDFs in a library.

`xtree` prints the same walk as an indented tree and finishes with a `N directories, M files` summary. `-L` caps the depth shown and `-d` lists folders only.

```
xtree https://contoso.sharepoint.com/sites/Marketing
xtree -L 2 -d "https://contoso.sharepoint.com/sites/Marketing/Shared Documents"
```

Each keeps its own token cache (`~/.config/xfind`, `~/.config/xtree`).

## Authentication and tenants

The suite authenticates through a multi-tenant Azure app registration ("Excelano SharePoint tools"), shared across `xftp`, `xcp`, `xsync`, `xfind`, `xtree`, and the sibling tool [xql](https://github.com/excelano/xql), so consenting once covers them all. Pointing a tool at another organization's site uses that same registration — nobody sets up their own. The first connection to a new tenant raises a one-time consent prompt; depending on that tenant's policy, either the user or an administrator clears it, after which everyone in the tenant is covered. The single scope requested is `Sites.ReadWrite.All`. If your organization restricts user consent, [ADMINS.md](ADMINS.md) has everything your IT department needs to review and approve the application.

To use your own app registration instead, change `defaultClientID` in `internal/spauth/auth.go` and rebuild.

## Large files and transfers

Files up to 250 MB upload in a single request. Above that, the transfer opens a Graph upload session and streams the file in 10 MiB chunks. Downloads stream straight to disk as well, into a temporary file that's renamed into place only once the transfer completes, so an interrupted download never leaves a corrupt file at the real name. Either direction reads or writes directly to disk rather than buffering the whole file in memory, so transfer size is bounded by the library's quota and your local disk, not by RAM.

Transfers over 50 MB print a progress line. Ctrl-C interrupts a transfer in progress and cleans up after itself: a partial download is discarded, and an aborted upload session is cancelled on the server.

## Building

```
go build -o xftp ./cmd/xftp
go build -o xcp ./cmd/xcp
go build -o xsync ./cmd/xsync
go build -o xfind ./cmd/xfind
go build -o xtree ./cmd/xtree
```

---

Built by David M. Anderson, with AI assistance.
