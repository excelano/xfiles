// Command xfind is the find(1) of the xftp family: it walks a SharePoint
// document library recursively and prints the paths it finds, optionally
// filtered by name, type, or depth. It is the read-only, recursive cousin of
// xftp's "ls" — point it at a site, library, or folder URL and it lists
// everything beneath, one library-relative path per line, ready to pipe.
// Authentication is device-code; the refresh token is cached at
// ~/.config/excelano/sp-token.json, shared with xql and the other xfiles tools.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"

	"github.com/excelano/spauth"
	"github.com/excelano/xfiles"
	"github.com/excelano/xfiles/internal/buildinfo"
	"github.com/excelano/xfiles/internal/cli"
	"github.com/excelano/xfiles/internal/drive"
)

func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "xfind")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".xfind"
	}
	return filepath.Join(home, ".config", "xfind")
}

// legacyTokenCache is the per-tool cache xfind kept before the family shared
// one; adopted on first run so nobody signs in again.
func legacyTokenCache() string {
	return filepath.Join(configDir(), "sp-token.json")
}

// version is stamped at build time via -ldflags by goreleaser.
var version = "(devel)"

// criteria is the set of tests an item must pass to be printed. A zero criteria
// matches everything (then xfind just lists the whole tree).
type criteria struct {
	namePat string // glob matched against the basename; "" means no name test
	iname   bool   // case-insensitive name match
	typ     string // "", "f" (files only), or "d" (folders only)
}

// match reports whether an item satisfies the criteria.
func (c criteria) match(it drive.Item) (bool, error) {
	switch c.typ {
	case "f":
		if it.IsFolder {
			return false, nil
		}
	case "d":
		if !it.IsFolder {
			return false, nil
		}
	}
	if c.namePat != "" {
		name, pat := it.Name, c.namePat
		if c.iname {
			name, pat = strings.ToLower(name), strings.ToLower(pat)
		}
		ok, err := path.Match(pat, name)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// relTo strips the walk root from a library-relative item path so output is
// relative to the folder the URL pointed at, the way find prints paths relative
// to its starting directory. A root of "" (the library root) is a no-op.
func relTo(root, full string) string {
	if root == "" {
		return full
	}
	return strings.TrimPrefix(strings.TrimPrefix(full, root), "/")
}

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("xfind", flag.ContinueOnError)
	library := fs.String("library", "", "Document library display name (default: inferred from the URL, else the site's default library)")
	name := fs.String("name", "", "Print only items whose name matches this glob (e.g. '*.xlsx')")
	iname := fs.String("iname", "", "Like --name, but case-insensitive")
	typ := fs.String("type", "", "Print only files (f) or only folders (d)")
	maxDepth := fs.Int("maxdepth", 0, "Descend at most this many folder levels (0 = unlimited)")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "V", false, "print version and exit (shorthand)")
	installSkill := fs.Bool("install-skill", false, "install the xfiles Claude Code skill and exit")
	uninstallSkill := fs.Bool("uninstall-skill", false, "remove the installed Claude Code skill and exit")
	usage := func(w io.Writer) {
		fs.SetOutput(w)
		fmt.Fprintln(w, "Usage: xfind [flags] <url>")
		fmt.Fprintln(w, "       xfind auth [--json]")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Recursively list a SharePoint library, printing each match on its own line")
		fmt.Fprintln(w, "relative to the folder the URL points at:")
		fmt.Fprintln(w, "  xfind https://contoso.sharepoint.com/sites/Marketing")
		fmt.Fprintln(w, "  xfind --name '*.xlsx' --type f https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Wrap a SharePoint \"Copy link\" URL in single quotes; its ? and & characters")
		fmt.Fprintln(w, "would otherwise be acted on by the shell before xfind sees them.")
		fmt.Fprintln(w)
		fs.PrintDefaults()
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Authentication is device-code via Microsoft Graph; refresh tokens are")
		fmt.Fprintln(w, "cached at "+spauth.CachePath()+", one session shared with xql and the other xfiles tools.")
		fmt.Fprintln(w, "`xfind auth` reports that session without starting a sign-in.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, cli.ExitCodes)
	}
	fs.Usage = func() { usage(os.Stderr) }
	args := os.Args[1:]
	// The bare state command binds no library and needs no URL, so it is
	// answered before the main flag set sees the line.
	if len(args) > 0 && args[0] == "auth" {
		return spauth.AuthCommand(context.Background(), "xfind", legacyTokenCache(), args[1:], os.Stdout, os.Stderr)
	}
	if cli.HelpRequested(args, fs) {
		usage(os.Stdout)
		return 0
	}
	if err := fs.Parse(cli.Reorder(args, fs)); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println("xfind " + buildinfo.Resolve(version))
		return 0
	}
	if *installSkill {
		return xfiles.InstallSkill(buildinfo.Resolve(version))
	}
	if *uninstallSkill {
		return xfiles.UninstallSkill()
	}

	c := criteria{namePat: *name, iname: false, typ: *typ}
	if *iname != "" {
		c.namePat, c.iname = *iname, true
	}
	if *name != "" && *iname != "" {
		fmt.Fprintln(os.Stderr, "Error: use either --name or --iname, not both")
		return 2
	}
	if c.typ != "" && c.typ != "f" && c.typ != "d" {
		fmt.Fprintf(os.Stderr, "Error: --type must be f (files) or d (folders), got %q\n", c.typ)
		return 2
	}
	if c.namePat != "" {
		if _, err := path.Match(c.namePat, ""); err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid name pattern %q: %v\n", c.namePat, err)
			return 2
		}
	}
	if *maxDepth < 0 {
		fmt.Fprintln(os.Stderr, "Error: --maxdepth cannot be negative")
		return 2
	}

	args = fs.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Error: exactly one SharePoint URL is required")
		fs.Usage()
		return 2
	}
	url := args[0]

	ctx := context.Background()

	client, err := spauth.NewPublicClient(spauth.CachePath(), legacyTokenCache())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup error: %v\n", err)
		return 1
	}
	result, err := spauth.Authenticate(ctx, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Authentication failed: %v%s\n", err, spauth.HintForAuthError(err))
		return 1
	}
	g := spauth.NewGraphClient(client, result.Account)
	fmt.Fprintf(os.Stderr, "Authenticated as: %s\n", result.Account.PreferredUsername)

	d, err := drive.ResolveDrive(ctx, g, url, *library)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind library: %v\n", err)
		return 1
	}

	tctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	root := d.StartPath
	var matchErr error
	out := bufio.NewWriter(os.Stdout)
	// With -type d we never emit files, so skip listing them entirely.
	walkErr := d.Walk(tctx, g, root, c.typ == "d", func(it drive.Item, p string, depth int, _ bool) bool {
		if ok, err := c.match(it); err != nil {
			matchErr = err
		} else if ok {
			fmt.Fprintln(out, relTo(root, p))
		}
		// Descend into folders unless a depth limit says to stop here.
		return it.IsFolder && (*maxDepth == 0 || depth < *maxDepth)
	})
	out.Flush()
	if matchErr != nil {
		fmt.Fprintf(os.Stderr, "find failed: %v\n", matchErr)
		return 1
	}
	if walkErr != nil {
		fmt.Fprintf(os.Stderr, "find failed: %v\n", walkErr)
		return 1
	}
	return 0
}
