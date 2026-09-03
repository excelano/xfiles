//go:build live

package drive

// Live tests run against a real library through the shared token cache. They
// are the pre-release pass the unit tests cannot be: CI holds no token, so
// these run locally on a machine that has signed in once.
//
//	XFILES_LIVE_SITE=https://<tenant>.sharepoint.com/sites/<test-site> go test -tags live ./...
//
// Each test owns a folder named for the run in the site's default library and
// removes it on the way out.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/excelano/spauth"
)

func liveDrive(t *testing.T) (context.Context, *spauth.GraphClient, *Drive) {
	t.Helper()
	site := os.Getenv("XFILES_LIVE_SITE")
	if site == "" {
		t.Fatal("XFILES_LIVE_SITE is not set; point it at a SharePoint test site to run the live tests")
	}
	ctx := context.Background()
	client, err := spauth.NewPublicClient(spauth.CachePath(), "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := spauth.CheckStatus(ctx, client, spauth.CachePath())
	if err != nil || !st.SignedIn {
		t.Fatalf("no usable session in %s; sign in with any Excelano SharePoint tool first (%v %s)", spauth.CachePath(), err, st.Reason)
	}
	accounts, _ := client.Accounts(ctx)
	g := spauth.NewGraphClient(client, accounts[0])
	d, err := ResolveDrive(ctx, g, site, "")
	if err != nil {
		t.Fatalf("resolving %s: %v", site, err)
	}
	return ctx, g, d
}

// fixtureFolder makes a folder this test owns and schedules its removal.
func fixtureFolder(t *testing.T, ctx context.Context, g *spauth.GraphClient, d *Drive) string {
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
	if err := d.Upload(ctx, g, folder+"/small.txt", "text/plain", bytes.NewReader(small), int64(len(small))); err != nil {
		t.Fatalf("Upload (single PUT): %v", err)
	}

	// The session path is only taken above simpleUploadMax on a real Upload;
	// drive it directly with a body large enough to matter but cheap to send.
	large := make([]byte, 1<<20)
	if _, err := rand.Read(large); err != nil {
		t.Fatal(err)
	}
	if err := d.uploadSession(ctx, g, folder+"/large.bin", bytes.NewReader(large), int64(len(large))); err != nil {
		t.Fatalf("upload session: %v", err)
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

// Walk is what xtree and xfind are built on: it has to visit a nested tree in
// order, report depth, and honour foldersOnly.
func TestLiveWalk(t *testing.T) {
	ctx, g, d := liveDrive(t)
	folder := fixtureFolder(t, ctx, g, d)
	for _, p := range []string{folder + "/a", folder + "/a/b"} {
		if err := d.Mkdir(ctx, g, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{folder + "/top.txt", folder + "/a/b/deep.txt"} {
		if err := d.Upload(ctx, g, p, "text/plain", bytes.NewReader([]byte("x")), 1); err != nil {
			t.Fatal(err)
		}
	}

	var seen []string
	err := d.Walk(ctx, g, folder, false, func(it Item, itemPath string, depth int, isLast bool) bool {
		seen = append(seen, fmt.Sprintf("%d:%s", depth, it.Name))
		return true
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []string{"1:a", "2:b", "3:deep.txt", "1:top.txt"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("Walk visited %v, want %v", seen, want)
	}

	seen = nil
	err = d.Walk(ctx, g, folder, true, func(it Item, itemPath string, depth int, isLast bool) bool {
		seen = append(seen, it.Name)
		return true
	})
	if err != nil {
		t.Fatalf("Walk foldersOnly: %v", err)
	}
	if fmt.Sprint(seen) != fmt.Sprint([]string{"a", "b"}) {
		t.Errorf("foldersOnly walk visited %v, want [a b]", seen)
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
