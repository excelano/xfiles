// Command xtree is the tree(1) of the xftp family: it walks a SharePoint
// document library recursively and prints it as an indented tree, with the
// ├──/└── guides tree users expect, then a "N directories, M files" summary.
// Where xfind gives flat, pipeable paths, xtree gives a shape you can read at a
// glance. Point it at a site, library, or folder URL.
// Authentication is device-code; refresh tokens are cached under ~/.config/xtree.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/excelano/xfiles/internal/drive"
	"github.com/excelano/xfiles/internal/spauth"
)

func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "xtree")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".xtree"
	}
	return filepath.Join(home, ".config", "xtree")
}

// version is stamped at build time via -ldflags by goreleaser.
var version = "(devel)"

// treePrefix builds the indentation that precedes a node's connector, given the
// "is last child" flag of each ancestor from depth 1 down to the node's parent.
// An ancestor that was the last of its siblings leaves blank space below it; one
// with siblings still to come leaves a vertical guide.
func treePrefix(ancestorLast []bool) string {
	var b strings.Builder
	for _, last := range ancestorLast {
		if last {
			b.WriteString("    ")
		} else {
			b.WriteString("│   ")
		}
	}
	return b.String()
}

// connector is the branch glyph for a node: └── when it is the last child of
// its parent, ├── otherwise.
func connector(isLast bool) string {
	if isLast {
		return "└── "
	}
	return "├── "
}

// countDirs and countFiles render the summary tallies with correct pluralization
// ("1 directory", "2 directories", "1 file", "0 files").
func countDirs(n int) string {
	if n == 1 {
		return "1 directory"
	}
	return fmt.Sprintf("%d directories", n)
}

func countFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("xtree", flag.ContinueOnError)
	library := fs.String("library", "", "Document library display name (default: inferred from the URL, else the site's default library)")
	level := fs.Int("L", 0, "Descend at most this many folder levels (0 = unlimited)")
	dirsOnly := fs.Bool("d", false, "List folders only")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "V", false, "print version and exit (shorthand)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: xtree [flags] <url>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Print a SharePoint library as an indented tree:")
		fmt.Fprintln(os.Stderr, "  xtree https://contoso.sharepoint.com/sites/Marketing")
		fmt.Fprintln(os.Stderr, "  xtree -L 2 -d https://contoso.sharepoint.com/sites/Marketing/Shared%20Documents")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Wrap a SharePoint \"Copy link\" URL in single quotes; its ? and & characters")
		fmt.Fprintln(os.Stderr, "would otherwise be acted on by the shell before xtree sees them.")
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
	if *level < 0 {
		fmt.Fprintln(os.Stderr, "Error: -L cannot be negative")
		return 2
	}

	args := fs.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Error: exactly one SharePoint URL is required")
		fs.Usage()
		return 2
	}
	url := args[0]

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
	out := bufio.NewWriter(os.Stdout)
	// The header is the location the tree is rooted at: the folder the URL
	// pointed into, or the library name when that's the whole drive.
	rootLabel := root
	if rootLabel == "" {
		rootLabel = d.Name
	}
	fmt.Fprintln(out, rootLabel)

	var dirs, files int
	var ancestorLast []bool
	walkErr := d.Walk(tctx, g, root, *dirsOnly, func(it drive.Item, _ string, depth int, isLast bool) bool {
		fmt.Fprintf(out, "%s%s%s\n", treePrefix(ancestorLast[:depth-1]), connector(isLast), it.Name)
		if it.IsFolder {
			dirs++
		} else {
			files++
		}
		// Record this node's last-ness at its depth so its children can draw the
		// correct guide above themselves.
		ancestorLast = append(ancestorLast[:depth-1], isLast)
		return it.IsFolder && (*level == 0 || depth < *level)
	})
	out.Flush()
	if walkErr != nil {
		fmt.Fprintf(os.Stderr, "tree failed: %v\n", walkErr)
		return 1
	}

	if *dirsOnly {
		fmt.Fprintf(out, "\n%s\n", countDirs(dirs))
	} else {
		fmt.Fprintf(out, "\n%s, %s\n", countDirs(dirs), countFiles(files))
	}
	out.Flush()
	return 0
}
