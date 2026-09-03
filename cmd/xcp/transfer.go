package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"

	"github.com/excelano/spauth"
	"github.com/excelano/xfiles/internal/drive"
	"github.com/excelano/xfiles/internal/xfer"
)

// runDownload copies a remote file (named by url) to the local dst. dst may be a
// directory (download into it) or a file path. Returns a process exit code.
func runDownload(ctx context.Context, g *spauth.GraphClient, url, dst, library string) int {
	d, err := drive.ResolveDrive(ctx, g, url, library)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind library: %v\n", err)
		return 1
	}
	remote := d.StartPath
	if remote == "" {
		fmt.Fprintln(os.Stderr, "Error: the URL must point to a file to download, not just a site or library")
		return 1
	}

	tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	// "-" as the destination streams the file to stdout (cat), leaving stdout
	// clean for piping. Status lines stay on stderr.
	if dst == "-" {
		if err := xfer.DownloadStream(tctx, g, d, remote, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "download failed: %v\n", err)
			return 1
		}
		return 0
	}

	dstIsDir := false
	if info, err := os.Stat(dst); err == nil && info.IsDir() {
		dstIsDir = true
	}
	localPath := downloadLocal(dst, dstIsDir, path.Base(remote))
	if !filepath.IsAbs(localPath) {
		if abs, err := filepath.Abs(localPath); err == nil {
			localPath = abs
		}
	}

	if err := xfer.Download(tctx, g, d, remote, localPath); err != nil {
		fmt.Fprintf(os.Stderr, "download failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "%s -> %s\n", url, localPath)
	return 0
}

// runUpload copies the local src file to the remote location named by url. The
// remote may be a folder (upload into it), an existing file (overwrite), or a
// new path. A src of "-" reads from stdin instead. Returns a process exit code.
func runUpload(ctx context.Context, g *spauth.GraphClient, src, url, library string) int {
	d, err := drive.ResolveDrive(ctx, g, url, library)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind library: %v\n", err)
		return 1
	}

	if src == "-" {
		return uploadStdin(ctx, g, d, url)
	}

	localPath := src
	if !filepath.IsAbs(localPath) {
		if abs, err := filepath.Abs(localPath); err == nil {
			localPath = abs
		}
	}
	if info, err := os.Stat(localPath); err != nil {
		fmt.Fprintf(os.Stderr, "upload failed: %v\n", err)
		return 1
	} else if info.IsDir() {
		fmt.Fprintf(os.Stderr, "upload failed: %s is a directory\n", src)
		return 1
	}

	// Decide where the file lands: an existing folder at the URL means "copy
	// into it"; an existing file means overwrite; anything else is the target
	// name. A Stat error is treated as "doesn't exist".
	destExists, destIsFolder := false, false
	if item, err := d.Stat(ctx, g, d.StartPath); err == nil {
		destExists, destIsFolder = true, item.IsFolder
	}
	remote := uploadRemote(d.StartPath, destExists, destIsFolder, filepath.Base(localPath))

	tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	n, err := xfer.Upload(tctx, g, d, localPath, remote)
	if err != nil {
		fmt.Fprintf(os.Stderr, "upload failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "%s -> %s (%d bytes)\n", src, url, n)
	return 0
}

// uploadStdin reads stdin to a temp spool file (so its size is known and large
// inputs still go through chunked upload), then uploads it. Because stdin has no
// name of its own, the URL must name the destination file — a folder or library
// root is rejected, since there'd be nothing to call the result.
func uploadStdin(ctx context.Context, g *spauth.GraphClient, d *drive.Drive, url string) int {
	remote := d.StartPath
	if remote == "" {
		fmt.Fprintln(os.Stderr, "Error: uploading from stdin needs the URL to name the destination file, e.g. .../Reports/out.csv")
		return 1
	}
	if item, err := d.Stat(ctx, g, remote); err == nil && item.IsFolder {
		fmt.Fprintln(os.Stderr, "Error: uploading from stdin needs the URL to name the destination file, not a folder")
		return 1
	}

	tmp, err := os.CreateTemp("", "xcp-stdin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "upload failed: %v\n", err)
		return 1
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, os.Stdin); err != nil {
		tmp.Close()
		fmt.Fprintf(os.Stderr, "upload failed: reading stdin: %v\n", err)
		return 1
	}
	if err := tmp.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "upload failed: %v\n", err)
		return 1
	}

	tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	n, err := xfer.Upload(tctx, g, d, tmpName, remote)
	if err != nil {
		fmt.Fprintf(os.Stderr, "upload failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "stdin -> %s (%d bytes)\n", url, n)
	return 0
}
