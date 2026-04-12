#!/usr/bin/env node

const { spawn } = require("child_process");
const path = require("path");
const fs = require("fs");

const isWindows = process.platform === "win32";
const binName = isWindows ? "ccg-binary.exe" : "ccg-binary";
const binPath = path.join(__dirname, binName);

if (!fs.existsSync(binPath)) {
  console.error("Error: ccg binary not found. Please try re-installing the package.");
  process.exit(1);
}

const child = spawn(binPath, process.argv.slice(2), {
  stdio: "inherit",
  shell: false,
});

child.on("error", (err) => {
  console.error(`Failed to start ccg binary: ${err.message}`);
  process.exit(1);
});

child.on("exit", (code) => {
  process.exit(code || 0);
});
