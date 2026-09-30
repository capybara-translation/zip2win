# zip2win

A CLI that creates ZIP archives that extract cleanly with Windows' built-in
tools (Explorer), and checks existing ZIPs for the problems that break them.
Runs on macOS, Linux, and Windows.

- **File names survive extraction.** Non-ASCII names (Japanese, accented
  letters, and so on) are stored as UTF-8 with the flag that tells Windows so,
  and macOS's decomposed (NFD) names are normalized to NFC. No mojibake, and
  no accents or dakuten split off from their letters.
- **No macOS clutter.** `.DS_Store`, `__MACOSX/`, and `._*` files are left out.
- **Safe to run.** Symbolic links aren't followed, an existing file is never
  overwritten without `--force`, and a failed run never leaves a half-written
  ZIP in place of the output.
- **`inspect` checks any ZIP**, including ones made by other tools: a missing
  UTF-8 flag, names usable for path traversal, duplicate entries, macOS
  metadata, and names disguised with bidirectional control characters.

## Install

### Homebrew (macOS / Linux)

```bash
brew install --cask capybara-translation/tap/zip2win
```

### go install

```bash
go install github.com/capybara-translation/zip2win/cmd/zip2win@latest
```

### Pre-built binaries

Download the archive for your platform from the
[Releases](https://github.com/capybara-translation/zip2win/releases) page.
Verify it against `checksums.txt` from the same release.

## Usage

```bash
# Archive a directory (or file) into a ZIP. The contents of docs/ are stored as docs/...
zip2win create docs docs.zip

# Without an output path, the ZIP is written next to the source as <source>.zip
# (docs -> docs.zip, memo.txt -> memo.txt.zip)
zip2win create docs

# Overwrite an existing output file
zip2win create --force docs docs.zip

# Inspect a ZIP (exit 0 if every entry is OK, exit 1 if there's a problem)
zip2win inspect docs.zip

# Print the version ("dev" for a build from an untagged or modified tree)
zip2win version
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
| Names containing a Unicode bidirectional control character (e.g. U+202E) | A warning is printed and the run continues; `inspect` reports it as a problem (such a name can display as something else, e.g. `invoice_\u202efdp.exe` as `invoice_exe.pdf`) |
| Destination already exists | Error, unless `--force` is given to overwrite it (a directory destination is always an error, even with `--force`) |
| Writing | Written to a temp file next to the output, named `<output>.<random>.tmp`, and renamed into place once complete, so a failure partway through never leaves a broken ZIP behind. The random name can't collide with your own files, and a leftover from an interrupted run (e.g. Ctrl-C) never blocks a later run — it is never deleted automatically either, so remove it by hand. The file is created with mode 0666 minus your umask, like any other file you create |
| Output ZIP inside the input directory | Never includes itself |

Windows reserved names (like `CON`) and characters that are invalid on
Windows (`: * ? " < > |`) are deliberately not checked or rewritten, since the
extraction target isn't necessarily Windows.

## What `inspect` reports

- The EFS flag not set, or a name that isn't valid UTF-8
- A name that isn't NFC-normalized (still in NFD)
- A path containing `..`, an absolute path, or a backslash-separated path (material for path traversal on the extracting side)
- A Unicode bidirectional control character in a name (it can make the name display as something else)
- macOS metadata files mixed into the archive
- Duplicate entry names (including pairs that only become identical after NFC normalization)

When displayed, control characters and bidirectional control characters in entry names are escaped (to prevent terminal escape-sequence injection and names that pass for something else).

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
