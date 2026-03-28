# Progress tracking library

Build a progress tracking library for Node.js that formats completion status in
multiple styles and exposes a CLI wrapper.

## Requirements

### Core formatter

Add a `formatProgress(complete, total)` function to `src/progress.js` that
returns a human-readable string like `"2/5 complete (40%)"`. The function must
use only built-in Node.js modules and export via CommonJS. Tests go in
`test/progress.test.js`.

### Overflow clamping

Progress values where `complete > total` must be clamped so they never exceed
100%. For example, `percent(7, 5)` returns `100` and `formatProgress(7, 5)`
returns `"5/5 complete (100%)"`. This depends on the core formatter being in
place first.

### CLI wrapper

After the formatter is stable, expose it through `src/cli.js` — a tiny script
that takes two integer arguments and prints the formatted progress to stdout.
Add tests for the CLI success path. Keep `npm test` and `npm run build` green.
This depends on overflow clamping being done first.

## Constraints

- All modules must be CommonJS (no ESM).
- No external dependencies — only Node.js built-ins.
- `npm test` and `npm run build` must pass at every stage.
