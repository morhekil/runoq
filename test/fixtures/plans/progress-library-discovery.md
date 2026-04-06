# Progress tracking library with discovery checkpoint

Build a progress tracking library for Node.js that formats completion status in
multiple styles and exposes a CLI wrapper, but pause midway to determine whether
caching is actually needed before committing to that extra surface area.

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

### Caching feasibility discovery

Before adding a cache, determine whether caching is needed at all. Benchmark the
formatter with representative inputs, document whether repeated formatting is a
real bottleneck, and capture the uncertainty in a short findings note. This is a
discovery milestone rather than guaranteed implementation work.

### CLI wrapper

After the formatter is stable and the caching discovery is complete, expose the
formatter through `src/cli.js` as a tiny script that takes two integer
arguments and prints the formatted progress to stdout. Add tests for the CLI
success path. Keep `npm test` and `npm run build` green.

## Constraints

- All modules must be CommonJS (no ESM).
- No external dependencies — only Node.js built-ins.
- `npm test` and `npm run build` must pass at every stage.
- Discovery findings must be written down before any caching implementation is approved.
