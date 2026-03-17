const test = require("node:test");
const assert = require("node:assert/strict");

const { percent } = require("../src/progress");

test("percent returns rounded progress", () => {
  assert.equal(percent(2, 5), 40);
});

test("percent returns zero for an empty total", () => {
  assert.equal(percent(0, 0), 0);
});
