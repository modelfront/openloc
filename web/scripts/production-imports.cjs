#!/usr/bin/env node
// Lists the packages that the source files under a directory import although
// the package.json does not declare them in "dependencies". What the source
// imports ships in the bundle, and the image's third-party notices list
// production dependencies only, so such a package would ship un-noticed.
// TypeScript's parser reads the imports (static, dynamic, re-exports,
// require), so strings and comments cannot fool it; type-only imports do not
// ship and are skipped, as are test files, type declarations and test
// folders. CSS files are scanned for @import.
//
//   node scripts/production-imports.cjs [<src dir>] [<package.json>]   (from web/)
//
// Exit 1, with the packages and the files that import them, when any is found.
"use strict";
const fs = require("fs");
const path = require("path");
const ts = require("typescript");

const builtins = new Set(require("module").builtinModules);
const skipDir = /^(__tests__|tests?)$/;
const skipFile = /(\.(test|spec)\.[cm]?[jt]sx?|\.d\.ts)$/;
const code = /\.([cm]?[jt]sx?)$/;

function packageOf(specifier) {
  if (/^[./]/.test(specifier) || /^(@\/|node:|virtual:)/.test(specifier)) return null;
  const parts = specifier.split("/");
  const name = specifier.startsWith("@") ? parts.slice(0, 2).join("/") : parts[0];
  return builtins.has(name) ? null : name;
}

// The bare specifiers a TypeScript or JavaScript file imports at runtime.
function codeImports(file, source) {
  const kind = /\.[cm]?tsx$/.test(file) ? ts.ScriptKind.TSX : /\.[cm]?jsx$/.test(file) ? ts.ScriptKind.JSX
    : /\.[cm]?js$/.test(file) ? ts.ScriptKind.JS : ts.ScriptKind.TS;
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, kind);
  const found = [];
  const typeOnlyClause = (clause) =>
    clause && (clause.isTypeOnly || (!clause.name && clause.namedBindings && ts.isNamedImports(clause.namedBindings)
      && clause.namedBindings.elements.length > 0 && clause.namedBindings.elements.every((e) => e.isTypeOnly)));
  const visit = (node) => {
    if (ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier)) {
      if (!typeOnlyClause(node.importClause)) found.push(node.moduleSpecifier.text);
    } else if (ts.isExportDeclaration(node) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
      if (!node.isTypeOnly) found.push(node.moduleSpecifier.text);
    } else if (ts.isCallExpression(node) && node.arguments.length > 0 && ts.isStringLiteralLike(node.arguments[0])
      && (node.expression.kind === ts.SyntaxKind.ImportKeyword
        || (ts.isIdentifier(node.expression) && node.expression.text === "require"))) {
      found.push(node.arguments[0].text);
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return found;
}

// The specifiers a CSS file imports; comments are not imports.
function cssImports(source) {
  const text = source.replace(/\/\*[\s\S]*?\*\//g, "");
  return [...text.matchAll(/@import\s+(?:url\()?\s*["']([^"']+)["']/g)].map((m) => m[1]);
}

// nonProductionImports(srcDir, packageJsonPath): Map of package → Set of files.
function nonProductionImports(srcDir, packageJsonPath) {
  const pj = JSON.parse(fs.readFileSync(packageJsonPath, "utf8"));
  const deps = new Set(Object.keys(pj.dependencies || {}));
  const hits = new Map();
  const note = (pkg, file) => {
    if (!pkg || deps.has(pkg)) return;
    if (!hits.has(pkg)) hits.set(pkg, new Set());
    hits.get(pkg).add(path.relative(path.dirname(packageJsonPath), file));
  };
  (function walk(dir) {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) { if (!skipDir.test(e.name)) walk(p); continue; }
      if (skipFile.test(e.name)) continue;
      if (code.test(e.name)) codeImports(p, fs.readFileSync(p, "utf8")).forEach((s) => note(packageOf(s), p));
      else if (e.name.endsWith(".css")) cssImports(fs.readFileSync(p, "utf8")).forEach((s) => note(packageOf(s), p));
    }
  })(srcDir);
  return hits;
}

if (require.main === module) {
  const srcDir = process.argv[2] || "src";
  const packageJson = process.argv[3] || "package.json";
  const hits = nonProductionImports(srcDir, packageJson);
  if (hits.size) {
    console.log(`\n✗ ${srcDir} imports packages that ${packageJson} does not list in "dependencies" (move them there: they ship, so the image notices must list them):\n`);
    for (const [pkg, files] of hits) {
      const list = [...files];
      console.log(`  ${pkg}  <- ${list.slice(0, 3).join(", ")}${list.length > 3 ? ` (+${list.length - 3})` : ""}`);
    }
    process.exit(1);
  }
  console.log(`✓ every package ${srcDir} imports is a production dependency`);
}

module.exports = { nonProductionImports };
