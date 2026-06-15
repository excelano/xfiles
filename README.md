# xftp

xftp gives a SharePoint document library the feel of an FTP session. You connect to a site, land at an interactive prompt, and move files around with the verbs your fingers already know: `ls`, `cd`, `get`, `put`, `mkdir`, `rm`, `mv`. There is no SSH, FTP, or SCP server behind SharePoint to connect to — those protocols simply don't exist there — so xftp recreates the experience on top of the Microsoft Graph drive API and hides the Graph plumbing entirely.

It is a single static Go binary with no daemon and no mounted filesystem. Authentication is device-code OAuth: the first time you connect, xftp prints a short code and a URL, you sign in once in a browser, and the refresh token is cached under `~/.config/xftp` so later runs are silent.

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

On Debian or Ubuntu, install from the [Excelano apt repository](https://excelano.com/apt/) instead, so `apt upgrade` keeps it current:

```sh
curl -fsSL https://excelano.com/apt/setup.sh | sudo sh
sudo apt install xftp
```

From source (Go 1.24 or later):

```
go install github.com/excelano/xftp/cmd/xftp@latest
```

To uninstall, run `curl -fsSL https://raw.githubusercontent.com/excelano/xftp/main/uninstall.sh | sh`.

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

Paths may be relative to the current folder or absolute with a leading `/`, and `.`/`..` work as you'd expect. Names containing spaces are double-quoted.

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

## Authentication and tenants

xftp authenticates through a multi-tenant Azure app registration ("Excelano SharePoint tools"), shared with its sibling tool [xql](https://github.com/excelano/xql), so consenting once covers both. Pointing xftp at another organization's site uses that same registration — nobody sets up their own. The first connection to a new tenant raises a one-time consent prompt; depending on that tenant's policy, either the user or an administrator clears it, after which everyone in the tenant is covered. The single scope requested is `Sites.ReadWrite.All`. If your organization restricts user consent, [ADMINS.md](ADMINS.md) has everything your IT department needs to review and approve the application.

To use your own app registration instead, change `defaultClientID` in `internal/spauth/auth.go` and rebuild.

## Building

```
go build -o xftp ./cmd/xftp
```

## Large files

Files up to 250 MB upload in a single request. Above that, xftp opens a Graph upload session and streams the file in 10 MiB chunks. Downloads stream straight to disk as well, into a temporary file that's renamed into place only once the transfer completes, so an interrupted download never leaves a corrupt file at the real name. Either direction reads or writes directly to disk rather than buffering the whole file in memory, so transfer size is bounded by the library's quota and your local disk, not by RAM.

Transfers over 50 MB print a progress line. Ctrl-C interrupts a transfer in progress and cleans up after itself: a partial download is discarded, and an aborted upload session is cancelled on the server.

---

Built by David M. Anderson, with AI assistance.
