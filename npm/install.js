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
  "win32-x64": "ccg-windows-amd64", // This is the zip name, the file inside is .exe
};

// @intent identify the current OS and CPU architecture for picking the matching ccg release asset.
function getPlatformKey() {
  const platform = process.platform;
  const arch = process.arch;
  return `${platform}-${arch}`;
}

// @intent map the current platform key to the published ccg release asset name and abort if unsupported.
function getAssetName() {
  const key = getPlatformKey();
  const name = PLATFORMS[key];
  if (!name) {
    console.error(`Unsupported platform: ${key}`);
    console.error(`Supported: ${Object.keys(PLATFORMS).join(", ")}`);
    process.exit(1);
  }
  return name;
}

// @intent build the GitHub release download URL for the current ccg version and platform archive.
function getDownloadUrl(assetName) {
  const isWindows = process.platform === "win32";
  const ext = isWindows ? ".zip" : ".tar.gz";
  return `https://github.com/${repo}/releases/download/v${version}/${assetName}${ext}`;
}

// @intent recursively follow HTTP redirects while downloading the release archive.
function followRedirects(url, resolve, reject) {
  const client = url.startsWith("https") ? https : http;
  client.get(url, (res) => {
    if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
      followRedirects(res.headers.location, resolve, reject);
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
}

// @intent fetch a release archive over HTTPS while transparently following redirects.
function download(url) {
  return new Promise((resolve, reject) => {
    followRedirects(url, resolve, reject);
  });
}

// @intent move one extracted executable into the stable npm package bin path.
function installExtractedBinary(binDir, candidates, targetPath) {
  for (const candidate of candidates) {
    const extracted = path.join(binDir, candidate);
    if (fs.existsSync(extracted)) {
      if (extracted !== targetPath) {
        if (fs.existsSync(targetPath)) {
          fs.unlinkSync(targetPath);
        }
        fs.renameSync(extracted, targetPath);
      }
      fs.chmodSync(targetPath, 0o755);
      return;
    }
  }
  throw new Error(`release archive did not contain any of: ${candidates.join(", ")}`);
}

// @intent download and extract platform-specific ccg and ccg-server binaries into the npm package bin directory during install.
async function install() {
  const assetName = getAssetName();
  const url = getDownloadUrl(assetName);
  const binDir = path.join(__dirname, "bin");
  const isWindows = process.platform === "win32";
  const ccgPath = path.join(binDir, isWindows ? "ccg-binary.exe" : "ccg-binary");
  const serverPath = path.join(binDir, isWindows ? "ccg-server-binary.exe" : "ccg-server-binary");

  // Skip if both binaries already exist
  if (fs.existsSync(ccgPath) && fs.existsSync(serverPath)) {
    console.log(`ccg binaries already installed at ${binDir}`);
    return;
  }

  console.log(`Downloading ccg v${version} for ${getPlatformKey()}...`);
  console.log(`  ${url}`);

  try {
    const data = await download(url);
    if (!fs.existsSync(binDir)) {
      fs.mkdirSync(binDir, { recursive: true });
    }

    if (isWindows) {
      // Write zip and extract
      const zipPath = path.join(binDir, "ccg.zip");
      fs.writeFileSync(zipPath, data);
      execSync(`powershell -Command "Expand-Archive -Path '${zipPath}' -DestinationPath '${binDir}' -Force"`, { stdio: "ignore" });
      fs.unlinkSync(zipPath);

      installExtractedBinary(binDir, ["ccg.exe", `${assetName}.exe`], ccgPath);
      installExtractedBinary(binDir, ["ccg-server.exe"], serverPath);
    } else {
      // Write tar.gz and extract
      const tarPath = path.join(binDir, "ccg.tar.gz");
      fs.writeFileSync(tarPath, data);
      execSync(`tar xzf "${tarPath}" -C "${binDir}"`, { stdio: "ignore" });
      fs.unlinkSync(tarPath);

      installExtractedBinary(binDir, ["ccg", assetName], ccgPath);
      installExtractedBinary(binDir, ["ccg-server"], serverPath);
    }

    console.log(`ccg v${version} installed successfully.`);
  } catch (err) {
    console.error(`Failed to install ccg: ${err.message}`);
    console.error(`You can build manually: make build`);
    process.exit(1);
  }
}

install();
