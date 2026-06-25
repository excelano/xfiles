// Package drive is xftp's SharePoint document-library client: it resolves a
// SharePoint URL to a Graph drive, then exposes FTP-shaped operations over that
// drive — Stat, List, Download, Upload, Mkdir, Remove, Move. The URL may name
// just the site (binds the default library), a specific library, or a folder
// deep-linked from the browser (binds that library and seeds the starting
// folder). Uploads stream from a reader: files up to 250MB go in a single PUT,
// larger ones through a chunked, resumable upload session.
package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/excelano/xfiles/internal/spauth"
)

// Drive is a resolved SharePoint document library the session operates on.
// SourceURL is kept so a future REPL "refresh"/reconnect can re-bind without
// re-prompting. StartPath is the library-relative folder the URL pointed into
// ("" for the library root), used to seed the REPL's working directory.
type Drive struct {
	SiteID    string
	DriveID   string
	Name      string
	Hostname  string
	SitePath  string
	SourceURL string
	StartPath string
}

// Item is one entry in a drive folder listing — the unit an FTP-style "ls"
// prints and "cd" descends into. LastModified is the service-level modification
// time (what "ls" shows); FSModified is the filesystem mtime from
// fileSystemInfo — the writable timestamp OneDrive clients mirror, which xsync
// uses for size+mtime comparisons. FSModified falls back to LastModified when a
// drive item carries no fileSystemInfo.
type Item struct {
	Name         string
	ID           string
	IsFolder     bool
	Size         int64
	ChildCount   int
	LastModified time.Time
	FSModified   time.Time
	// QuickXorHash is SharePoint's content hash for a file, base64-encoded as
	// Graph returns it; empty for folders and for files whose hash the service
	// has not yet computed. xsync uses it to settle a size-equal/mtime-diverged
	// comparison by content instead of re-transferring on a timestamp alone.
	QuickXorHash string
}

// parseSiteURL splits a SharePoint URL into its hostname, server-relative site
// path, and the leftover path below the site. For
// https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports it
// returns ("contoso.sharepoint.com", "/sites/Marketing", "Shared Documents/Reports").
// The leftover is what tells ResolveDrive whether the URL points at a specific
// library/folder; the actual library match is done by webUrl in ResolveDrive.
//
// Adapted from xql's parseListURL, minus the /Lists/ requirement.
func parseSiteURL(rawURL string) (hostname, sitePath, restPath string, err error) {
	u, perr := url.Parse(rawURL)
	if perr != nil {
		return "", "", "", fmt.Errorf("parsing site URL: %w", perr)
	}
	if u.Host == "" {
		return "", "", "", fmt.Errorf("site URL has no host: %s", rawURL)
	}
	hostname = u.Host

	parts := urlPathSegments(u.Path)
	parts = parts[shareLinkPrefixLen(parts):]
	if len(parts) == 0 {
		return hostname, "", "", nil
	}

	// A canonical site URL is /sites/{name} or /teams/{name}; keep just that
	// prefix as the site path. Anything beyond it is the library/folder path.
	if len(parts) >= 2 && (strings.EqualFold(parts[0], "sites") || strings.EqualFold(parts[0], "teams")) {
		sitePath = "/" + parts[0] + "/" + parts[1]
		return hostname, sitePath, strings.Join(parts[2:], "/"), nil
	}

	// Root site (no /sites/ segment): the whole path is below the site.
	return hostname, "", strings.Join(parts, "/"), nil
}

// ResolveDrive resolves a SharePoint URL to a Graph drive. Library selection
// follows three rules, in order: an explicit library name wins; otherwise a URL
// that points below the site is matched to a library by webUrl (with any folder
// remainder kept as the starting path); otherwise the site's default document
// library ("Documents"/"Shared Documents") is bound.
func ResolveDrive(ctx context.Context, g *spauth.GraphClient, siteURL, library string) (*Drive, error) {
	hostname, sitePath, restPath, err := parseSiteURL(siteURL)
	if err != nil {
		return nil, err
	}

	siteID, err := resolveSiteID(ctx, g, hostname, sitePath)
	if err != nil {
		return nil, fmt.Errorf("resolving site: %w", err)
	}

	var driveID, name, startPath string
	switch {
	case library != "":
		driveID, name, err = resolveDriveByName(ctx, g, siteID, library)
	case restPath != "":
		driveID, name, startPath, err = resolveDriveFromURL(ctx, g, siteID, siteURL)
	default:
		driveID, name, err = resolveDefaultDrive(ctx, g, siteID)
	}
	if err != nil {
		return nil, fmt.Errorf("resolving library: %w", err)
	}

	return &Drive{
		SiteID:    siteID,
		DriveID:   driveID,
		Name:      name,
		Hostname:  hostname,
		SitePath:  sitePath,
		SourceURL: siteURL,
		StartPath: startPath,
	}, nil
}

// resolveSiteID mirrors xql's resolveSiteID: GET /sites/{host}:{path}.
func resolveSiteID(ctx context.Context, g *spauth.GraphClient, hostname, sitePath string) (string, error) {
	var path string
	if sitePath == "" {
		path = fmt.Sprintf("/sites/%s", hostname)
	} else {
		path = fmt.Sprintf("/sites/%s:%s", hostname, sitePath)
	}
	body, err := g.Get(ctx, path, nil)
	if err != nil {
		return "", err
	}
	var site struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &site); err != nil {
		return "", fmt.Errorf("decoding site response: %w", err)
	}
	if site.ID == "" {
		return "", fmt.Errorf("site response missing id")
	}
	return site.ID, nil
}

// resolveDefaultDrive binds the site's default document library.
func resolveDefaultDrive(ctx context.Context, g *spauth.GraphClient, siteID string) (id, name string, err error) {
	body, err := g.Get(ctx, fmt.Sprintf("/sites/%s/drive", siteID), nil)
	if err != nil {
		return "", "", err
	}
	var d struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return "", "", fmt.Errorf("decoding default drive: %w", err)
	}
	return d.ID, d.Name, nil
}

// driveMeta is the subset of a Graph drive used to match a URL to a library.
type driveMeta struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	WebURL string `json:"webUrl"`
}

// listDrives returns every document library on the site, with the webUrl needed
// to match one against a deep-linked URL.
func listDrives(ctx context.Context, g *spauth.GraphClient, siteID string) ([]driveMeta, error) {
	raws, err := g.GetAll(ctx, fmt.Sprintf("/sites/%s/drives", siteID), url.Values{
		"$select": {"id,name,webUrl"},
	})
	if err != nil {
		return nil, err
	}
	drives := make([]driveMeta, 0, len(raws))
	for _, raw := range raws {
		var d driveMeta
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("decoding drive entry: %w", err)
		}
		drives = append(drives, d)
	}
	return drives, nil
}

// resolveDriveByName matches a library by display name (case-insensitive).
func resolveDriveByName(ctx context.Context, g *spauth.GraphClient, siteID, library string) (id, name string, err error) {
	drives, err := listDrives(ctx, g, siteID)
	if err != nil {
		return "", "", err
	}
	names := make([]string, 0, len(drives))
	for _, d := range drives {
		names = append(names, d.Name)
		if strings.EqualFold(d.Name, library) {
			return d.ID, d.Name, nil
		}
	}
	return "", "", fmt.Errorf("no library named %q (found: %s)", library, strings.Join(names, ", "))
}

// resolveDriveFromURL matches the library whose webUrl the URL points into and
// returns the folder remainder as the starting path. When no library matches
// (an unusual URL shape), it falls back to the site's default library.
func resolveDriveFromURL(ctx context.Context, g *spauth.GraphClient, siteID, rawURL string) (id, name, startPath string, err error) {
	drives, err := listDrives(ctx, g, siteID)
	if err != nil {
		return "", "", "", err
	}
	if m, sp, ok := matchDriveByURL(rawURL, drives); ok {
		return m.ID, m.Name, sp, nil
	}
	id, name, err = resolveDefaultDrive(ctx, g, siteID)
	return id, name, "", err
}

// matchDriveByURL picks the drive whose webUrl is the longest path-prefix of
// rawURL and returns the library-relative folder path remaining after it. It
// honors the modern web-UI deep link, which carries the real folder in an "id"
// query parameter rather than the path. Matching is case-insensitive (SharePoint
// paths are), and a trailing Forms/<View>.aspx is dropped since that's a list
// view, not a folder. ok is false when no drive's host+path prefixes the URL.
func matchDriveByURL(rawURL string, drives []driveMeta) (match driveMeta, startPath string, ok bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return driveMeta{}, "", false
	}
	inHost := strings.ToLower(u.Host)
	effPath := u.Path
	if id := u.Query().Get("id"); id != "" {
		effPath = id
	}
	inSegs := urlPathSegments(effPath)
	inSegs = inSegs[shareLinkPrefixLen(inSegs):]
	if len(inSegs) == 0 {
		return driveMeta{}, "", false
	}

	best := -1
	var remainder []string
	for _, d := range drives {
		du, perr := url.Parse(d.WebURL)
		if perr != nil || strings.ToLower(du.Host) != inHost {
			continue
		}
		dSegs := urlPathSegments(du.Path)
		if len(dSegs) == 0 || len(dSegs) > len(inSegs) || !segsHasPrefix(inSegs, dSegs) {
			continue
		}
		if len(dSegs) > best {
			best = len(dSegs)
			match = d
			remainder = inSegs[len(dSegs):]
		}
	}
	if best < 0 {
		return driveMeta{}, "", false
	}
	return match, strings.Join(stripViewSuffix(remainder), "/"), true
}

// shareLinkPrefixLen reports how many leading segments form a SharePoint
// "Copy link" sharing-URL prefix that sits in front of the real server-relative
// path — e.g. /:f:/r/sites/... or /:w:/g/.... The first segment is a :type:
// token (a short code wrapped in colons: :f: folder, :w:/:x:/:p: Office docs,
// etc.); the second is a single-letter routing action (r = redirect, g = guest,
// s, u, ...). Returns 0 when segs doesn't start with such a prefix, so plain
// /sites/... URLs are untouched.
func shareLinkPrefixLen(segs []string) int {
	if len(segs) == 0 {
		return 0
	}
	s0 := segs[0]
	if len(s0) < 2 || !strings.HasPrefix(s0, ":") || !strings.HasSuffix(s0, ":") {
		return 0
	}
	if len(segs) >= 2 && len(segs[1]) == 1 {
		return 2
	}
	return 1
}

// urlPathSegments splits an already-decoded URL path into its non-empty
// segments. url.Parse decodes %20 into the path, so segments carry spaces.
func urlPathSegments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// segsHasPrefix reports whether prefix matches the leading segments of segs,
// compared case-insensitively.
func segsHasPrefix(segs, prefix []string) bool {
	for i := range prefix {
		if !strings.EqualFold(segs[i], prefix[i]) {
			return false
		}
	}
	return true
}

// stripViewSuffix drops a trailing Forms/<View>.aspx, the SharePoint list-view
// URL that isn't a real folder.
func stripViewSuffix(segs []string) []string {
	if len(segs) > 0 && strings.HasSuffix(strings.ToLower(segs[len(segs)-1]), ".aspx") {
		segs = segs[:len(segs)-1]
		if len(segs) > 0 && strings.EqualFold(segs[len(segs)-1], "Forms") {
			segs = segs[:len(segs)-1]
		}
	}
	return segs
}

// itemRef builds the Graph drive-item addressing segment for a library-relative
// path. "" or "/" addresses the drive root; otherwise each segment is
// percent-escaped and wrapped in the root:/<path>: path-addressing form.
func itemRef(path string) string {
	clean := strings.Trim(path, "/")
	if clean == "" {
		return "/root"
	}
	segs := strings.Split(clean, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return "/root:/" + strings.Join(segs, "/") + ":"
}

// driveItemJSON is the subset of a Graph driveItem xftp reads.
type driveItemJSON struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Size                 int64  `json:"size"`
	LastModifiedDateTime string `json:"lastModifiedDateTime"`
	Folder               *struct {
		ChildCount int `json:"childCount"`
	} `json:"folder"`
	File *struct {
		Hashes struct {
			QuickXorHash string `json:"quickXorHash"`
		} `json:"hashes"`
	} `json:"file"`
	FileSystemInfo *struct {
		LastModifiedDateTime string `json:"lastModifiedDateTime"`
	} `json:"fileSystemInfo"`
}

func (j driveItemJSON) toItem() Item {
	it := Item{ID: j.ID, Name: j.Name, Size: j.Size}
	if j.Folder != nil {
		it.IsFolder = true
		it.ChildCount = j.Folder.ChildCount
	}
	if t, err := time.Parse(time.RFC3339, j.LastModifiedDateTime); err == nil {
		it.LastModified = t
	}
	it.FSModified = it.LastModified
	if j.FileSystemInfo != nil {
		if t, err := time.Parse(time.RFC3339, j.FileSystemInfo.LastModifiedDateTime); err == nil {
			it.FSModified = t
		}
	}
	if j.File != nil {
		it.QuickXorHash = j.File.Hashes.QuickXorHash
	}
	return it
}

// List returns the children of a library-relative folder path ("" or "/" for
// the library root). This is the read primitive behind an FTP "ls".
func (d *Drive) List(ctx context.Context, g *spauth.GraphClient, path string) ([]Item, error) {
	endpoint := fmt.Sprintf("/drives/%s%s/children", d.DriveID, itemRef(path))
	raws, err := g.GetAll(ctx, endpoint, url.Values{
		"$select": {"id,name,size,lastModifiedDateTime,folder,file,fileSystemInfo"},
	})
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(raws))
	for _, raw := range raws {
		var j driveItemJSON
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, fmt.Errorf("decoding drive item: %w", err)
		}
		items = append(items, j.toItem())
	}
	return items, nil
}

// SortChildren orders a folder listing in place: case-insensitively by name,
// with the exact name as a stable tiebreaker. Both xfind and xtree walk with
// this ordering so their output is deterministic and matches between the two.
func SortChildren(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		li, lj := strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)
		if li != lj {
			return li < lj
		}
		return items[i].Name < items[j].Name
	})
}

// Walk recursively visits every item beneath the library-relative root folder,
// depth first, in SortChildren order. For each item it calls visit with the
// item, its library-relative path, its depth below root (root's direct children
// are depth 1), and whether it is the last child of its parent (so a tree view
// can draw └── vs ├──). When visit returns false for a folder, the walk does not
// descend into it — that's how depth and type filters prune the traversal. A
// List failure on any folder stops the walk and is returned.
func (d *Drive) Walk(ctx context.Context, g *spauth.GraphClient, root string,
	visit func(it Item, itemPath string, depth int, isLast bool) (descend bool)) error {
	return d.walkDir(ctx, g, root, 1, visit)
}

func (d *Drive) walkDir(ctx context.Context, g *spauth.GraphClient, dir string, depth int,
	visit func(it Item, itemPath string, depth int, isLast bool) bool) error {
	items, err := d.List(ctx, g, dir)
	if err != nil {
		return fmt.Errorf("listing /%s: %w", strings.Trim(dir, "/"), err)
	}
	SortChildren(items)
	for i, it := range items {
		p := path.Join(dir, it.Name)
		isLast := i == len(items)-1
		descend := visit(it, p, depth, isLast)
		if it.IsFolder && descend {
			if err := d.walkDir(ctx, g, p, depth+1, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// simpleUploadMax is Graph's ceiling for a single PUT to /content. Files at or
// below this go in one request; larger files use a chunked upload session.
const simpleUploadMax = 250 * 1024 * 1024

// uploadChunkSize is the byte count per PUT within an upload session. Graph
// requires every chunk except the last to be a multiple of 320 KiB; 10 MiB
// satisfies that and keeps the round-trip count low without holding much in
// memory.
const uploadChunkSize = 10 * 1024 * 1024

// Stat returns metadata for a single item at the library-relative path ("" or
// "/" for the drive root). Used to validate "cd" targets and to size downloads.
func (d *Drive) Stat(ctx context.Context, g *spauth.GraphClient, path string) (Item, error) {
	body, err := g.Get(ctx, fmt.Sprintf("/drives/%s%s", d.DriveID, itemRef(path)), url.Values{
		"$select": {"id,name,size,lastModifiedDateTime,folder,file,fileSystemInfo"},
	})
	if err != nil {
		return Item{}, err
	}
	var j driveItemJSON
	if err := json.Unmarshal(body, &j); err != nil {
		return Item{}, fmt.Errorf("decoding item: %w", err)
	}
	return j.toItem(), nil
}

// Download streams the content of a remote file at the library-relative path
// into w. (FTP "get".)
func (d *Drive) Download(ctx context.Context, g *spauth.GraphClient, path string, w io.Writer) error {
	rc, err := g.GetStream(ctx, fmt.Sprintf("/drives/%s%s/content", d.DriveID, itemRef(path)))
	if err != nil {
		return err
	}
	defer rc.Close()
	if _, err := io.Copy(w, rc); err != nil {
		return fmt.Errorf("streaming download: %w", err)
	}
	return nil
}

// Upload streams size bytes from r to the library-relative remote path. (FTP
// "put".) Files at or below SimpleUploadMax go in a single PUT; larger files use
// a chunked, resumable upload session. An existing file at the path is replaced.
func (d *Drive) Upload(ctx context.Context, g *spauth.GraphClient, path, contentType string, r io.Reader, size int64) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if size <= simpleUploadMax {
		data, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("reading upload data: %w", err)
		}
		_, err = g.PutRaw(ctx, fmt.Sprintf("/drives/%s%s/content", d.DriveID, itemRef(path)), contentType, data)
		return err
	}
	return d.uploadSession(ctx, g, path, r, size)
}

// uploadSession uploads a large file in chunks. It opens a Graph upload session,
// then streams uploadChunkSize-byte ranges from r until size bytes are sent. The
// session is cancelled on failure so a partial upload leaves nothing behind.
func (d *Drive) uploadSession(ctx context.Context, g *spauth.GraphClient, path string, r io.Reader, size int64) error {
	body, err := g.Post(ctx, fmt.Sprintf("/drives/%s%s/createUploadSession", d.DriveID, itemRef(path)),
		map[string]interface{}{
			"item": map[string]interface{}{
				"@microsoft.graph.conflictBehavior": "replace",
			},
		})
	if err != nil {
		return fmt.Errorf("creating upload session: %w", err)
	}
	var sess struct {
		UploadURL string `json:"uploadUrl"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		return fmt.Errorf("decoding upload session: %w", err)
	}
	if sess.UploadURL == "" {
		return fmt.Errorf("upload session response missing uploadUrl")
	}

	// Cancel the session on any failure using a fresh context, so cleanup still
	// runs even when the upload was aborted by ctx cancellation (Ctrl-C).
	cancelSession := func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		g.CancelUploadSession(cctx, sess.UploadURL)
	}

	buf := make([]byte, uploadChunkSize)
	var sent int64
	for sent < size {
		n, readErr := io.ReadFull(r, buf)
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			cancelSession()
			return fmt.Errorf("reading upload data: %w", readErr)
		}
		if n == 0 {
			break
		}
		if _, _, err := g.UploadChunk(ctx, sess.UploadURL, buf[:n], sent, size); err != nil {
			cancelSession()
			return err
		}
		sent += int64(n)
	}
	if sent != size {
		cancelSession()
		return fmt.Errorf("upload incomplete: sent %d of %d bytes", sent, size)
	}
	return nil
}

// SetMTime writes the filesystem last-modified time of the item at the
// library-relative path into its fileSystemInfo — the timestamp OneDrive clients
// use to mirror local mtimes (unlike lastModifiedDateTime, which is
// service-controlled and read-only). xsync stamps it after an upload so the
// remote copy carries the source file's mtime, keeping later size+mtime
// comparisons stable instead of re-uploading on every run. The time is sent at
// whole-second precision in UTC, which Graph accepts unambiguously.
func (d *Drive) SetMTime(ctx context.Context, g *spauth.GraphClient, path string, t time.Time) error {
	body := map[string]interface{}{
		"fileSystemInfo": map[string]string{
			"lastModifiedDateTime": t.UTC().Format(time.RFC3339),
		},
	}
	_, err := g.Patch(ctx, fmt.Sprintf("/drives/%s%s", d.DriveID, itemRef(path)), body)
	return err
}

// Mkdir creates a folder at the library-relative path. (FTP "mkdir".) Fails if
// something already exists at that path.
func (d *Drive) Mkdir(ctx context.Context, g *spauth.GraphClient, path string) error {
	parent, leaf := splitPath(path)
	if leaf == "" {
		return fmt.Errorf("mkdir: empty folder name")
	}
	body := map[string]interface{}{
		"name":                              leaf,
		"folder":                            map[string]interface{}{},
		"@microsoft.graph.conflictBehavior": "fail",
	}
	_, err := g.Post(ctx, fmt.Sprintf("/drives/%s%s/children", d.DriveID, itemRef(parent)), body)
	return err
}

// Remove deletes the file or folder at the library-relative path. (FTP
// "delete"/"rmdir".) Folder deletes are recursive in Graph, so callers should
// confirm first.
func (d *Drive) Remove(ctx context.Context, g *spauth.GraphClient, path string) error {
	if strings.Trim(path, "/") == "" {
		return fmt.Errorf("refusing to delete the drive root")
	}
	return g.Delete(ctx, fmt.Sprintf("/drives/%s%s", d.DriveID, itemRef(path)))
}

// Move renames or relocates an item from src to dst (both library-relative).
// (FTP "rename".) It resolves the destination's parent folder to an item ID and
// PATCHes the source — the id-based parentReference is more reliable than the
// path-based form.
func (d *Drive) Move(ctx context.Context, g *spauth.GraphClient, src, dst string) error {
	if strings.Trim(src, "/") == "" {
		return fmt.Errorf("refusing to move the drive root")
	}
	dstParent, dstLeaf := splitPath(dst)
	if dstLeaf == "" {
		return fmt.Errorf("move: empty destination name")
	}
	parentID, err := d.itemID(ctx, g, dstParent)
	if err != nil {
		return fmt.Errorf("resolving destination folder: %w", err)
	}
	body := map[string]interface{}{
		"name":            dstLeaf,
		"parentReference": map[string]string{"id": parentID},
	}
	_, err = g.Patch(ctx, fmt.Sprintf("/drives/%s%s", d.DriveID, itemRef(src)), body)
	return err
}

// itemID returns the Graph item ID for a library-relative path ("" or "/" for
// the root).
func (d *Drive) itemID(ctx context.Context, g *spauth.GraphClient, path string) (string, error) {
	body, err := g.Get(ctx, fmt.Sprintf("/drives/%s%s", d.DriveID, itemRef(path)), url.Values{
		"$select": {"id"},
	})
	if err != nil {
		return "", err
	}
	var j struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return "", fmt.Errorf("decoding item id: %w", err)
	}
	if j.ID == "" {
		return "", fmt.Errorf("item response missing id")
	}
	return j.ID, nil
}

// splitPath splits a library-relative path into its parent path and leaf name.
// "Docs/Reports/q1.xlsx" -> ("Docs/Reports", "q1.xlsx"); a top-level name
// returns an empty parent (the root).
func splitPath(p string) (parent, leaf string) {
	clean := strings.Trim(p, "/")
	if clean == "" {
		return "", ""
	}
	i := strings.LastIndex(clean, "/")
	if i < 0 {
		return "", clean
	}
	return clean[:i], clean[i+1:]
}
