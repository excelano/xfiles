//go:build live

// Author: David M. Anderson
// Built with AI assistance (Claude, Anthropic)

package drive

// Live tests run against a real library through the shared token cache. They
// are the pre-release pass the unit tests cannot be: CI holds no token, so
// these run locally on a machine that has signed in once.
//
//	XFILES_LIVE_SITE=https://<tenant>.sharepoint.com/sites/<test-site> go test -tags live ./...
//
// Each test owns a folder named for the run in the site's default library and
// removes it on the way out, except the search test, whose fixture stays in
// place because the search index can take hours to see a new file.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/excelano/graphauth"
)

func liveDrive(t *testing.T) (context.Context, *graphauth.GraphClient, *Drive) {
	t.Helper()
	site := os.Getenv("XFILES_LIVE_SITE")
	if site == "" {
		t.Fatal("XFILES_LIVE_SITE is not set; point it at a SharePoint test site to run the live tests")
	}
	ctx := context.Background()
	client, err := graphauth.SharePoint.NewPublicClient(graphauth.SharePoint.CachePath(), "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := graphauth.SharePoint.CheckStatus(ctx, client, graphauth.SharePoint.CachePath())
	if err != nil || !st.SignedIn {
		t.Fatalf("no usable session in %s; sign in with any Excelano SharePoint tool first (%v %s)", graphauth.SharePoint.CachePath(), err, st.Reason)
	}
	accounts, _ := client.Accounts(ctx)
	g := graphauth.SharePoint.NewGraphClient(client, accounts[0])
	d, err := ResolveDrive(ctx, g, site, "")
	if err != nil {
		t.Fatalf("resolving %s: %v", site, err)
	}
	return ctx, g, d
}

// fixtureFolder makes a folder this test owns and schedules its removal.
func fixtureFolder(t *testing.T, ctx context.Context, g *graphauth.GraphClient, d *Drive) string {
	t.Helper()
	folder := fmt.Sprintf("xfiles-live-%d", time.Now().UnixNano())
	if err := d.Mkdir(ctx, g, folder); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() {
		if err := d.Remove(ctx, g, folder); err != nil {
			t.Errorf("cleanup: removing %s: %v", folder, err)
		}
	})
	return folder
}

// The file lifecycle every command relies on: upload by both paths, list,
// stat, download, touch, rename, remove.
func TestLiveFileLifecycle(t *testing.T) {
	ctx, g, d := liveDrive(t)
	folder := fixtureFolder(t, ctx, g, d)

	if err := d.MkdirIfMissing(ctx, g, folder); err != nil {
		t.Fatalf("MkdirIfMissing on an existing folder: %v", err)
	}

	small := []byte("xfiles live probe\n")
	put, err := d.Upload(ctx, g, folder+"/small.txt", "text/plain", bytes.NewReader(small), int64(len(small)))
	if err != nil {
		t.Fatalf("Upload (single PUT): %v", err)
	}
	if put.WebURL == "" {
		t.Error("Upload (single PUT) returned no webUrl")
	}

	// The session path is only taken above simpleUploadMax on a real Upload;
	// drive it directly with a body large enough to matter but cheap to send.
	large := make([]byte, 1<<20)
	if _, err := rand.Read(large); err != nil {
		t.Fatal(err)
	}
	body, err := d.uploadSession(ctx, g, folder+"/large.bin", bytes.NewReader(large), int64(len(large)))
	if err != nil {
		t.Fatalf("upload session: %v", err)
	}
	var finished driveItemJSON
	if err := json.Unmarshal(body, &finished); err != nil || finished.WebURL == "" {
		t.Errorf("upload session's final chunk carried no webUrl (err %v)", err)
	}

	items, err := d.List(ctx, g, folder)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sizes := map[string]int64{}
	for _, it := range items {
		sizes[it.Name] = it.Size
	}
	if sizes["small.txt"] != int64(len(small)) || sizes["large.bin"] != int64(len(large)) {
		t.Errorf("List sizes = %v, want small=%d large=%d", sizes, len(small), len(large))
	}

	for name, want := range map[string][]byte{"small.txt": small, "large.bin": large} {
		var buf bytes.Buffer
		if err := d.Download(ctx, g, folder+"/"+name, &buf); err != nil {
			t.Fatalf("Download %s: %v", name, err)
		}
		if !bytes.Equal(buf.Bytes(), want) {
			t.Errorf("%s: downloaded %d bytes that differ from the %d uploaded", name, buf.Len(), len(want))
		}
	}

	stamp := time.Date(2020, 5, 17, 9, 30, 0, 0, time.UTC)
	if err := d.SetMTime(ctx, g, folder+"/small.txt", stamp); err != nil {
		t.Fatalf("SetMTime: %v", err)
	}
	it, err := d.Stat(ctx, g, folder+"/small.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !it.FSModified.Equal(stamp) {
		t.Errorf("FSModified after SetMTime = %v, want %v", it.FSModified, stamp)
	}

	if err := d.Move(ctx, g, folder+"/small.txt", folder+"/moved.txt"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := d.Stat(ctx, g, folder+"/small.txt"); err == nil {
		t.Error("source still resolves after Move")
	}
	if _, err := d.Stat(ctx, g, folder+"/moved.txt"); err != nil {
		t.Errorf("destination does not resolve after Move: %v", err)
	}

	if err := d.Remove(ctx, g, folder+"/moved.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := d.Stat(ctx, g, folder+"/moved.txt"); err == nil {
		t.Error("file still resolves after Remove")
	}
}

// Walk is what xtree, xfind and xsync are built on: it has to visit a nested
// tree in order, report depth, and honour foldersOnly and maxDepth, the same
// way whether it fetched the tree by delta query or folder by folder.
func TestLiveWalk(t *testing.T) {
	ctx, g, d := liveDrive(t)
	folder := fixtureFolder(t, ctx, g, d)
	for _, p := range []string{folder + "/a", folder + "/a/b", folder + "/Mixed Case"} {
		if err := d.Mkdir(ctx, g, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{folder + "/top.txt", folder + "/a/b/data.csv", folder + "/a/b/deep.txt", folder + "/Mixed Case/x #1.txt"} {
		if _, err := d.Upload(ctx, g, p, "text/plain", bytes.NewReader([]byte("x")), 1); err != nil {
			t.Fatal(err)
		}
	}

	// The delta path must not be failing over to listing unnoticed.
	children, err := d.subtree(ctx, g, folder)
	if err != nil {
		t.Fatalf("delta subtree: %v", err)
	}
	if n := len(children[strings.ToLower(folder+"/Mixed Case")]); n != 1 {
		t.Errorf("delta subtree has %d children under Mixed Case, want 1", n)
	}

	walk := func(root string, maxDepth int, foldersOnly bool) []string {
		t.Helper()
		var seen []string
		err := d.Walk(ctx, g, root, maxDepth, foldersOnly, func(it Item, itemPath string, depth int, isLast bool) bool {
			seen = append(seen, fmt.Sprintf("%d:%s:%v", depth, it.Name, isLast))
			return true
		})
		if err != nil {
			t.Fatalf("Walk(%q, %d, %v): %v", root, maxDepth, foldersOnly, err)
		}
		return seen
	}

	want := []string{"1:a:false", "2:b:true", "3:data.csv:false", "3:deep.txt:true", "1:Mixed Case:false", "2:x #1.txt:true", "1:top.txt:true"}
	byDelta := walk(folder, 0, false)
	if fmt.Sprint(byDelta) != fmt.Sprint(want) {
		t.Errorf("delta walk visited %v, want %v", byDelta, want)
	}
	if byListing := walk(folder, 99, false); fmt.Sprint(byListing) != fmt.Sprint(byDelta) {
		t.Errorf("listing walk visited %v, delta walk %v", byListing, byDelta)
	}
	if upper := walk(strings.ToUpper(folder), 0, false); fmt.Sprint(upper) != fmt.Sprint(byDelta) {
		t.Errorf("delta walk from an upper-cased root visited %v, want %v", upper, byDelta)
	}
	if got := walk(folder, 0, true); fmt.Sprint(got) != fmt.Sprint([]string{"1:a:false", "2:b:true", "1:Mixed Case:true"}) {
		t.Errorf("foldersOnly walk visited %v", got)
	}
	if got := walk(folder, 1, false); fmt.Sprint(got) != fmt.Sprint([]string{"1:a:false", "1:Mixed Case:false", "1:top.txt:true"}) {
		t.Errorf("maxDepth 1 walk visited %v", got)
	}
}

// ResolveDrive's three selection rules against one site: default library,
// explicit --library name, and a folder URL below the site that both picks
// the library and sets the starting path.
func TestLiveResolveDriveVariants(t *testing.T) {
	ctx, g, byDefault := liveDrive(t)
	site := os.Getenv("XFILES_LIVE_SITE")
	folder := fixtureFolder(t, ctx, g, byDefault)

	byName, err := ResolveDrive(ctx, g, site, byDefault.Name)
	if err != nil {
		t.Fatalf("ResolveDrive by library name %q: %v", byDefault.Name, err)
	}
	if byName.DriveID != byDefault.DriveID {
		t.Errorf("by name bound drive %s, default bound %s", byName.DriveID, byDefault.DriveID)
	}
	if _, err := ResolveDrive(ctx, g, site, "no-such-library-"+folder); err == nil {
		t.Error("ResolveDrive with an unknown library name should fail")
	}

	// The library's webUrl segment for the default library is "Shared Documents"
	// on English-language sites; derive it from the drive rather than assume.
	body, err := g.Get(ctx, "/drives/"+byDefault.DriveID, nil)
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		WebURL string `json:"webUrl"`
	}
	if err := json.Unmarshal(body, &meta); err != nil || meta.WebURL == "" {
		t.Fatalf("drive has no webUrl: %v", err)
	}
	byURL, err := ResolveDrive(ctx, g, meta.WebURL+"/"+folder, "")
	if err != nil {
		t.Fatalf("ResolveDrive by folder URL: %v", err)
	}
	if byURL.DriveID != byDefault.DriveID {
		t.Errorf("by URL bound drive %s, default bound %s", byURL.DriveID, byDefault.DriveID)
	}
	if byURL.StartPath != folder {
		t.Errorf("by URL StartPath = %q, want %q", byURL.StartPath, folder)
	}
}

// searchFixture is the folder the search test keeps permanently, and
// searchTerm the word only its files contain.
const (
	searchFixture = "xfiles-live-search"
	searchTerm    = "xfilessearchfixture"
)

// Search against a fixture that outlives the run: a hit inside the searched
// folder and a hit outside it, both carrying the same word in their contents
// only. The first run creates the fixture and skips, since the index cannot see
// it yet; a later run must find the inside hit and not the outside one.
func TestLiveSearch(t *testing.T) {
	ctx, g, d := liveDrive(t)
	inside, outside := searchFixture+"/Inside/hit.txt", searchFixture+"/Outside/hit.txt"

	item, err := d.Stat(ctx, g, inside)
	if err != nil {
		for _, p := range []string{searchFixture, searchFixture + "/Inside", searchFixture + "/Outside"} {
			if err := d.MkdirIfMissing(ctx, g, p); err != nil {
				t.Fatal(err)
			}
		}
		body := []byte("This file exists for the xfiles search test: " + searchTerm + "\n")
		for _, p := range []string{inside, outside} {
			if _, err := d.Upload(ctx, g, p, "text/plain", bytes.NewReader(body), int64(len(body))); err != nil {
				t.Fatal(err)
			}
		}
		t.Skipf("created /%s; the search index can take hours to see it, so rerun the live tests later", searchFixture)
	}

	hits, err := d.Search(ctx, g, searchFixture+"/Inside", searchTerm)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var paths []string
	for _, h := range hits {
		paths = append(paths, h.Path)
	}
	if len(paths) == 0 && time.Since(item.LastModified) < 24*time.Hour {
		t.Skipf("/%s was created %s ago and is not indexed yet; rerun later", searchFixture, time.Since(item.LastModified).Round(time.Minute))
	}
	if fmt.Sprint(paths) != fmt.Sprint([]string{inside}) {
		t.Errorf("Search of /%s/Inside returned %v, want [%s]", searchFixture, paths, inside)
	}
	if len(hits) == 1 && hits[0].WebURL == "" {
		t.Error("search hit carries no webUrl")
	}
}
