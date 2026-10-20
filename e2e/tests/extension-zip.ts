import { execFileSync } from "child_process";
import { mkdtempSync, readFileSync, rmSync } from "fs";
import { tmpdir } from "os";
import path from "path";
import { crc32 } from "zlib";

// Builds the test extension (internal/extension/testdata/mockqe) and packs it
// as an uploadable zip, so no binary is checked in. Needs the Go toolchain;
// the first build takes a while, later ones hit Go's build cache.

const REPO_ROOT = path.join(__dirname, "..", "..");
const MOCKQE_DIR = path.join(REPO_ROOT, "internal", "extension", "testdata", "mockqe");

/** The mock QE extension (id "mockqe", name "Mock QE") as a zip. */
export function buildMockQEZip(): Buffer {
  const out = mkdtempSync(path.join(tmpdir(), "mockqe-"));
  try {
    const wasm = path.join(out, "extension.wasm");
    execFileSync("go", ["build", "-buildmode=c-shared", "-o", wasm, "./internal/extension/testdata/mockqe"], {
      cwd: REPO_ROOT,
      env: { ...process.env, GOOS: "wasip1", GOARCH: "wasm" },
      stdio: "pipe",
    });
    return zipStored({
      "manifest.json": readFileSync(path.join(MOCKQE_DIR, "manifest.json")),
      "extension.wasm": readFileSync(wasm),
    });
  } finally {
    rmSync(out, { recursive: true, force: true });
  }
}

/** A zip with uncompressed (stored) entries — all an extension upload needs. */
export function zipStored(files: Record<string, Buffer>): Buffer {
  const locals: Buffer[] = [];
  const centrals: Buffer[] = [];
  let offset = 0;
  for (const [name, data] of Object.entries(files)) {
    const nameBuf = Buffer.from(name, "utf8");
    const crc = crc32(data);

    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0); // local file header signature
    local.writeUInt16LE(20, 4); // version needed
    local.writeUInt16LE(0x0800, 6); // flags: UTF-8 names
    local.writeUInt16LE(0, 8); // method: stored
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(data.length, 18); // compressed size
    local.writeUInt32LE(data.length, 22); // uncompressed size
    local.writeUInt16LE(nameBuf.length, 26);
    locals.push(local, nameBuf, data);

    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0); // central directory header signature
    central.writeUInt16LE(20, 4); // version made by
    central.writeUInt16LE(20, 6); // version needed
    central.writeUInt16LE(0x0800, 8);
    central.writeUInt16LE(0, 10);
    central.writeUInt32LE(crc, 16);
    central.writeUInt32LE(data.length, 20);
    central.writeUInt32LE(data.length, 24);
    central.writeUInt16LE(nameBuf.length, 28);
    central.writeUInt32LE(offset, 42); // local header offset
    centrals.push(central, nameBuf);

    offset += local.length + nameBuf.length + data.length;
  }
  const centralDir = Buffer.concat(centrals);
  const count = Object.keys(files).length;
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0); // end of central directory signature
  end.writeUInt16LE(count, 8);
  end.writeUInt16LE(count, 10);
  end.writeUInt32LE(centralDir.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, centralDir, end]);
}
