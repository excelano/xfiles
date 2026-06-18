// Command xcp is the scp-style companion to xftp: a one-shot, non-interactive
// copy between the local filesystem and a SharePoint document library over
// Microsoft Graph. Exactly one of the two arguments is a SharePoint URL, and
// its position decides the direction — `xcp report.xlsx <url>` uploads,
// `xcp <url> ./` downloads — the way scp keys off which side carries host:.
// Authentication is device-code; refresh tokens are cached under ~/.config/xcp.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/excelano/xftp/internal/spauth"
)

func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "xcp")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".xcp"
	}
	return filepath.Join(home, ".config", "xcp")
}

// version is stamped at build time via -ldflags by goreleaser.
var version = "(devel)"

// direction is which way a copy goes, inferred from which argument is a URL.
type direction int

const (
	upload direction = iota
	download
)

// isURL reports whether s looks like a SharePoint URL (and therefore the remote
// side of the copy). SharePoint is always https; http is accepted for symmetry.
func isURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")
}

// classify decides the copy direction from the two operands. Exactly one must be
// a URL: two URLs would be a server-to-server copy (unsupported), two locals are
// a job for cp.
func classify(src, dst string) (direction, error) {
	srcURL, dstURL := isURL(src), isURL(dst)
	switch {
	case srcURL && !dstURL:
		return download, nil
	case !srcURL && dstURL:
		return upload, nil
	case srcURL && dstURL:
		return 0, fmt.Errorf("both arguments are URLs; remote-to-remote copy is not supported")
	default:
		return 0, fmt.Errorf("one argument must be a SharePoint URL; for local-to-local copies use cp")
	}
}

// uploadRemote computes the library-relative destination path for an upload.
// startPath is where the URL pointed (the library root is ""). An existing
// folder there means "copy into it" under the local file's name; an existing
// file means overwrite; a path that doesn't exist is taken as the target name.
func uploadRemote(startPath string, destExists, destIsFolder bool, localBase string) string {
	switch {
	case destExists && destIsFolder:
		return path.Join(startPath, localBase)
	case destExists && !destIsFolder:
		return startPath
	default:
		if startPath == "" {
			return localBase
		}
		return startPath
	}
}

// downloadLocal computes the local destination path for a download. A dst that
// is an existing directory means "download into it" under the remote file's
// name; otherwise dst is the file path to write.
func downloadLocal(dst string, dstIsDir bool, remoteBase string) string {
	if dstIsDir {
		return filepath.Join(dst, remoteBase)
	}
	return dst
}

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("xcp", flag.ContinueOnError)
	library := fs.String("library", "", "Document library display name (default: inferred from the URL, else the site's default library)")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "V", false, "print version and exit (shorthand)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: xcp [--library <name>] <src> <dst>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Copy a file between the local filesystem and SharePoint. Exactly one of")
		fmt.Fprintln(os.Stderr, "<src>/<dst> is a SharePoint URL; its position sets the direction:")
		fmt.Fprintln(os.Stderr, "  xcp report.xlsx https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports")
		fmt.Fprintln(os.Stderr, "  xcp https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports/Q1.xlsx ./")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Authentication is device-code via Microsoft Graph; refresh tokens are")
		fmt.Fprintln(os.Stderr, "cached at "+filepath.Join(configDir(), "sp-token.json")+".")
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Println(version)
		return 0
	}
	args := fs.Args()
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "Error: exactly two arguments are required (a source and a destination)")
		fs.Usage()
		return 2
	}
	src, dst := args[0], args[1]
	dir, err := classify(src, dst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fs.Usage()
		return 2
	}

	ctx := context.Background()
	tokenCachePath := filepath.Join(configDir(), "sp-token.json")

	client, err := spauth.NewPublicClient(tokenCachePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup error: %v\n", err)
		return 1
	}
	result, err := spauth.Authenticate(ctx, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Authentication failed: %v%s\n", err, spauth.HintForAuthError(err))
		return 1
	}
	graph := spauth.NewGraphClient(client, result.Account)
	fmt.Fprintf(os.Stderr, "Authenticated as: %s\n", result.Account.PreferredUsername)

	if dir == download {
		return runDownload(ctx, graph, src, dst, *library)
	}
	return runUpload(ctx, graph, src, dst, *library)
}
