//go:build live

package xfer

// Live test for the local-file transfers xcp and xsync are built on. Needs
// XFILES_LIVE_SITE and a signed-in machine; see internal/drive/live_test.go.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/excelano/spauth"
	"github.com/excelano/xfiles/internal/drive"
)

func TestLiveUploadThenDownload(t *testing.T) {
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
	d, err := drive.ResolveDrive(ctx, g, site, "")
	if err != nil {
		t.Fatalf("resolving %s: %v", site, err)
	}

	folder := fmt.Sprintf("xfiles-live-%d", time.Now().UnixNano())
	if err := d.Mkdir(ctx, g, folder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Remove(ctx, g, folder); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	local := filepath.Join(t.TempDir(), "probe.txt")
	content := []byte("xfer live probe " + folder + "\n")
	if err := os.WriteFile(local, content, 0600); err != nil {
		t.Fatal(err)
	}
	n, err := Upload(ctx, g, d, local, folder+"/probe.txt")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("Upload reported %d bytes, want %d", n, len(content))
	}

	back := filepath.Join(t.TempDir(), "back.txt")
	if err := Download(ctx, g, d, folder+"/probe.txt", back); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("round trip changed the bytes: %q", got)
	}

	var stream bytes.Buffer
	if err := DownloadStream(ctx, g, d, folder+"/probe.txt", &stream); err != nil {
		t.Fatalf("DownloadStream: %v", err)
	}
	if !bytes.Equal(stream.Bytes(), content) {
		t.Errorf("DownloadStream changed the bytes: %q", stream.Bytes())
	}
}
