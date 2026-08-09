# Releasing xfiles

The release loop for a new version. Run it from a clean `main` with the working tree committed. Examples below cut `v1.2.3`; substitute the version you are actually releasing.

**One tag releases every command in the repo.** There is no per-command version and no way to ship one of them alone: goreleaser builds every entry in `.goreleaser.yml` from the tag, and each command becomes its own archive, its own Debian package, its own Homebrew formula, and its own winget package. The steps below are therefore written to iterate over what the release actually produced rather than to name the commands, so adding a sixth one is a change to `.goreleaser.yml` and nothing else.

**There is no version to bump.** goreleaser stamps each binary from the tag (`-X main.version={{ .Version }}`), so the tag is the single source of truth and no file in the repo carries the number.

1. **Verify.** `go build ./... && go test ./...`, and confirm `git status` is clean. A dirty tree makes goreleaser refuse the release outright, which is the good failure — the bad one is tagging a commit you have not tested.

   The tests cannot reach SharePoint, so a clean run says the code compiles and the pure logic holds, not that the Graph calls still work. Exercise the commands you touched against a real library before tagging, and give anything that writes or deletes an extra pass.

2. **Tag and push.** `git tag v1.2.3 && git push origin main --tags`. The `v*` tag triggers `.github/workflows/release.yml`, which runs goreleaser and does the whole build in one job: platform archives for Linux and macOS on both architectures plus Windows x64, an amd64 and an arm64 `.deb` per command, `checksums.txt`, and the GitHub Release itself. It also pushes a formula per command to `excelano/homebrew-tap`, so Homebrew needs no local step.

   `install.sh` and `uninstall.sh` are attached to the release as extra files, which is what lets a user pin an install to a release URL instead of the rolling `main` branch. They are shipped as-is from the tagged commit, so a fix to either only reaches pinned installs on the next release.

   If the tag was created by another workflow's `GITHUB_TOKEN` the push trigger will not fire, since GitHub suppresses downstream events for token-created refs. Dispatch by hand in that case: `gh workflow run release.yml -f tag=v1.2.3`.

3. **Ship to apt.** This is the channel you install from, so a release that has not reached it is not shipped, whatever the release page says.
   ```sh
   apt-ship xfiles v1.2.3
   ```
   One command covers every command in the repo: it takes whatever `.deb` files the release produced rather than naming them, so a sixth command needs no change here. It adds each to the pool, re-signs the indices, previews the rsync, **refuses to deploy if the preview would delete anything**, pushes, and verifies each package against the live index on both architectures. The tag is optional; with none it takes the latest release. See `feedback_rsync_parent_wipes_subpath` for why the deletion guard exists.

   **This is the step releases lose.** Nothing downstream depends on apt — winget reads the GitHub release directly and ships fine over a release whose apt step never happened — so the failure is silent and everything else looks finished. `fleet -r` is what catches it: an `APT` column reading `behind`, and the `apt-ship` line to fix it.

   `updatesite` is an rsync and does not touch git, but a routine package add leaves nothing to commit either — `dists/` and `pool/` are gitignored build artifacts, which is also why `git status` in the apt repo cannot tell you the step was skipped. Commit the apt repo only when you changed something tracked: a script, `conf/release.conf`, a metapackage `control` file, or the README's curated install hint.

   The **`xfiles` metapackage** in `~/excelano-apt/metapackages/` is a control file that depends on the commands by name with no version constraint, so a release does not touch it. It needs a rebuild (`build.sh xfiles`, then `add-deb.sh` the result) only when the set of commands changes, and that rebuild is where its own `Version:` gets bumped by hand.

4. **Submit the winget manifests.** winget stores one manifest per version and treats each command as a separate package, so a release means one PR per command. Sync the fork first, then let the release tell you what to submit:
   ```sh
   komac sync
   V=1.2.3
   for p in $(gh release view v$V --repo excelano/xfiles --json assets \
                --jq '.assets[].name | select(endswith("_windows_amd64.zip")) | split("_")[0]'); do
     komac update Excelano.$p --version $V \
       --urls https://github.com/excelano/xfiles/releases/download/v$V/${p}_${V}_windows_amd64.zip \
       --submit
   done
   ```
   Run it once without `--submit` first and read the manifests, and check the generated hashes against the release's `checksums.txt`. **A command that has never shipped to winget is not covered by that loop** — `komac update` needs a prior version to generate from, so the first submission for a new command is `komac new Excelano.<name>` and it will sit behind the `New-Package` label waiting on a volunteer moderator, which runs to days or weeks. Once merged, later versions ride the loop above and clear automated validation with no human involved, usually inside a day.

   **Sync the fork before submitting**, every time. A fork that has drifted behind upstream fails in a way that reads like a permissions problem rather than a stale fork, and the failure mode plus the recurring validation failures are all in `~/notes/build_release_gotchas.md`.

   **A pushed `v*` tag is spent.** The merged manifests pin `InstallerSha256`, so deleting and re-cutting a tag swaps the release assets out from under every one of them and breaks every install of that version. Nothing in the pipeline refuses the second attempt — winget, apt, and the Homebrew formulae all overwrite silently. If a release goes wrong after the tag is pushed, bump to the next number.

## Notes

- **Windows is x64 only.** goreleaser ignores the `windows/arm64` combination, so each command has one Windows archive and one winget installer entry.
- **Partial success is the normal failure.** A release that ships most of its packages and drops one is easy to miss, because the GitHub Release still looks complete at a glance. The check that catches it is comparing the count of `.deb` files against the number of commands built, before step 3.
- **`go install` bypasses the ldflags,** so a copy installed that way reports a dev version rather than the tag. That is expected and not worth working around; the installers, apt, Homebrew, and winget all carry stamped binaries.
- **Homebrew tap access is an org-secret question.** The release job pushes the formulae with `HOMEBREW_TAP_TOKEN`. If that secret is scoped to selected repositories, a repo that is not on the list fails the formula step with `Input required and not supplied: token` while the rest of the release succeeds.
- The README, the landing pages under `excelano.com`, and `SECURITY.md` reference the version implicitly via "latest"; none need a per-release edit.
