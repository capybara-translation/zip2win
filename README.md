# zip2win

A CLI that creates ZIP archives whose Japanese file names don't turn into
mojibake when extracted with Windows' built-in tools (Explorer).
Runs on macOS, Linux, and Windows.

## Install

```bash
go install github.com/capybara-translation/zip2win/cmd/zip2win@latest
```

## Usage

```bash
# Archive a directory (or file) into a ZIP. The contents of docs/ are stored as docs/...
zip2win create docs docs.zip

# Overwrite an existing output file
zip2win create --force docs docs.zip

# Inspect a ZIP (exit 0 if every entry is OK, exit 1 if there's a problem)
zip2win inspect docs.zip
```

Flags must come before positional arguments.

## What it does

| Item | Policy |
|---|---|
| File names | Stored as UTF-8, with the EFS flag (General Purpose Bit Flag bit 11) explicitly set on every entry |
| Unicode normalization | NFC (converts macOS-originated NFD names) |
| Path separator | `/` |
| Exclusions | `.DS_Store`, `._*`, `__MACOSX/`, symbolic links (a notice is written to stderr when one is skipped; an error if `<source>` itself is a symlink) |
| Non-regular files (FIFOs, sockets, device files) | Notified on stderr and skipped when found during the walk; an error if `<source>` itself is one (it can't be stored in a ZIP, and a FIFO blocks when opened) |
| `<source>` itself is an excluded name | Error (`.DS_Store`, `._*`, `__MACOSX`; this avoids silently producing an empty or partly-missing archive) |
| Name collision after NFC normalization | Error (never produces a ZIP with more than one entry at the same path) |
| Names containing a backslash | A warning is printed and the run continues; `inspect` reports it as a problem |
| Destination already exists | Error, unless `--force` is given to overwrite it (a directory destination is always an error, even with `--force`) |
| Writing | Written to a temp file `<output>.tmp` (e.g. `docs.zip` -> `docs.zip.tmp`) and renamed into place once complete, so a failure partway through never leaves a broken ZIP behind. A leftover `.tmp` file is an error; with `--force` it's removed with a notice (an error if it's the same file as `<source>`) |
| Output ZIP inside the input directory | Never includes itself |

Windows reserved names (like `CON`) and characters that are invalid on
Windows (`: * ? " < > |`) are deliberately not checked or rewritten, since the
extraction target isn't necessarily Windows.

## What `inspect` reports

- The EFS flag not set, or a name that isn't valid UTF-8
- A name that isn't NFC-normalized (still in NFD)
- A path containing `..`, an absolute path, or a backslash-separated path (material for path traversal on the extracting side)
- macOS metadata files mixed into the archive
- Duplicate entry names (including pairs that only become identical after NFC normalization)

When displayed, control characters in entry names are escaped (to prevent terminal escape-sequence injection).

## Verifying on a real Windows machine

Even with EFS set correctly, extraction can still fail because of path
length, permissions, or security software. Always extract for real on a
Windows environment equivalent to the target before delivery.

1. Build for Windows
   ```bash
   GOOS=windows GOARCH=amd64 go build -o zip2win.exe ./cmd/zip2win
   ```
2. Prepare a test folder (include ASCII names, Japanese names, names with spaces, an empty folder, and a deep hierarchy)
3. Create the ZIP with `zip2win.exe create <folder> test.zip`
4. Confirm `zip2win.exe inspect test.zip` exits 0
5. Right-click `test.zip` in Explorer and choose "Extract All"
6. Confirm the extracted file names aren't garbled and that empty folders were reproduced

## Known limitations

- There's a theoretical TOCTOU window between checking that the destination doesn't exist and the rename
- 100% compatibility with every Windows environment and older ZIP software is not guaranteed
- The NFC-collision tests are skipped on macOS/APFS, since that file system merges NFC/NFD names before zip2win ever sees the difference

## License

MIT. See [LICENSE](LICENSE).
