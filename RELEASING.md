# Releasing xfiles

The release loop lives in `~/notes/releasing.md` — the ordered steps, the apt
step, the winget submission, the spent-tag rule, and the standing facts about
tokens and secrets. Failure recipes are in `~/notes/build_release_gotchas.md`.
This file carries what is true of xfiles and not of its siblings.

| | |
|---|---|
| Loop | goreleaser |
| `apt-ship` argument | `xfiles` |
| winget packages | `Excelano.<command>`, one per command |
| Windows asset | `<command>_<version>_windows_amd64.zip` |

**One tag releases every command in the repo.** There is no per-command version
and no way to ship one of them alone: goreleaser builds every entry in
`.goreleaser.yml` from the tag, and each command becomes its own archive, its own
Debian package, its own Homebrew formula, and its own winget package. The steps
here are written to iterate over what the release actually produced rather than
to name the commands, so adding a command is a change to `.goreleaser.yml` and
nothing else.

**One `apt-ship xfiles` covers every command.** It takes whatever `.deb` files
the release produced rather than naming them.

**Winget means one PR per command.** Let the release tell you what to submit:

```sh
gh repo sync anderix/winget-pkgs
V=1.2.3
for p in $(gh release view v$V --repo excelano/xfiles --json assets \
             --jq '.assets[].name | select(endswith("_windows_amd64.zip")) | split("_")[0]'); do
  komac update Excelano.$p --version $V \
    --urls https://github.com/excelano/xfiles/releases/download/v$V/${p}_${V}_windows_amd64.zip \
    --submit
done
```

Run it once without `--submit` and read the manifests first. **A command that has
never shipped to winget is not covered by that loop** — `komac update` needs a
prior version to generate from, so a first submission is `komac new
Excelano.<name>` and waits on a moderator.

**Partial success is the normal failure.** A release that ships most of its
packages and drops one is easy to miss, because the GitHub Release still looks
complete at a glance. The check that catches it is comparing the count of `.deb`
files against the number of commands built, before the apt step.

**Windows is x64 only,** so each command has one Windows archive and one winget
installer entry.

`install.sh` and `uninstall.sh` are attached to the release as extra files, so a
user can pin an install to a release URL instead of the rolling `main` branch.
The release workflow takes a `workflow_dispatch` input, so a tag that fails to
trigger it needs no re-tagging: `gh workflow run release.yml -f tag=v1.2.3`.

**The `xfiles` metapackage** in `~/excelano-apt/metapackages/` depends on the
commands by name with no version constraint, so a release does not touch it. It
needs a rebuild (`build.sh xfiles`, then `add-deb.sh` the result) only when the
set of commands changes.

**The unit tests cannot reach SharePoint,** so a clean `go test ./...` says the
code compiles and the pure logic holds, not that the Graph calls still work. The
live suite is the pass that does, and it is part of step 1 here:

```sh
XFILES_LIVE_SITE=https://<tenant>.sharepoint.com/sites/<test-site> go test -tags live ./...
```

It runs on a machine that has signed in once (any tool in the family, or `xql
sp`), against the named site's default document library, and every test owns a
folder it creates and removes. It cannot run in CI: device-code needs a human,
and a refresh token in this public repo's secrets would be a live credential to
the tenant. Give anything that writes or deletes an extra look in the output
before tagging; the suite exercises upload by both paths, download, move,
touch, remove and the tree walk, but a real library can only fail the way it
chooses to on the day.
