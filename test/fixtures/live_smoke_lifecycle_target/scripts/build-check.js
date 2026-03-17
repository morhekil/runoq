const { execFileSync } = require("node:child_process");
const { existsSync } = require("node:fs");
const { join } = require("node:path");

const files = [
  join(__dirname, "..", "src", "progress.js"),
  join(__dirname, "..", "src", "cli.js"),
].filter(existsSync);

for (const file of files) {
  execFileSync(process.execPath, ["--check", file], { stdio: "inherit" });
}

process.stdout.write(`Checked ${files.length} file(s).\n`);
