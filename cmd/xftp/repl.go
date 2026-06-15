package main

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/excelano/xftp/internal/drive"
	"github.com/excelano/xftp/internal/spauth"
	"github.com/peterh/liner"
)

// session holds the mutable state of one interactive xftp run: the bound drive,
// the remote working directory (library-relative; "" is the root), and the
// local working directory used by get/put.
type session struct {
	ctx      context.Context
	g        *spauth.GraphClient
	d        *drive.Drive
	cwd      string
	localDir string
}

// runREPL drives the FTP-style command loop until quit/EOF. It returns a
// process exit code.
func runREPL(ctx context.Context, g *spauth.GraphClient, d *drive.Drive) int {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	s := &session{ctx: ctx, g: g, d: d, cwd: d.StartPath, localDir: wd}

	line := liner.NewLiner()
	defer line.Close()
	line.SetCtrlCAborts(true)

	histPath := filepath.Join(configDir(), "history")
	if f, err := os.Open(histPath); err == nil {
		line.ReadHistory(f)
		f.Close()
	}
	defer saveHistory(line, histPath)

	for {
		input, err := line.Prompt(s.prompt())
		if err == liner.ErrPromptAborted {
			continue // Ctrl-C cancels the current line
		}
		if err == io.EOF {
			fmt.Fprintln(os.Stderr)
			return 0
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "input error: %v\n", err)
			return 1
		}
		args := tokenize(input)
		if len(args) == 0 {
			continue
		}
		line.AppendHistory(input)
		if quit := s.dispatch(line, args); quit {
			return 0
		}
	}
}

func saveHistory(line *liner.State, path string) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	line.WriteHistory(f)
}

func (s *session) prompt() string {
	return fmt.Sprintf("xftp:/%s> ", s.cwd)
}

// dispatch runs one command. It returns true when the session should end.
func (s *session) dispatch(line *liner.State, args []string) bool {
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "?":
		printHelp()
	case "quit", "exit", "bye":
		return true
	case "pwd":
		fmt.Printf("/%s\n", s.cwd)
	case "cd":
		s.cd(rest)
	case "ls", "dir":
		s.ls(rest)
	case "get":
		s.get(rest)
	case "put":
		s.put(rest)
	case "mkdir":
		s.mkdir(rest)
	case "rm", "delete":
		s.rm(line, rest)
	case "mv", "rename":
		s.mv(rest)
	case "lpwd":
		fmt.Println(s.localDir)
	case "lcd":
		s.lcd(rest)
	case "lls":
		s.lls(rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q; type \"help\"\n", cmd)
	}
	return false
}

// cd with no argument reports the current remote directory (bare command
// reports state); with one, it validates the target is a folder and descends.
func (s *session) cd(rest []string) {
	if len(rest) == 0 {
		fmt.Printf("/%s\n", s.cwd)
		return
	}
	target := resolveRemote(s.cwd, rest[0])
	item, err := s.d.Stat(s.ctx, s.g, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cd: %v\n", err)
		return
	}
	if !item.IsFolder {
		fmt.Fprintf(os.Stderr, "cd: not a folder: /%s\n", target)
		return
	}
	s.cwd = target
}

func (s *session) ls(rest []string) {
	target := s.cwd
	if len(rest) > 0 {
		target = resolveRemote(s.cwd, rest[0])
	}
	items, err := s.d.List(s.ctx, s.g, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ls: %v\n", err)
		return
	}
	printItems(os.Stdout, items)
}

func (s *session) get(rest []string) {
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: get <remote> [local]")
		return
	}
	remote := resolveRemote(s.cwd, rest[0])
	localName := path.Base(remote)
	if len(rest) > 1 {
		localName = rest[1]
	}
	localPath := localName
	if !filepath.IsAbs(localPath) {
		localPath = filepath.Join(s.localDir, localPath)
	}

	ctx, stop := signal.NotifyContext(s.ctx, os.Interrupt)
	defer stop()

	item, err := s.d.Stat(ctx, s.g, remote)
	if err != nil {
		fmt.Fprintf(os.Stderr, "get: %v\n", err)
		return
	}
	if item.IsFolder {
		fmt.Fprintf(os.Stderr, "get: /%s is a folder\n", remote)
		return
	}

	// Download into a temp file in the destination directory, then rename on
	// success. An interrupted or failed download never leaves a corrupt file at
	// the real name.
	tmp, err := os.CreateTemp(filepath.Dir(localPath), "."+filepath.Base(localPath)+".part-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "get: %v\n", err)
		return
	}
	tmpName := tmp.Name()

	var w io.Writer = tmp
	showProgress := item.Size > progressThreshold
	if showProgress {
		w = &progressWriter{w: tmp, total: item.Size, label: path.Base(remote)}
	}

	dlErr := s.d.Download(ctx, s.g, remote, w)
	if showProgress {
		fmt.Fprintln(os.Stderr)
	}
	if cerr := tmp.Close(); cerr != nil && dlErr == nil {
		dlErr = cerr
	}
	if dlErr != nil {
		os.Remove(tmpName)
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "get: interrupted")
		} else {
			fmt.Fprintf(os.Stderr, "get: %v\n", dlErr)
		}
		return
	}
	if err := os.Rename(tmpName, localPath); err != nil {
		os.Remove(tmpName)
		fmt.Fprintf(os.Stderr, "get: %v\n", err)
		return
	}
	fmt.Printf("got /%s -> %s\n", remote, localPath)
}

func (s *session) put(rest []string) {
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: put <local> [remote]")
		return
	}
	localPath := rest[0]
	if !filepath.IsAbs(localPath) {
		localPath = filepath.Join(s.localDir, localPath)
	}
	f, err := os.Open(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "put: %v\n", err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		fmt.Fprintf(os.Stderr, "put: %v\n", err)
		return
	}
	if info.IsDir() {
		fmt.Fprintf(os.Stderr, "put: %s is a directory\n", localPath)
		return
	}
	remoteArg := filepath.Base(localPath)
	if len(rest) > 1 {
		remoteArg = rest[1]
	}
	remote := resolveRemote(s.cwd, remoteArg)
	ctype := mime.TypeByExtension(filepath.Ext(localPath))

	ctx, stop := signal.NotifyContext(s.ctx, os.Interrupt)
	defer stop()

	// Show progress for large uploads so a multi-chunk transfer doesn't look
	// hung; small ones finish in a single request.
	var r io.Reader = f
	showProgress := info.Size() > progressThreshold
	if showProgress {
		r = &progressReader{r: f, total: info.Size(), label: path.Base(remote)}
	}
	err = s.d.Upload(ctx, s.g, remote, ctype, r, info.Size())
	if showProgress {
		fmt.Fprintln(os.Stderr)
	}
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "put: interrupted")
		} else {
			fmt.Fprintf(os.Stderr, "put: %v\n", err)
		}
		return
	}
	fmt.Printf("put %s -> /%s (%d bytes)\n", localPath, remote, info.Size())
}

func (s *session) mkdir(rest []string) {
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mkdir <path>")
		return
	}
	target := resolveRemote(s.cwd, rest[0])
	if err := s.d.Mkdir(s.ctx, s.g, target); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		return
	}
	fmt.Printf("created /%s\n", target)
}

// rm deletes a file immediately; folder deletes are recursive, so they prompt
// for confirmation first.
func (s *session) rm(line *liner.State, rest []string) {
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rm <path>")
		return
	}
	target := resolveRemote(s.cwd, rest[0])
	item, err := s.d.Stat(s.ctx, s.g, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rm: %v\n", err)
		return
	}
	if item.IsFolder {
		msg := fmt.Sprintf("rm: /%s is a folder with %d item(s); delete recursively? [y/N]: ", target, item.ChildCount)
		if !confirm(line, msg) {
			fmt.Println("cancelled")
			return
		}
	}
	if err := s.d.Remove(s.ctx, s.g, target); err != nil {
		fmt.Fprintf(os.Stderr, "rm: %v\n", err)
		return
	}
	fmt.Printf("removed /%s\n", target)
}

func (s *session) mv(rest []string) {
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mv <src> <dst>")
		return
	}
	src := resolveRemote(s.cwd, rest[0])
	dst := resolveRemote(s.cwd, rest[1])
	if err := s.d.Move(s.ctx, s.g, src, dst); err != nil {
		fmt.Fprintf(os.Stderr, "mv: %v\n", err)
		return
	}
	fmt.Printf("moved /%s -> /%s\n", src, dst)
}

// lcd with no argument reports the local working directory; with one, it
// changes to it (after verifying it exists).
func (s *session) lcd(rest []string) {
	if len(rest) == 0 {
		fmt.Println(s.localDir)
		return
	}
	target := rest[0]
	if !filepath.IsAbs(target) {
		target = filepath.Join(s.localDir, target)
	}
	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lcd: %v\n", err)
		return
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "lcd: not a directory: %s\n", target)
		return
	}
	s.localDir = target
	fmt.Println(s.localDir)
}

func (s *session) lls(rest []string) {
	target := s.localDir
	if len(rest) > 0 {
		if filepath.IsAbs(rest[0]) {
			target = rest[0]
		} else {
			target = filepath.Join(s.localDir, rest[0])
		}
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lls: %v\n", err)
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, e := range entries {
		kind := "-"
		if e.IsDir() {
			kind = "d"
		}
		fmt.Fprintf(tw, "%s\t%s\n", kind, e.Name())
	}
	tw.Flush()
}

// confirm prompts on the active liner and returns true only for an explicit
// yes. The prompt is not added to history.
func confirm(line *liner.State, prompt string) bool {
	ans, err := line.Prompt(prompt)
	if err != nil {
		return false
	}
	ans = strings.TrimSpace(strings.ToLower(ans))
	return ans == "y" || ans == "yes"
}

// progressThreshold is the file size above which get/put print a progress line.
// Below it, transfers are quick enough that progress would just flicker.
const progressThreshold = 50 * 1024 * 1024 // 50 MiB

// progressReader wraps a reader and prints a single rewritten line of upload
// progress to stderr as bytes flow through it. The label is the remote file name.
type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	label string
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	pct := 0.0
	if p.total > 0 {
		pct = float64(p.read) / float64(p.total) * 100
	}
	fmt.Fprintf(os.Stderr, "\ruploading %s: %d/%d bytes (%.0f%%)", p.label, p.read, p.total, pct)
	return n, err
}

// progressWriter is the download counterpart of progressReader: it prints a
// rewritten progress line as bytes are written to the local file.
type progressWriter struct {
	w     io.Writer
	total int64
	wrote int64
	label string
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.wrote += int64(n)
	pct := 0.0
	if p.total > 0 {
		pct = float64(p.wrote) / float64(p.total) * 100
	}
	fmt.Fprintf(os.Stderr, "\rdownloading %s: %d/%d bytes (%.0f%%)", p.label, p.wrote, p.total, pct)
	return n, err
}

func printItems(w io.Writer, items []drive.Item) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsFolder != items[j].IsFolder {
			return items[i].IsFolder // folders first
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, it := range items {
		kind := "-"
		size := fmt.Sprintf("%d", it.Size)
		if it.IsFolder {
			kind = "d"
			size = fmt.Sprintf("%d items", it.ChildCount)
		}
		mod := ""
		if !it.LastModified.IsZero() {
			mod = it.LastModified.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", kind, size, mod, it.Name)
	}
	tw.Flush()
}

func printHelp() {
	fmt.Print(`Commands:
  ls [path]            list a remote folder (default: current)
  cd [path]            change remote folder (bare: print current)
  pwd                  print remote folder
  get <remote> [local] download a file
  put <local> [remote] upload a file (chunked above 250MB)
  mkdir <path>         create a remote folder
  rm <path>            delete a file (folders prompt to confirm)
  mv <src> <dst>       move or rename a remote item
  lcd [dir]            change local folder (bare: print current)
  lpwd                 print local folder
  lls [dir]            list a local folder
  help                 this list
  quit                 exit

Paths may be relative to the current folder or absolute (leading /).
Quote names with spaces: get "Q1 Plan.xlsx"
`)
}

// resolveRemote turns a user-supplied path (relative or absolute) into a
// normalized library-relative path. "" is the drive root. It resolves "."  and
// ".." segments via path.Clean.
func resolveRemote(cwd, arg string) string {
	if arg == "" {
		return cwd
	}
	joined := arg
	if !strings.HasPrefix(arg, "/") {
		joined = "/" + cwd + "/" + arg
	}
	return strings.Trim(path.Clean(joined), "/")
}

// tokenize splits a command line on whitespace, honoring double quotes so file
// names with spaces survive as single arguments.
func tokenize(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			args = append(args, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case (c == ' ' || c == '\t') && !inQuote:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return args
}
