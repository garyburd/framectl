# framectl

Manage art on a Samsung **The Frame** TV from the command line: discover, pair,
upload, list, mirror, and control the art-mode slideshow.

## Install

```sh
go install github.com/garyburd/framectl@latest
```

## Quick start

Pair once (accept the TV prompt), then use the saved data file:

```sh
framectl ~/tv.json pair -name "The Frame 50"
framectl ~/tv.json sync ~/Pictures/frame          # mirror a folder
```

The data file precedes the command: `framectl [global flags] <tv.json> <command>
[command flags] [args]`. (`help` and `discover` need no data file.) `sync`
mirrors local files without interrupting someone watching the TV; other
commands work by content ID. The command-first names `discover`, `help`, and
`prepare` are reserved and cannot be used as the data-file argument.

Commands can wake a sleeping Frame using the MAC saved during pairing. Enable
Wake-on-LAN / "Power On with Mobile" on the TV, or pass `-no-wake` to fail fast.

## Commands

See **[COMMANDS.txt](COMMANDS.txt)** for the full generated reference:

```sh
framectl help
```

## How photos are presented

Photos are fitted without cropping to 3840×2160. Non-16:9 images get automatic
letter- or pillar-box fills based on their content.
