// Command xsync is the rsync-style companion to xftp: a one-shot recursive mirror
// between a local directory tree and a SharePoint document-library folder over
// Microsoft Graph. Exactly one of the two arguments is a SharePoint URL, and its
// position sets the direction — `xsync ./reports <url>` mirrors up,
// `xsync <url> ./reports` mirrors down — the way scp/rsync key off which side
// carries host:.
//
// Like rsync, xsync transfers only files that are new or changed. The
// comparison is by modification time: it stamps each uploaded file's SharePoint
// fileSystemInfo mtime from the local file, and sets the local mtime from
// fileSystemInfo on download, so unchanged files aren't re-sent. Size is
// consulted only once the timestamp says something moved, because SharePoint
// rewrites Office documents on upload and their stored size never matches the
// source again (see differs in sync.go). --ignore-times forces the transfer of
// everything when a local edit left the mtime untouched, and
// --itemize-changes/-i prints why each file is being sent. --delete removes
// destination items missing from the source (mirroring proper); --dry-run/-n
// previews the whole plan and changes nothing. Authentication is device-code;
// refresh tokens are cached under ~/.config/xsync.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/excelano/xfiles"
	"github.com/excelano/xfiles/internal/buildinfo"
	"github.com/excelano/xfiles/internal/cli"
	"github.com/excelano/xfiles/internal/spauth"
)

func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "xsync")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".xsync"
	}
	return filepath.Join(home, ".config", "xsync")
}

// version is stamped at build time via -ldflags by goreleaser.
var version = "(devel)"

// direction is which way the mirror runs, inferred from which argument is a URL.
type direction int

const (
	upload   direction = iota // local tree -> SharePoint folder
	download                  // SharePoint folder -> local tree
)

// isURL reports whether s looks like a SharePoint URL (and therefore the remote
// side of the mirror). SharePoint is always https; http is accepted for symmetry.
func isURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")
}

// classify decides the mirror direction from the two operands. Exactly one must
// be a URL: two URLs would be a server-to-server mirror (unsupported), two
// locals are a job for rsync.
func classify(src, dst string) (direction, error) {
	srcURL, dstURL := isURL(src), isURL(dst)
	switch {
	case srcURL && !dstURL:
		return download, nil
	case !srcURL && dstURL:
		return upload, nil
	case srcURL && dstURL:
		return 0, fmt.Errorf("both arguments are URLs; remote-to-remote sync is not supported")
	default:
		return 0, fmt.Errorf("one argument must be a SharePoint URL; to sync two local trees use rsync")
	}
}

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("xsync", flag.ContinueOnError)
	library := fs.String("library", "", "Document library display name (default: inferred from the URL, else the site's default library)")
	dryRun := fs.Bool("dry-run", false, "show what would change without transferring or deleting anything")
	fs.BoolVar(dryRun, "n", false, "show what would change without transferring or deleting anything (shorthand)")
	doDelete := fs.Bool("delete", false, "delete destination items that no longer exist in the source (true mirror)")
	ignoreTimes := fs.Bool("ignore-times", false, "transfer every file, skipping the modification-time comparison")
	fs.BoolVar(ignoreTimes, "I", false, "transfer every file, skipping the modification-time comparison (shorthand)")
	itemize := fs.Bool("itemize-changes", false, "print why each file is being transferred (new, time, content, forced)")
	fs.BoolVar(itemize, "i", false, "print why each file is being transferred (shorthand)")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "V", false, "print version and exit (shorthand)")
	installSkill := fs.Bool("install-skill", false, "install the xfiles Claude Code skill and exit")
	uninstallSkill := fs.Bool("uninstall-skill", false, "remove the installed Claude Code skill and exit")
	usage := func(w io.Writer) {
		fs.SetOutput(w)
		fmt.Fprintln(w, "Usage: xsync [--library <name>] [--delete] [--dry-run] [--itemize-changes] <src> <dst>")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Recursively mirror a directory tree between the local filesystem and a")
		fmt.Fprintln(w, "SharePoint folder. Exactly one of <src>/<dst> is a SharePoint URL; its")
		fmt.Fprintln(w, "position sets the direction:")
		fmt.Fprintln(w, "  xsync ./reports https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports")
		fmt.Fprintln(w, "  xsync https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports ./reports")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Only new or changed files are transferred, compared by modification time.")
		fmt.Fprintln(w, "Add --delete to remove destination items missing from the source, --dry-run")
		fmt.Fprintln(w, "to preview, and --itemize-changes to see why each file was picked. Use")
		fmt.Fprintln(w, "--ignore-times to force a transfer when an edit left the mtime untouched.")
		fmt.Fprintln(w)
		fs.PrintDefaults()
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Authentication is device-code via Microsoft Graph; refresh tokens are")
		fmt.Fprintln(w, "cached at "+filepath.Join(configDir(), "sp-token.json")+".")
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
		fmt.Println("xsync " + buildinfo.Resolve(version))
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
		// A lone URL-shaped argument is the fingerprint of an unquoted "Copy link":
		// its & characters made the shell split the command before the second
		// operand ever reached xsync.
		if len(args) == 1 && isURL(args[0]) {
			fmt.Fprintln(os.Stderr, "\nHint: a SharePoint \"Copy link\" URL contains ? and & characters that the")
			fmt.Fprintln(os.Stderr, "shell acts on unless the whole URL is wrapped in single quotes:")
			fmt.Fprintln(os.Stderr, "  xsync ./reports 'https://…/Reports?d=…&csf=1&web=1'")
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
	tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	tokenCachePath := filepath.Join(configDir(), "sp-token.json")
	client, err := spauth.NewPublicClient(tokenCachePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup error: %v\n", err)
		return 1
	}
	result, err := spauth.Authenticate(tctx, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Authentication failed: %v%s\n", err, spauth.HintForAuthError(err))
		return 1
	}
	graph := spauth.NewGraphClient(client, result.Account)
	fmt.Fprintf(os.Stderr, "Authenticated as: %s\n", result.Account.PreferredUsername)

	var localDir, url string
	if dir == upload {
		localDir, url = src, dst
	} else {
		url, localDir = src, dst
	}
	return runSync(tctx, graph, dir, localDir, url, *library, *dryRun, *doDelete, *ignoreTimes, *itemize)
}
