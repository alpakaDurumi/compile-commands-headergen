# compile-commands-headergen

A tiny tool that appends `compile_commands.json` entries for headers, pointing them at the compile command of a source file you specify.

## Why

`clangd` parses headers using a compile command it infers from some source file that (it guesses) includes that header.

When it guesses wrong — for example, borrowing the command from an unrelated target — the header can end up parsed with the wrong include paths or macros, and in some cases (shared include guards between physically different files) its entire body gets rendered inactive.

This is a known, hard-to-fix limitation of clangd (see [clangd/clangd#123](https://github.com/clangd/clangd/issues/123) and [clangd/clangd#2680](https://github.com/clangd/clangd/issues/2680)). Instead of guessing, you tell it explicitly which source file's compile command a given header should use.

## Install

Download a prebuilt binary from the [Releases](../../releases) page, or build from source (requires [Go](https://go.dev/dl/) 1.21+):

```bash
go build -o headergen .
```

## Usage

```bash
headergen --pair "path/to/header.h=path/to/source.cpp" build/compile_commands.json
```

- `--pair` can be repeated to patch multiple headers
- Already-patched headers are left untouched (safe to re-run)
- Paths can be relative or absolute; matching is done by normalized suffix (case-insensitive, slash-insensitive)

### Watch mode

CMake regenerates `compile_commands.json` on every configure, which would overwrite a manually-added entry. Since CMake itself has no hook to post-process the file after generation, `headergen` can instead watch the file and re-patch it automatically whenever it changes:

```bash
headergen --watch --pair "path/to/header.h=path/to/source.cpp" build/compile_commands.json
```

Leave this running in a terminal; every time your build system regenerates `compile_commands.json` (e.g. via a CMake configure), `headergen` detects the change and re-applies the patch within about a second. Safe to start before the file even exists.

## How it works

For each `--pair header=source`:

1. Find the existing `compile_commands.json` entry for `source`.
2. Clone its `command` and `directory` as-is.
3. Replace the trailing input file with `header`.
4. Append the new entry (skipped if `header` already has one).
