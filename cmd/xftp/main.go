// Command xftp gives SharePoint document libraries an FTP-client feel over
// Microsoft Graph: connect to a site, then an interactive prompt offers
// ls/cd/get/put/mkdir/rm/mv. Authentication is device-code; the refresh token
// is cached at ~/.config/excelano/sp-token.json, shared with xql and the other
// xfiles tools.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/excelano/spauth"
	"github.com/excelano/xfiles"
	"github.com/excelano/xfiles/internal/buildinfo"
	"github.com/excelano/xfiles/internal/cli"
	"github.com/excelano/xfiles/internal/drive"
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

// legacyTokenCache is the per-tool cache xftp kept before the family shared
// one; adopted on first run so nobody signs in again.
func legacyTokenCache() string {
	return filepath.Join(configDir(), "sp-token.json")
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
	installSkill := fs.Bool("install-skill", false, "install the xfiles Claude Code skill and exit")
	uninstallSkill := fs.Bool("uninstall-skill", false, "remove the installed Claude Code skill and exit")
	usage := func(w io.Writer) {
		fs.SetOutput(w)
		fmt.Fprintln(w, "Usage: xftp [--library <name>] <url>")
		fmt.Fprintln(w, "       xftp auth [--json]")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "<url> is a SharePoint site, library, or folder URL, e.g.")
		fmt.Fprintln(w, "  https://contoso.sharepoint.com/sites/Marketing")
		fmt.Fprintln(w, "  https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents/Reports")
		fmt.Fprintln(w)
		fs.PrintDefaults()
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Authentication is device-code via Microsoft Graph; refresh tokens are")
		fmt.Fprintln(w, "cached at "+spauth.CachePath()+", one session shared with xql and the other xfiles tools.")
		fmt.Fprintln(w, "`xftp auth` reports that session without starting a sign-in.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, cli.ExitCodes)
	}
	fs.Usage = func() { usage(os.Stderr) }
	args := os.Args[1:]
	// The bare state command binds no library and needs no URL, so it is
	// answered before the main flag set sees the line.
	if len(args) > 0 && args[0] == "auth" {
		return spauth.AuthCommand(context.Background(), "xftp", legacyTokenCache(), args[1:], os.Stdout, os.Stderr)
	}
	if cli.HelpRequested(args, fs) {
		usage(os.Stdout)
		return 0
	}
	if err := fs.Parse(cli.Reorder(args, fs)); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println("xftp " + buildinfo.Resolve(version))
		return 0
	}
	if *installSkill {
		return xfiles.InstallSkill(buildinfo.Resolve(version))
	}
	if *uninstallSkill {
		return xfiles.UninstallSkill()
	}
	args = fs.Args()
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
