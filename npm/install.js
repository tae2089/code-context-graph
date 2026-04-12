#!/usr/bin/env node

const { execSync } = require("child_process");
const fs = require("fs");
const path = require("path");
const https = require("https");
const http = require("http");

const pkg = require("./package.json");
const version = pkg.version;
const repo = "tae2089/code-context-graph";

const PLATFORMS = {
  "darwin-arm64": "ccg-darwin-arm64",
  "darwin-x64": "ccg-darwin-amd64",
  "linux-x64": "ccg-linux-amd64",
  "linux-arm64": "ccg-linux-arm64",
  "win32-x64": "ccg-windows-amd64",
};

function getPlatformKey() {
  const platform = process.platform;
  const arch = process.arch;
  return `${platform}-${arch}`;
}

function getBinaryName() {
  const key = getPlatformKey();
  const name = PLATFORMS[key];
  if (!name) {
    console.error(`Unsupported platform: ${key}`);
    console.error(`Supported: ${Object.keys(PLATFORMS).join(", ")}`);
    process.exit(1);
  }
  return name;
}

function getDownloadUrl(binaryName) {
  const isWindows = process.platform === "win32";
  const ext = isWindows ? ".zip" : ".tar.gz";
  return `https://github.com/${repo}/releases/download/v${version}/${binaryName}${ext}`;
}

function download(url) {
  return new Promise((resolve, reject) => {
    const follow = (url) => {
      const client = url.startsWith("https") ? https : http;
      client.get(url, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          follow(res.headers.location);
          return;
        }
        if (res.statusCode !== 200) {
          reject(new Error(`HTTP ${res.statusCode} for ${url}`));
          return;
        }
        const chunks = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => resolve(Buffer.concat(chunks)));
        res.on("error", reject);
      }).on("error", reject);
    };
    follow(url);
  });
}

async function install() {
  const binaryName = getBinaryName();
  const url = getDownloadUrl(binaryName);
  const binDir = path.join(__dirname, "bin");
  const isWindows = process.platform === "win32";
  const binPath = path.join(binDir, isWindows ? "ccg.exe" : "ccg");

  // Skip if binary already exists
  if (fs.existsSync(binPath)) {
    console.log(`ccg already installed at ${binPath}`);
    return;
  }

  console.log(`Downloading ccg v${version} for ${getPlatformKey()}...`);
  console.log(`  ${url}`);

  try {
    const data = await download(url);
    fs.mkdirSync(binDir, { recursive: true });

    if (isWindows) {
      // Write zip and extract
      const zipPath = path.join(binDir, "ccg.zip");
      fs.writeFileSync(zipPath, data);
      execSync(`powershell -Command "Expand-Archive -Path '${zipPath}' -DestinationPath '${binDir}' -Force"`, { stdio: "ignore" });
      fs.unlinkSync(zipPath);
    } else {
      // Write tar.gz and extract
      const tarPath = path.join(binDir, "ccg.tar.gz");
      fs.writeFileSync(tarPath, data);
      execSync(`tar xzf "${tarPath}" -C "${binDir}"`, { stdio: "ignore" });
      fs.unlinkSync(tarPath);

      // Rename platform-specific binary to 'ccg'
      const extracted = path.join(binDir, binaryName);
      if (fs.existsSync(extracted) && extracted !== binPath) {
        fs.renameSync(extracted, binPath);
      }
      fs.chmodSync(binPath, 0o755);
    }

    console.log(`ccg v${version} installed successfully.`);
  } catch (err) {
    console.error(`Failed to install ccg: ${err.message}`);
    console.error(`You can build manually: CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/`);
    process.exit(1);
  }
}

install();
