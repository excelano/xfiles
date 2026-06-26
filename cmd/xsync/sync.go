package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/excelano/quickxorhash"
	"golang.org/x/term"

	"github.com/excelano/xfiles/internal/drive"
	"github.com/excelano/xfiles/internal/spauth"
	"github.com/excelano/xfiles/internal/xfer"
)

// modWindow is the tolerance for the mtime comparison. SharePoint stores
// fileSystemInfo times at whole-second (or finer) precision while local files
// carry nanoseconds, so two files written "at the same time" can differ by a
// fraction of a second; treating anything within this window as unchanged keeps
// xsync from re-transferring on that rounding alone. It mirrors rsync's
// --modify-window idea.
const modWindow = 2 * time.Second

// fileEntry is one node in either tree, keyed in the scan maps by its path
// relative to the mirror root (forward slashes on both sides). Size and mtime
// are unset for directories, which are compared by existence alone.
type fileEntry struct {
	rel   string
	isDir bool
	size  int64
	mtime time.Time
	// hash is SharePoint's QuickXorHash, set only on entries scanned from the
	// remote side; the local scan leaves it empty and computes a hash on demand.
	hash string
}

// opKind is the verb of a planned change, interpreted against whichever side is
// the destination for the run's direction.
type opKind int

const (
	opMkdir  opKind = iota // create a directory on the destination
	opCopy                 // transfer a file from source to destination
	opDelete               // remove a destination item missing from the source
)

// op is a single planned change at a mirror-relative path. mtime is the source
// file's modification time, stamped onto the destination after a copy so the
// next run sees the two as equal.
type op struct {
	kind  opKind
	rel   string
	isDir bool
	size  int64
	mtime time.Time
}

// differs reports whether two files should be considered changed: any size
// difference, or an mtime gap wider than modWindow.
func differs(a, b fileEntry) bool {
	if a.size != b.size {
		return true
	}
	d := a.mtime.Sub(b.mtime)
	if d < 0 {
		d = -d
	}
	return d > modWindow
}

// relTo returns full's path relative to root, both library-relative. With an
// empty root (the library itself) full is already relative.
func relTo(root, full string) string {
	root = strings.Trim(root, "/")
	full = strings.Trim(full, "/")
	if full == root {
		return ""
	}
	if root == "" {
		return full
	}
	return strings.TrimPrefix(full, root+"/")
}

// depth counts the path separators in a mirror-relative path, so parents sort
// before children.
func depth(rel string) int { return strings.Count(rel, "/") }

// hasAncestorIn reports whether any ancestor of rel is itself in the set, used
// to drop redundant deletes — removing a folder cascades to its contents, so a
// child delete underneath a deleted folder would only 404.
func hasAncestorIn(rel string, set map[string]bool) bool {
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		if set[strings.Join(parts[:i], "/")] {
			return true
		}
	}
	return false
}

// runSync binds the library, scans both trees, plans the changes, and (after a
// confirmation when deletions are involved) applies them. It returns a process
// exit code.
func runSync(ctx context.Context, g *spauth.GraphClient, dir direction, localDir, url, library string, dryRun, doDelete bool) int {
	d, err := drive.ResolveDrive(ctx, g, url, library)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind library: %v\n", err)
		return 1
	}
	remoteRoot := d.StartPath

	source, dest, err := scanTrees(ctx, g, d, dir, localDir, remoteRoot, dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	mkdirs, copies, verify, deletes, conflicts, upToDate := plan(source, dest, doDelete)

	// Settle the size-equal/mtime-diverged candidates by content hash. The remote
	// side carries SharePoint's QuickXorHash either way; matches are already in
	// sync, mismatches join the copies. Hashing is read-only, so it runs on a dry
	// run too, keeping the previewed plan honest.
	if len(verify) > 0 {
		remote := dest
		if dir == download {
			remote = source
		}
		moreCopies, verified := resolveVerify(ctx, g, d, dir, localDir, remoteRoot, verify, remote, dryRun)
		copies = append(copies, moreCopies...)
		sort.Slice(copies, func(i, j int) bool { return copies[i].rel < copies[j].rel })
		upToDate += verified
	}

	for _, c := range conflicts {
		fmt.Fprintf(os.Stderr, "skipping (type conflict): %s\n", c)
	}

	if len(mkdirs)+len(copies)+len(deletes) == 0 {
		fmt.Printf("Already in sync (%d up to date).\n", upToDate)
		return boolToCode(len(conflicts) > 0)
	}

	if dryRun {
		fmt.Println("Dry run — no changes will be made:")
	}
	printPlan(dir, mkdirs, copies, deletes)

	if dryRun {
		fmt.Printf("\nWould change: %d created, %d copied, %d deleted (%d up to date).\n",
			len(mkdirs), len(copies), len(deletes), upToDate)
		return boolToCode(len(conflicts) > 0)
	}

	// Deletions are the one irreversible step; gate them behind a confirmation on
	// an interactive terminal. Non-interactive runs (scripts, pipes) proceed, the
	// way rsync --delete does, since there's no one to ask.
	if len(deletes) > 0 && stdinIsTTY() {
		if !confirm(fmt.Sprintf("Delete %d destination item(s) not in the source? [y/N] ", len(deletes))) {
			fmt.Fprintln(os.Stderr, "Skipping deletions.")
			deletes = nil
		}
	}

	res := execute(ctx, g, d, dir, localDir, remoteRoot, mkdirs, copies, deletes)
	fmt.Printf("\nDone: %d created, %d copied, %d deleted (%d up to date).\n",
		res.mkdirs, res.copies, res.deletes, upToDate)
	return boolToCode(res.errs > 0 || len(conflicts) > 0)
}

// scanTrees validates the two roots for the chosen direction and returns the
// source and destination trees as relative-path maps. For an upload it ensures
// the remote root folder exists (creating it, and any ancestors, unless this is
// a dry run); for a download it requires the remote folder to exist and creates
// the local root.
func scanTrees(ctx context.Context, g *spauth.GraphClient, d *drive.Drive, dir direction, localDir, remoteRoot string, dryRun bool) (source, dest map[string]fileEntry, err error) {
	if dir == upload {
		info, serr := os.Stat(localDir)
		if serr != nil {
			return nil, nil, fmt.Errorf("local source %s: %w", localDir, serr)
		}
		if !info.IsDir() {
			return nil, nil, fmt.Errorf("local source %s is not a directory; use xcp for a single file", localDir)
		}
		local, lerr := scanLocal(localDir)
		if lerr != nil {
			return nil, nil, lerr
		}
		remote, rerr := scanRemoteRoot(ctx, g, d, remoteRoot, dryRun)
		if rerr != nil {
			return nil, nil, rerr
		}
		return local, remote, nil
	}

	// download: the remote folder is the source and must exist.
	if remoteRoot != "" {
		it, serr := d.Stat(ctx, g, remoteRoot)
		if serr != nil {
			return nil, nil, fmt.Errorf("remote source not found: %w", serr)
		}
		if !it.IsFolder {
			return nil, nil, fmt.Errorf("the URL points to a file, not a folder; use xcp for a single file")
		}
	}
	remote, rerr := scanRemote(ctx, g, d, remoteRoot)
	if rerr != nil {
		return nil, nil, rerr
	}
	if !dryRun {
		if mkErr := os.MkdirAll(localDir, 0o755); mkErr != nil {
			return nil, nil, fmt.Errorf("creating local destination %s: %w", localDir, mkErr)
		}
	}
	local := map[string]fileEntry{}
	if info, serr := os.Stat(localDir); serr == nil {
		if !info.IsDir() {
			return nil, nil, fmt.Errorf("local destination %s is not a directory", localDir)
		}
		scanned, lerr := scanLocal(localDir)
		if lerr != nil {
			return nil, nil, lerr
		}
		local = scanned
	}
	return remote, local, nil
}

// scanRemoteRoot scans the remote tree for an upload, ensuring the destination
// root folder exists first. A root that isn't there yet means an empty remote
// tree (everything will be created); on a real run its folders are created up
// front so later path-addressed uploads have a parent.
func scanRemoteRoot(ctx context.Context, g *spauth.GraphClient, d *drive.Drive, remoteRoot string, dryRun bool) (map[string]fileEntry, error) {
	if remoteRoot != "" {
		it, err := d.Stat(ctx, g, remoteRoot)
		if err != nil {
			if !dryRun {
				if cerr := ensureRemoteDir(ctx, g, d, remoteRoot); cerr != nil {
					return nil, fmt.Errorf("creating remote destination: %w", cerr)
				}
			}
			return map[string]fileEntry{}, nil
		}
		if !it.IsFolder {
			return nil, fmt.Errorf("the URL points to a file, not a folder; use xcp for a single file")
		}
	}
	return scanRemote(ctx, g, d, remoteRoot)
}

// ensureRemoteDir creates each missing segment of a library-relative folder
// path, parent before child, so a deep destination root can be materialised in
// one shot.
func ensureRemoteDir(ctx context.Context, g *spauth.GraphClient, d *drive.Drive, dir string) error {
	segs := strings.Split(strings.Trim(dir, "/"), "/")
	cur := ""
	for _, s := range segs {
		cur = path.Join(cur, s)
		if _, err := d.Stat(ctx, g, cur); err == nil {
			continue
		}
		if err := d.Mkdir(ctx, g, cur); err != nil {
			return err
		}
	}
	return nil
}

// scanLocal walks a local directory tree into a relative-path map. Symlinks and
// other non-regular files are skipped — there's no faithful way to mirror them
// to a document library.
func scanLocal(root string) (map[string]fileEntry, error) {
	m := map[string]fileEntry{}
	err := filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if de.IsDir() {
			m[rel] = fileEntry{rel: rel, isDir: true}
			return nil
		}
		if !de.Type().IsRegular() {
			return nil
		}
		info, ierr := de.Info()
		if ierr != nil {
			return ierr
		}
		m[rel] = fileEntry{rel: rel, size: info.Size(), mtime: info.ModTime()}
		return nil
	})
	return m, err
}

// scanRemote walks a SharePoint folder tree into a relative-path map, recording
// size, the writable fileSystemInfo mtime, and the QuickXorHash used as a
// content-level tiebreaker when the mtime alone is inconclusive.
func scanRemote(ctx context.Context, g *spauth.GraphClient, d *drive.Drive, root string) (map[string]fileEntry, error) {
	m := map[string]fileEntry{}
	err := d.Walk(ctx, g, root, false, func(it drive.Item, p string, _ int, _ bool) bool {
		rel := relTo(root, p)
		if it.IsFolder {
			m[rel] = fileEntry{rel: rel, isDir: true}
		} else {
			m[rel] = fileEntry{rel: rel, size: it.Size, mtime: it.FSModified, hash: it.QuickXorHash}
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

// plan diffs the source tree against the destination tree, producing the
// directories to create, files to copy, and (when doDelete) destination items to
// remove. A file the cheap size+mtime test flags as changed, but whose size is
// unchanged, goes into verify rather than copies: only its timestamp moved, which
// on some tenants happens because the post-upload mtime stamp did not survive, so
// the caller settles it by content hash before re-transferring. Directory
// creations sort parents-first; deletions keep only the top-most missing path of
// each removed subtree.
func plan(source, dest map[string]fileEntry, doDelete bool) (mkdirs, copies, verify, deletes []op, conflicts []string, upToDate int) {
	for rel, s := range source {
		dst, exists := dest[rel]
		if s.isDir {
			if exists {
				if !dst.isDir {
					conflicts = append(conflicts, rel+" (folder in source, file in destination)")
				}
				continue
			}
			mkdirs = append(mkdirs, op{kind: opMkdir, rel: rel, isDir: true})
			continue
		}
		if exists {
			if dst.isDir {
				conflicts = append(conflicts, rel+" (file in source, folder in destination)")
				continue
			}
			if !differs(s, dst) {
				upToDate++
				continue
			}
			if s.size == dst.size {
				verify = append(verify, op{kind: opCopy, rel: rel, size: s.size, mtime: s.mtime})
				continue
			}
		}
		copies = append(copies, op{kind: opCopy, rel: rel, size: s.size, mtime: s.mtime})
	}

	if doDelete {
		delSet := map[string]bool{}
		for rel := range dest {
			if _, ok := source[rel]; !ok {
				delSet[rel] = true
			}
		}
		for rel := range delSet {
			if hasAncestorIn(rel, delSet) {
				continue
			}
			deletes = append(deletes, op{kind: opDelete, rel: rel, isDir: dest[rel].isDir})
		}
	}

	sort.Slice(mkdirs, func(i, j int) bool {
		if di, dj := depth(mkdirs[i].rel), depth(mkdirs[j].rel); di != dj {
			return di < dj
		}
		return mkdirs[i].rel < mkdirs[j].rel
	})
	sort.Slice(copies, func(i, j int) bool { return copies[i].rel < copies[j].rel })
	sort.Slice(deletes, func(i, j int) bool { return deletes[i].rel < deletes[j].rel })
	return mkdirs, copies, verify, deletes, conflicts, upToDate
}

// printPlan lists every planned change, one per line, with a direction-aware
// verb for copies (upload vs download).
func printPlan(dir direction, mkdirs, copies, deletes []op) {
	for _, o := range mkdirs {
		fmt.Printf("%-8s %s/\n", "mkdir", o.rel)
	}
	verb := "upload"
	if dir == download {
		verb = "download"
	}
	for _, o := range copies {
		fmt.Printf("%-8s %s\n", verb, o.rel)
	}
	for _, o := range deletes {
		suffix := ""
		if o.isDir {
			suffix = "/"
		}
		fmt.Printf("%-8s %s%s\n", "delete", o.rel, suffix)
	}
}

// result tallies what execute actually did, so the summary reflects completed
// work rather than the planned counts — the two diverge when an item fails.
type result struct {
	mkdirs, copies, deletes, errs int
}

// execute applies the planned changes against the destination side and returns
// the tally of what succeeded and failed. Per-item failures are reported and the
// run continues, the way rsync soldiers on past a single bad file.
func execute(ctx context.Context, g *spauth.GraphClient, d *drive.Drive, dir direction, localRoot, remoteRoot string, mkdirs, copies, deletes []op) result {
	var r result
	for _, o := range mkdirs {
		if err := applyMkdir(ctx, g, d, dir, localRoot, remoteRoot, o); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", o.rel, err)
			r.errs++
		} else {
			r.mkdirs++
		}
	}
	for _, o := range copies {
		if err := applyCopy(ctx, g, d, dir, localRoot, remoteRoot, o); err != nil {
			fmt.Fprintf(os.Stderr, "copy %s: %v\n", o.rel, err)
			r.errs++
		} else {
			r.copies++
		}
	}
	for _, o := range deletes {
		if err := applyDelete(ctx, g, d, dir, localRoot, remoteRoot, o); err != nil {
			fmt.Fprintf(os.Stderr, "delete %s: %v\n", o.rel, err)
			r.errs++
		} else {
			r.deletes++
		}
	}
	return r
}

func applyMkdir(ctx context.Context, g *spauth.GraphClient, d *drive.Drive, dir direction, localRoot, remoteRoot string, o op) error {
	if dir == upload {
		return d.Mkdir(ctx, g, path.Join(remoteRoot, o.rel))
	}
	return os.MkdirAll(filepath.Join(localRoot, filepath.FromSlash(o.rel)), 0o755)
}

func applyCopy(ctx context.Context, g *spauth.GraphClient, d *drive.Drive, dir direction, localRoot, remoteRoot string, o op) error {
	localPath := filepath.Join(localRoot, filepath.FromSlash(o.rel))
	remotePath := path.Join(remoteRoot, o.rel)
	if dir == upload {
		if _, err := xfer.Upload(ctx, g, d, localPath, remotePath); err != nil {
			return err
		}
		// Stamp the remote copy with the source mtime so the next run sees them as
		// equal; a failure here only costs a redundant re-upload later, so it's a
		// warning, not a hard error.
		if err := d.SetMTime(ctx, g, remotePath, o.mtime); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not set mtime on %s: %v\n", o.rel, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	if err := xfer.Download(ctx, g, d, remotePath, localPath); err != nil {
		return err
	}
	if err := os.Chtimes(localPath, o.mtime, o.mtime); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not set mtime on %s: %v\n", o.rel, err)
	}
	return nil
}

// resolveVerify decides each same-size/mtime-diverged candidate by content hash,
// turning an inconclusive timestamp into a definite answer. It hashes the local
// file with QuickXorHash and compares it to the remote item's hash (the remote
// side always carries one). A match is already in sync; on a real run it re-stamps
// the mtime on the side that drifted so the next run hits the cheap fast path. A
// mismatch — or a remote item with no hash computed yet — is returned as a copy.
func resolveVerify(ctx context.Context, g *spauth.GraphClient, d *drive.Drive, dir direction, localRoot, remoteRoot string, verify []op, remote map[string]fileEntry, dryRun bool) (copies []op, verified int) {
	for _, o := range verify {
		localPath := filepath.Join(localRoot, filepath.FromSlash(o.rel))
		want := remote[o.rel].hash
		got, err := localQuickXor(localPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: hashing %s: %v\n", o.rel, err)
		}
		if want == "" || got == "" || got != want {
			copies = append(copies, o)
			continue
		}
		// Content matches; only the timestamp was off. Re-stamp the drifted side so
		// the comparison fast-paths next time. Best effort — on a tenant that does
		// not honour the stamp this just repeats the cheap hash on the next run.
		if !dryRun {
			if dir == upload {
				err = d.SetMTime(ctx, g, path.Join(remoteRoot, o.rel), o.mtime)
			} else {
				err = os.Chtimes(localPath, o.mtime, o.mtime)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not set mtime on %s: %v\n", o.rel, err)
			}
		}
		verified++
	}
	return copies, verified
}

// localQuickXor computes the base64 QuickXorHash of a local file, matching the
// encoding SharePoint reports so the two values can be compared directly.
func localQuickXor(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := quickxorhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

func applyDelete(ctx context.Context, g *spauth.GraphClient, d *drive.Drive, dir direction, localRoot, remoteRoot string, o op) error {
	if dir == upload {
		return d.Remove(ctx, g, path.Join(remoteRoot, o.rel))
	}
	return os.RemoveAll(filepath.Join(localRoot, filepath.FromSlash(o.rel)))
}

// stdinIsTTY reports whether standard input is an interactive terminal, so the
// delete confirmation is only asked when there's a human to answer it. It uses a
// real terminal check rather than os.ModeCharDevice, which is also set for
// /dev/null and other character devices — under that looser test a cron job or
// `xsync --delete < /dev/null` would prompt, read EOF, and silently skip the
// deletions the user explicitly asked for.
func stdinIsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// confirm prints a yes/no prompt to stderr and reports whether the answer was
// affirmative. Anything other than y/yes is a no.
func confirm(prompt string) bool {
	fmt.Fprint(os.Stderr, prompt)
	var resp string
	fmt.Scanln(&resp)
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "y" || resp == "yes"
}

func boolToCode(b bool) int {
	if b {
		return 1
	}
	return 0
}
