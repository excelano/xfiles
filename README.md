# xftp

xftp gives a SharePoint document library the feel of an FTP session. You connect to a site, land at an interactive prompt, and move files around with the verbs your fingers already know: `ls`, `cd`, `get`, `put`, `mkdir`, `rm`, `mv`. There is no SSH, FTP, or SCP server behind SharePoint to connect to — those protocols simply don't exist there — so xftp recreates the experience on top of the Microsoft Graph drive API and hides the Graph plumbing entirely.

It is a single static Go binary with no daemon and no mounted filesystem. Authentication is device-code OAuth: the first time you connect, xftp prints a short code and a URL, you sign in once in a browser, and the refresh token is cached under `~/.config/xftp` so later runs are silent.

The repository also ships **xcp**, an scp-style companion for one-shot, non-interactive copies — a single command to move one file to or from a library without entering a session. It is the right tool for scripts and quick transfers; xftp is the right tool for browsing and working interactively. The two share the same engine and the same sign-in. See [One-shot copies with xcp](#one-shot-copies-with-xcp) below.

## Install

Prebuilt binary (Linux and macOS, x86_64 and arm64):

```
curl -fsSL https://raw.githubusercontent.com/excelano/xftp/main/install.sh | sh
```

If the installer needs to write to a root-owned directory like `/usr/local/bin`, wrap `sh`, not `curl`:

```
curl -fsSL https://raw.githubusercontent.com/excelano/xftp/main/install.sh | sudo sh
```

Pin a version with `XFTP_VERSION=v1.0.0`, or install elsewhere with `XFTP_INSTALL_DIR=$HOME/bin`.

The installer drops both `xftp` and `xcp` into the same directory.

On Debian or Ubuntu, install from the [Excelano apt repository](https://excelano.com/apt/) instead, so `apt upgrade` keeps them current:

```sh
curl -fsSL https://excelano.com/apt/setup.sh | sudo sh
sudo apt install xftp xcp
```

From source (Go 1.24 or later):

```
go install github.com/excelano/xftp/cmd/xftp@latest
go install github.com/excelano/xftp/cmd/xcp@latest
```

To uninstall, run `curl -fsSL https://raw.githubusercontent.com/excelano/xftp/main/uninstall.sh | sh`, which removes both binaries.

## Connecting

Pass a SharePoint URL as the only argument. It can be a site, a document library, or a folder — including the link you copy straight from the browser address bar.

```
xftp https://contoso.sharepoint.com/sites/Marketing
xftp https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports
```

xftp works out which library to bind from the URL. A bare site URL binds the site's default document library. A URL that points into a specific library binds that one, and if it points at a folder within the library, xftp drops you straight into that folder. To force a particular library regardless of the URL, name it by its display name:

```
xftp --library "Project Files" https://contoso.sharepoint.com/sites/Marketing
```

Once connected you're at the prompt, which shows your position in the library:

```
xftp:/> ls
xftp:/> cd Reports
xftp:/Reports> get "Q1 Plan.xlsx"
xftp:/Reports> put report.pdf Archive/report.pdf
```

Paths may be relative to the current folder or absolute with a leading `/`, and `.`/`..` work as you'd expect. Names containing spaces can be quoted (`"Phase 2"` or `'Phase 2'`) or escaped (`Phase\ 2`), the same way you would in a shell.

## Commands

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

## One-shot copies with xcp

When you just need to move a single file and don't want a session, use `xcp`. It mirrors `scp`: two arguments, a source and a destination, where exactly one of them is a SharePoint URL. Which side carries the URL decides the direction, the same way `scp` keys off which side carries `host:`.

Upload a local file to a library folder:

```
xcp report.xlsx "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports"
```

Download a file from a library to the current directory:

```
xcp "https://contoso.sharepoint.com/sites/Marketing/Shared Documents/Reports/Q1 Plan.xlsx" ./
```

The destination follows `cp`/`scp` habits. On upload, a URL that points at a folder copies the file into it under its own name, a URL that points at an existing file overwrites it, and any other path is taken as the new name. On download, a destination that is an existing directory receives the file under its remote name, and otherwise the destination is the path to write. The `--library` flag works as it does in xftp, and the same copy-link URLs are understood, so you can paste straight from SharePoint's "Copy link" button.

xcp authenticates exactly like xftp and through the same app registration, but it keeps its own token cache under `~/.config/xcp`, so the first run signs in once of its own. Recursive directory copies (`-r`) aren't supported yet; xcp moves one file per invocation.

## Authentication and tenants

xftp authenticates through a multi-tenant Azure app registration ("Excelano SharePoint tools"), shared with `xcp` and with the sibling tool [xql](https://github.com/excelano/xql), so consenting once covers all three. Pointing xftp at another organization's site uses that same registration — nobody sets up their own. The first connection to a new tenant raises a one-time consent prompt; depending on that tenant's policy, either the user or an administrator clears it, after which everyone in the tenant is covered. The single scope requested is `Sites.ReadWrite.All`. If your organization restricts user consent, [ADMINS.md](ADMINS.md) has everything your IT department needs to review and approve the application.

To use your own app registration instead, change `defaultClientID` in `internal/spauth/auth.go` and rebuild.

## Building

```
go build -o xftp ./cmd/xftp
go build -o xcp ./cmd/xcp
```

## Large files

Files up to 250 MB upload in a single request. Above that, xftp opens a Graph upload session and streams the file in 10 MiB chunks. Downloads stream straight to disk as well, into a temporary file that's renamed into place only once the transfer completes, so an interrupted download never leaves a corrupt file at the real name. Either direction reads or writes directly to disk rather than buffering the whole file in memory, so transfer size is bounded by the library's quota and your local disk, not by RAM.

Transfers over 50 MB print a progress line. Ctrl-C interrupts a transfer in progress and cleans up after itself: a partial download is discarded, and an aborted upload session is cancelled on the server.

---

Built by David M. Anderson, with AI assistance.
