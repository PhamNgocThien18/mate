# Installing mate

mate is one Go binary, `mate`. Install it with `go install`, or build it from a
clone of the source.

## Requirements

- Go 1.25 or later, to install or build mate.
- Herdr, which runs the agents' terminal sessions. Put `herdr` on `PATH`.
  The console also looks for it in `$XDG_BIN_HOME`, `~/.local/bin`,
  `/opt/homebrew/bin` and `/usr/local/bin`, but other `mate` commands do not.
- git.
- gh, the GitHub CLI, logged in (`gh auth login`), for pull requests.
- At least one harness CLI, such as `claude` (Claude Code) or `codex`, logged
  in on this machine.

## Install with go install

```sh
go install github.com/nguyenngocanh94/mate/cmd/mate@latest
```

`@latest` is the newest commit on `main`; the repository has no release tags
yet. Pin one commit of `main` with `@<commit>` instead, a full or short hash:

```sh
go install github.com/nguyenngocanh94/mate/cmd/mate@<commit>
```

Commits from before `go install` was supported (CON-183) fail: their `go.mod`
has a `replace` directive, which `go install` refuses.

The module path still names the repository's old owner. GitHub redirects it to
`github.com/ericsnguyen9941/mate`, and `proxy.golang.org` serves the module
under the old path, so the command above works as written.

`go install` puts `mate` in `$(go env GOBIN)`, or in `$(go env GOPATH)/bin`
(`~/go/bin` by default) when `GOBIN` is not set. That directory must be on
`PATH`.

## Build from source

```sh
git clone https://github.com/ericsnguyen9941/mate.git
cd mate
make build
```

`make build` writes `bin/mate`. Copy it to a directory on `PATH`, for example
`/usr/local/bin`. `make check` runs gofmt, `go vet` and the tests.

## Check the install

```sh
mate --version
```

prints `mate <version> (<commit>, <build date>)`.

- A `make build` binary prints `git describe` of the checkout, its short
  commit and the time of the build.
- A `go install` binary prints the module version, a pseudo-version such as
  `v0.0.0-20261105080000-0123456789ab` that holds the commit's time and hash.
  Its commit and build date are `unknown`: Go records them only when it builds
  inside a git checkout.
- A plain `go build` in a checkout prints a pseudo-version too, ending in
  `+dirty` when the checkout has uncommitted changes, with the full commit
  hash. Its build date is the commit's time, not the time of the build.

## macOS

- The console lays the agents out in panes of the terminal it runs in when that
  terminal is WezTerm or Ghostty 1.3+. In any other terminal the agents run
  headless and you follow them in Office and City in the browser.
- Native notifications need Xcode Command Line Tools (`xcode-select --install`).
  See [notifications.md](notifications.md).
- The console shows the Office and City addresses on its status line. The
  commands that open a browser, `mate dashboard --open` and plugin links in
  the console, use `open`.

## Linux

- The console lays the agents out in panes only inside WezTerm. Ghostty on
  Linux has no driver; there, and in any other terminal, the agents run
  headless and you follow them in Office and City in the browser.
- The console shows the Office and City addresses on its status line. The
  commands that open a browser, `mate dashboard --open` and plugin links in
  the console, use `xdg-open` (package `xdg-utils`). Without it, open the
  address yourself.
- Notifications need `notify-send` (package `libnotify-bin` on Debian and
  Ubuntu, `libnotify` on Fedora and Arch). See
  [notifications.md](notifications.md).

## Windows (WSL2)

mate runs on Windows only through WSL2; there is no native Windows build.

- Install and run mate inside the WSL2 distribution, as on Linux above: Go,
  Herdr, git, gh and the harness CLIs are all installed inside WSL.
- Keep the workspace and the repositories on the WSL file system, for example
  under `~`, not under `/mnt/c`. git is slow on `/mnt/c`, and file locks there
  may not behave as they do on Linux.
- Open Office and City in a Windows browser at `http://localhost:7777`. WSL2
  forwards `localhost` to Windows.
- For mate to open the browser itself (`mate dashboard --open`, plugin
  links), install `xdg-utils` and point the `BROWSER` variable at a command
  that opens the Windows browser, for example `wslview` from the `wslu`
  package:

  ```sh
  sudo apt install xdg-utils wslu
  echo 'export BROWSER=wslview' >> ~/.bashrc
  ```
