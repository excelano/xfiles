// Command xftp gives SharePoint document libraries an FTP-client feel over
// Microsoft Graph: connect to a site, then an interactive prompt offers
// ls/cd/get/put/mkdir/rm/mv. Authentication is device-code; refresh tokens are
// cached under ~/.config/xftp.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/excelano/xftp/internal/drive"
	"github.com/excelano/xftp/internal/spauth"
)

func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "xftp")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".xftp"
	}
	return filepath.Join(home, ".config", "xftp")
}

// version is stamped at build time via -ldflags by goreleaser.
var version = "(devel)"

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("xftp", flag.ContinueOnError)
	library := fs.String("library", "", "Document library display name (default: inferred from the URL, else the site's default library)")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "V", false, "print version and exit (shorthand)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: xftp [--library <name>] <url>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "<url> is a SharePoint site, library, or folder URL, e.g.")
		fmt.Fprintln(os.Stderr, "  https://contoso.sharepoint.com/sites/Marketing")
		fmt.Fprintln(os.Stderr, "  https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports")
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
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: a SharePoint URL is required")
		fs.Usage()
		return 2
	}
	if len(args) > 1 {
		fmt.Fprintf(os.Stderr, "Error: unexpected extra arguments after the URL: %v\n", args[1:])
		fs.Usage()
		return 2
	}
	siteURL := args[0]

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

	d, err := drive.ResolveDrive(ctx, graph, siteURL, *library)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to bind library: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "Authenticated as: %s\n", result.Account.PreferredUsername)
	location := d.Name
	if d.StartPath != "" {
		location = fmt.Sprintf("%s/%s", d.Name, d.StartPath)
	}
	fmt.Fprintf(os.Stderr, "Connected to: %s / %s. Type \"help\" for commands, \"quit\" to exit.\n", d.Hostname, location)

	return runREPL(ctx, graph, d)
}
