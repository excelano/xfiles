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

	"github.com/excelano/spauth"
	"github.com/excelano/xfiles/internal/drive"
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

// Reason codes explain why a file was scheduled for transfer; --itemize-changes
// prints them beside the verb. They exist because the failure this comparison
// guards against — a destination that silently rewrites what you uploaded — is
// invisible in a bare "upload <path>" line, and took a manual unzip of the
// remote copy to diagnose the first time. rsync's -i flag string is the model.
const (
	reasonNew     = "new"     // no counterpart on the destination
	reasonTime    = "time"    // mtime moved beyond modWindow
	reasonContent = "content" // mtime moved, size matched, content hash differed
	reasonForced  = "forced"  // --ignore-times bypassed the comparison
)

// op is a single planned change at a mirror-relative path. mtime is the source
// file's modification time, stamped onto the destination after a copy so the
// next run sees the two as equal. reason is set on copies only.
type op struct {
	kind   opKind
	rel    string
	isDir  bool
	size   int64
	mtime  time.Time
	reason string
}

// differs reports whether two files should be considered changed. The mtime is
// authoritative and the size is deliberately NOT consulted here.
//
// SharePoint rewrites Office documents on upload to bind them to the library's
// content type, injecting customXml parts and a ContentTypeId so the stored file
// is permanently larger than the source and hashes differently. A size test
// therefore reports every .docx/.xlsx/.pptx as changed on every run, cutting a
// new document version each time — and it short-circuits before any content hash
// can settle the question. The writable fileSystemInfo mtime is the one field
// that survives the rewrite and still tracks real edits: verified against a live
// library, where an edit made in Word for the web moved it.
//
// So an mtime match within modWindow means in sync, and size only enters the
// picture in plan() once the timestamp says something moved. The cost is a local
// file whose content and size change while its mtime stays put — a restore from
// backup, cp -p, a timestamp-preserving tar -x — which reads as unchanged where
// rsync's size+mtime check would have caught it. --ignore-times forces the
// transfer when that happens.
func differs(a, b fileEntry) bool {
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
func runSync(ctx context.Context, g *spauth.GraphClient, dir direction, localDir, url, library string, dryRun, doDelete, ignoreTimes, itemize bool) int {
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

	// SharePoint and OneDrive are case-preserving but case-insensitive, so an
	// upload's destination keys must be matched with case folded. A download's
	// destination is the local filesystem, which on Linux is case-sensitive.
	mkdirs, copies, verify, deletes, conflicts, upToDate := plan(source, dest, doDelete, ignoreTimes, dir == upload)

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
	printPlan(dir, itemize, mkdirs, copies, deletes)

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
		if err := d.MkdirIfMissing(ctx, g, cur); err != nil {
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
// remove. A file whose mtime moved but whose size is unchanged goes into verify
// rather than copies: only its timestamp shifted, which on some tenants happens
// because the post-upload mtime stamp did not survive, so the caller settles it
// by content hash before re-transferring. ignoreTimes skips the comparison
// entirely and schedules every existing file, the escape hatch for a local edit
// that left the mtime untouched. Directory creations sort parents-first;
// deletions keep only the top-most missing path of each removed subtree.
func plan(source, dest map[string]fileEntry, doDelete, ignoreTimes, foldDest bool) (mkdirs, copies, verify, deletes []op, conflicts []string, upToDate int) {
	// Index both sides by folded key when the destination cannot tell two
	// spellings apart. Without this a library holding D1 and a source offering
	// d1 read as one missing folder and one extra one — which schedules a mkdir
	// for a folder that is already there and, under --delete, a recursive delete
	// of the folder the copies were just written into, because execute runs
	// deletes last. The mkdir noise was the visible half; that was the other.
	destFold := foldIndex(dest, foldDest)
	sourceFold := foldIndex(source, foldDest)

	// Two source paths differing only in case are two entries here and one item
	// on the destination, so one silently overwrites the other. Say so instead.
	if foldDest {
		for _, rels := range collisions(source) {
			conflicts = append(conflicts, strings.Join(rels, " and ")+
				" (differ only by case; the destination cannot hold both)")
		}
	}

	for rel, s := range source {
		dst, exists := lookupFolded(dest, destFold, rel)
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
		reason := reasonNew
		if exists {
			if dst.isDir {
				conflicts = append(conflicts, rel+" (file in source, folder in destination)")
				continue
			}
			switch {
			case ignoreTimes:
				reason = reasonForced
			case !differs(s, dst):
				upToDate++
				continue
			case s.size == dst.size:
				verify = append(verify, op{kind: opCopy, rel: rel, size: s.size, mtime: s.mtime, reason: reasonTime})
				continue
			default:
				reason = reasonTime
			}
		}
		copies = append(copies, op{kind: opCopy, rel: rel, size: s.size, mtime: s.mtime, reason: reason})
	}

	if doDelete {
		delSet := map[string]bool{}
		for rel := range dest {
			if _, ok := lookupFolded(source, sourceFold, rel); !ok {
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

// foldIndex maps each entry's lower-cased path to its actual one, or returns nil
// when the destination is case-sensitive and no folding should happen. A path
// that collides with another under folding is not represented here — collisions
// are reported as conflicts rather than resolved by picking a winner.
func foldIndex(m map[string]fileEntry, fold bool) map[string]string {
	if !fold {
		return nil
	}
	idx := make(map[string]string, len(m))
	for rel := range m {
		idx[strings.ToLower(rel)] = rel
	}
	return idx
}

// lookupFolded finds rel in m, trying the exact spelling first so a tree whose
// cases already agree behaves exactly as it did before folding existed.
func lookupFolded(m map[string]fileEntry, idx map[string]string, rel string) (fileEntry, bool) {
	if e, ok := m[rel]; ok {
		return e, true
	}
	if idx == nil {
		return fileEntry{}, false
	}
	if actual, ok := idx[strings.ToLower(rel)]; ok {
		return m[actual], true
	}
	return fileEntry{}, false
}

// collisions groups the paths that differ only by case, sorted so the report
// reads the same on every run (Go map iteration order is not stable).
func collisions(m map[string]fileEntry) [][]string {
	byFold := map[string][]string{}
	for rel := range m {
		k := strings.ToLower(rel)
		byFold[k] = append(byFold[k], rel)
	}
	var out [][]string
	for _, rels := range byFold {
		if len(rels) > 1 {
			sort.Strings(rels)
			out = append(out, rels)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// printPlan lists every planned change, one per line, with a direction-aware
// verb for copies (upload vs download). When itemize is set each copy also
// carries its reason code, so "why is this being sent again?" is answerable from
// the output instead of by inspecting the remote copy by hand.
func printPlan(dir direction, itemize bool, mkdirs, copies, deletes []op) {
	for _, o := range mkdirs {
		printOp(itemize, "mkdir", "", o.rel+"/")
	}
	verb := "upload"
	if dir == download {
		verb = "download"
	}
	for _, o := range copies {
		printOp(itemize, verb, o.reason, o.rel)
	}
	for _, o := range deletes {
		suffix := ""
		if o.isDir {
			suffix = "/"
		}
		printOp(itemize, "delete", "", o.rel+suffix)
	}
}

// printOp writes one plan line, with the reason column only when itemizing so
// the default output stays as narrow as it was.
func printOp(itemize bool, verb, reason, path string) {
	if itemize {
		fmt.Printf("%-8s %-8s %s\n", verb, reason, path)
		return
	}
	fmt.Printf("%-8s %s\n", verb, path)
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
		// The folder existing is the end state a mirror wants, so an already-there
		// folder is not an error the way it is for an interactive xftp mkdir.
		return d.MkdirIfMissing(ctx, g, path.Join(remoteRoot, o.rel))
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
			o.reason = reasonContent
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
