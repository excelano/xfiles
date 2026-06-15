# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately through GitHub Security Advisories at https://github.com/excelano/xftp/security/advisories/new. If you would rather not use GitHub, email david.anderson@excelano.com instead. I aim to respond within seven days.

Please do not open public issues for security problems.

## Supported versions

The latest v1.x release receives security fixes. Older versions are not supported.

## What xftp can access

xftp is a CLI that runs locally on your machine. It calls Microsoft Graph over HTTPS to read and write items in a single bound SharePoint document library — the one named by the URL you pass (and the optional `--library` flag). Authentication is delegated device-code OAuth against your Microsoft Entra ID account; the single scope requested is `Sites.ReadWrite.All`. xftp cannot access any data your account cannot already access in SharePoint Online, and it touches no Graph endpoints beyond the bound library's drive. There is no daemon, no mounted filesystem, and no server component.

Downloads stream to a temporary file in the destination directory and are renamed into place only on success; uploads larger than 250 MB go through a Graph upload session, which is cancelled on the server if the transfer is interrupted.

IT administrators evaluating xftp for a Microsoft 365 tenant will find the application's registration details, the delegated-permission risk profile, and the consent and revocation steps in [ADMINS.md](ADMINS.md).

## What xftp stores

xftp stores REPL command history at `~/.config/xftp/history` and caches a refresh token at `~/.config/xftp/sp-token.json`, both with file mode 0600 (directory mode 0700). The cached token lets subsequent runs reauthenticate without another device-code prompt. Delete `sp-token.json` to force re-authentication; revoke the granted permission at https://myaccount.microsoft.com/applications to invalidate the token server-side. There is no telemetry, no analytics, and no remote logging.

## Verifying releases

Every GitHub release includes a `checksums.txt` file listing SHA-256 hashes of all binary archives. Verify any download before running it:

    sha256sum xftp_1.0.0_linux_amd64.tar.gz
    # compare against the value in checksums.txt

Release artifacts are built by GitHub Actions from a tagged commit using the goreleaser configuration in this repo. The workflow and build configuration are public and auditable.
