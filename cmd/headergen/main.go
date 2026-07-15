// headergen adds compile_commands.json entries for headers that clangd would
// otherwise infer a (possibly wrong) command for.
//
// Usage:
//
//	headergen --pair "path/to/header.h=path/to/source.cpp" build/compile_commands.json
//
// You can pass --pair multiple times. For each pair, headergen finds the
// compile command already recorded for the source file, clones it, retargets
// it at the header, and appends it as a new entry. Existing header entries
// are left untouched (idempotent).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry mirrors one record of the JSON Compilation Database format.
// See: https://clang.llvm.org/docs/JSONCompilationDatabase.html
type Entry struct {
	Directory string `json:"directory"`
	Command   string `json:"command,omitempty"`
	File      string `json:"file"`
	Output    string `json:"output,omitempty"`
}

// pairFlag implements flag.Value so we can accept --pair multiple times.
type pairFlag struct {
	pairs []pair
}

type pair struct {
	header string
	source string
}

func (p *pairFlag) String() string {
	return fmt.Sprintf("%v", p.pairs)
}

func (p *pairFlag) Set(value string) error {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid --pair %q, expected header=source", value)
	}
	p.pairs = append(p.pairs, pair{header: parts[0], source: parts[1]})
	return nil
}

func main() {
	var pairs pairFlag
	flag.Var(&pairs, "pair", "header=source mapping (repeatable)")
	watch := flag.Bool("watch", false, "watch compile_commands.json and re-patch whenever it changes (e.g. after every CMake configure)")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: headergen [--watch] [--pair header=source ...] <compile_commands.json>")
		os.Exit(2)
	}
	if len(pairs.pairs) == 0 {
		fmt.Fprintln(os.Stderr, "error: at least one --pair is required")
		os.Exit(2)
	}

	ccPath := flag.Arg(0)

	if *watch {
		watchAndPatch(ccPath, pairs.pairs)
		return
	}

	if err := run(ccPath, pairs.pairs); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// watchAndPatch polls ccPath for modification-time changes and re-runs the
// patch whenever the file is rewritten (e.g. by a fresh CMake configure).
// Because run() is idempotent (it skips headers that already have an entry),
// re-triggering after our own write is harmless: it just becomes a no-op
// "skip" pass until the next real regeneration.
func watchAndPatch(ccPath string, pairs []pair) {
	fmt.Printf("watching %s for changes (Ctrl+C to stop)...\n", ccPath)

	var lastModTime time.Time
	for {
		info, err := os.Stat(ccPath)
		if err != nil {
			// File may not exist yet (e.g. before the first configure). Keep polling.
			time.Sleep(time.Second)
			continue
		}

		if info.ModTime().After(lastModTime) {
			lastModTime = info.ModTime()
			// A CMake generate step can take a brief moment to finish writing
			// the file; give it a beat before reading, then patch.
			time.Sleep(300 * time.Millisecond)
			if err := run(ccPath, pairs); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
		}

		time.Sleep(time.Second)
	}
}

func run(ccPath string, pairs []pair) error {
	raw, err := os.ReadFile(ccPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", ccPath, err)
	}

	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("parsing %s: %w", ccPath, err)
	}

	byAbs := make(map[string]Entry)
	for _, e := range entries {
		abs, err := entryAbsPath(e)
		if err != nil {
			return fmt.Errorf("resolving entry %q: %w", e.File, err)
		}
		byAbs[normalize(abs)] = e
	}

	changed := false
	for _, p := range pairs {
		headerAbs, err := filepath.Abs(p.header)
		if err != nil {
			return fmt.Errorf("resolving header %q: %w", p.header, err)
		}
		sourceAbs, err := filepath.Abs(p.source)
		if err != nil {
			return fmt.Errorf("resolving source %q: %w", p.source, err)
		}

		headerKey := normalize(headerAbs)
		if _, ok := byAbs[headerKey]; ok {
			fmt.Printf("skip: %s already has an entry\n", p.header)
			continue
		}

		donor, ok := byAbs[normalize(sourceAbs)]
		if !ok {
			return fmt.Errorf("no compile_commands.json entry found for source %q (resolved to %q)", p.source, sourceAbs)
		}

		cmd := retarget(donor.Command, headerAbs)

		newEntry := Entry{
			Directory: donor.Directory,
			Command:   cmd,
			File:      headerAbs,
		}
		entries = append(entries, newEntry)
		byAbs[headerKey] = newEntry
		changed = true
		fmt.Printf("added: %s (donor: %s)\n", p.header, p.source)
	}

	if !changed {
		return nil
	}

	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding result: %w", err)
	}
	if err := os.WriteFile(ccPath, out, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", ccPath, err)
	}
	return nil
}

// normalize makes paths comparable regardless of slash direction or case
// (Windows paths are case-insensitive).
func normalize(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
}

// entryAbsPath resolves an entry's file to an absolute path. Per the JSON
// Compilation Database spec, "file" may be relative to "directory".
func entryAbsPath(e Entry) (string, error) {
	if filepath.IsAbs(e.File) {
		return filepath.Clean(e.File), nil
	}
	return filepath.Abs(filepath.Join(e.Directory, e.File))
}

// retarget replaces the trailing input file (after the final " -- ", if
// present) with the header path. If there's no "--" separator, it appends one.
func retarget(command, header string) string {
	if idx := strings.LastIndex(command, " -- "); idx != -1 {
		head := command[:idx]
		return fmt.Sprintf(`%s -- "%s"`, head, header)
	}
	return fmt.Sprintf(`%s -- "%s"`, command, header)
}
