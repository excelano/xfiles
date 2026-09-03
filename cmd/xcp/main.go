// Command xcp is the scp-style companion to xftp: a one-shot, non-interactive
// copy between the local filesystem and a SharePoint document library over
// Microsoft Graph. Exactly one of the two arguments is a SharePoint URL, and
// its position decides the direction — `xcp report.xlsx <url>` uploads,
// `xcp <url> ./` downloads — the way scp keys off which side carries host:.
// Authentication is device-code; the refresh token is cached at
// ~/.config/excelano/sp-token.json, shared with xql and the other xfiles tools.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/excelano/spauth"
	"github.com/excelano/xfiles"
	"github.com/excelano/xfiles/internal/buildinfo"
	"github.com/excelano/xfiles/internal/cli"
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
	installSkill := fs.Bool("install-skill", false, "install the xfiles Claude Code skill and exit")
	uninstallSkill := fs.Bool("uninstall-skill", false, "remove the installed Claude Code skill and exit")
	usage := func(w io.Writer) {
		fs.SetOutput(w)
		fmt.Fprintln(w, "Usage: xcp [--library <name>] <src> <dst>")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Copy a file between the local filesystem and SharePoint. Exactly one of")
		fmt.Fprintln(w, "<src>/<dst> is a SharePoint URL; its position sets the direction:")
		fmt.Fprintln(w, "  xcp report.xlsx https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports")
		fmt.Fprintln(w, "  xcp https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports/Q1.xlsx ./")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Use - as the local side to stream: a - destination cats the file to stdout,")
		fmt.Fprintln(w, "a - source uploads from stdin (the URL must then name the target file).")
		fmt.Fprintln(w, "  xcp https://contoso.sharepoint.com/.../Q1.xlsx -   | xsv stats")
		fmt.Fprintln(w, "  generate-report | xcp - https://contoso.sharepoint.com/.../report.csv")
		fmt.Fprintln(w)
		fs.PrintDefaults()
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Authentication is device-code via Microsoft Graph; refresh tokens are")
		fmt.Fprintln(w, "cached at "+spauth.CachePath()+", one session shared with xql and the other xfiles tools.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, cli.ExitCodes)
	}
	fs.Usage = func() { usage(os.Stderr) }
	args := os.Args[1:]
	if cli.HelpRequested(args, fs) {
		usage(os.Stdout)
		return 0
	}
	if err := fs.Parse(cli.Reorder(args, fs)); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println("xcp " + buildinfo.Resolve(version))
		return 0
	}
	if *installSkill {
		return xfiles.InstallSkill(buildinfo.Resolve(version))
	}
	if *uninstallSkill {
		return xfiles.UninstallSkill()
	}
	args = fs.Args()
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "Error: exactly two arguments are required (a source and a destination)")
		// One URL-shaped argument is the fingerprint of an unquoted "Copy link":
		// its & characters made the shell split the command, so the destination
		// (and the rest of the URL) never reached xcp.
		if len(args) == 1 && isURL(args[0]) {
			fmt.Fprintln(os.Stderr, "\nHint: a SharePoint \"Copy link\" URL contains ? and & characters that the")
			fmt.Fprintln(os.Stderr, "shell acts on unless the whole URL is wrapped in single quotes:")
			fmt.Fprintln(os.Stderr, "  xcp 'https://…/file.xlsx?d=…&csf=1&web=1' file.xlsx")
		}
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
	// The per-tool cache this binary kept before the family shared one; adopted
	// on first run so nobody signs in again.
	legacyTokenCache := filepath.Join(configDir(), "sp-token.json")

	client, err := spauth.NewPublicClient(spauth.CachePath(), legacyTokenCache)
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
