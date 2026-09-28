# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately through GitHub Security Advisories at https://github.com/excelano/xfiles/security/advisories/new. If you would rather not use GitHub, email david.anderson@excelano.com instead. I aim to respond within seven days.

Please do not open public issues for security problems.

## Supported versions

The latest release receives security fixes. Older versions are not supported.

## What xftp can access

This repository ships five CLIs that run locally on your machine: `xftp` (interactive), `xcp` (one-shot copies), `xsync` (recursive mirror), and the read-only `xfind` and `xtree` (recursive listing). They call Microsoft Graph over HTTPS to read and write items in a single bound SharePoint document library — the one named by the URL you pass (and the optional `--library` flag); `xfind` and `xtree` only ever read. Authentication is delegated device-code OAuth against your Microsoft Entra ID account; the single scope requested is `Sites.ReadWrite.All`. None of the tools can access any data your account cannot already access in SharePoint Online, and they touch no Graph endpoints beyond the bound library's drive. There is no daemon, no mounted filesystem, and no server component.

Downloads stream to a temporary file in the destination directory and are renamed into place only on success; uploads larger than 250 MB go through a Graph upload session, which is cancelled on the server if the transfer is interrupted. `xsync`'s `--delete` flag removes destination items that no longer exist in the source; on an interactive terminal it asks for confirmation first, and `--dry-run` previews the full plan without changing anything.

IT administrators evaluating any of these tools for a Microsoft 365 tenant will find the application's registration details, the delegated-permission risk profile, and the consent and revocation steps in [ADMINS.md](ADMINS.md). Every tool in the suite shares one app registration, so a single consent covers them all.

## What the tools store

xftp stores REPL command history at `~/.config/xftp/history` with file mode 0600 (directory mode 0700). Every xfiles command caches one refresh token at `~/.config/excelano/sp-token.json`, a file shared with the sibling tool xql because every one of them signs in against the same app registration, so subsequent runs of any tool in the family reauthenticate without another device-code prompt. The cache is encrypted at rest under a key the operating system holds, not one stored beside it: DPAPI on Windows, deriving its key from the user's logon; the user's D-Bus Secret Service on Linux, or the kernel's per-user keyring where no session bus or unlocked collection is reachable; the login Keychain on macOS. A copy of the file taken off that account, in a backup, a synced folder or a disk image, cannot be read. Where none of these is reachable, the cache falls back to plaintext, and its protection is then the file mode, 0600 in a 0700 directory, which keeps other accounts on the host out and does nothing for a copy. On every platform, any program running as the same user can read the token. The cache is written through a temp file and rename so an interrupted write never replaces a good cache. Versions before the cache was shared kept a token under `~/.config/<tool>`; the current version adopts that file on first run and deletes it once the shared cache is confirmed encrypted, leaving it for the uninstaller's purge step to remove wherever encryption is not reachable. Delete the shared `sp-token.json` to force re-authentication for the whole family; revoke the granted permission at https://myaccount.microsoft.com/applications to invalidate the token server-side. There is no telemetry, no analytics, and no remote logging.

## Verifying releases

Every GitHub release includes a `checksums.txt` file listing SHA-256 hashes of all binary archives. Verify any download before running it:

    sha256sum xftp_1.0.0_linux_amd64.tar.gz
    # compare against the value in checksums.txt

Release artifacts are built by GitHub Actions from a tagged commit using the goreleaser configuration in this repo. The workflow and build configuration are public and auditable.
