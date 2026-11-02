#!/usr/bin/env node
/*
Validate the syntax of every mermaid fenced block in the Markdown files of
the repository with the real Mermaid parser (mermaid.parse) - syntax only,
no rendering, so no browser or Chromium is needed. jsdom provides the DOM
globals the Mermaid bundle expects at import time.

Usage:
  make check-mermaid                 (installs mermaid + jsdom in a temp dir)
  node hack/check_mermaid.mjs [FILE...]   (mermaid and jsdom must already be
                                           resolvable, e.g. via NODE_PATH)

Requires Node.js >= 20.11 (import.meta.dirname).
Exit code 0 when every block parses, 1 otherwise.
*/

import { createRequire } from 'node:module';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { pathToFileURL } from 'node:url';

const MERMAID_BLOCK = /```mermaid\n(.*?)```/gs;
const EXCLUDED_DIRS = new Set(['.git', 'node_modules']);

function markdownFiles(dir) {
  const files = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      if (EXCLUDED_DIRS.has(entry)) continue;
      files.push(...markdownFiles(full));
    } else if (entry.endsWith('.md')) {
      files.push(full);
    }
  }
  return files;
}

async function main() {
  const root = join(import.meta.dirname, '..');
  const targets = process.argv.length > 2
    ? process.argv.slice(2)
    : markdownFiles(root);
  const blocks = [];
  for (const file of targets) {
    const text = readFileSync(file, 'utf8');
    const display = file.startsWith(root + '/') ? relative(root, file) : file;
    for (const match of text.matchAll(MERMAID_BLOCK)) {
      const line = text.slice(0, match.index).split('\n').length;
      blocks.push({ file: display, line, diagram: match[1] });
    }
  }
  if (blocks.length === 0) {
    console.log('No mermaid blocks found.');
    return;
  }

  // Mermaid's UMD bundle reads DOM globals at import time: set them up
  // before requiring it. The probe path keeps the resolution anchored to
  // this script while NODE_PATH supplies the actual packages.
  const require = createRequire(pathToFileURL(join(import.meta.dirname, 'node_modules/.probe')));
  const { JSDOM } = require('jsdom');
  const dom = new JSDOM('<!DOCTYPE html>');
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, 'navigator', {
    value: dom.window.navigator,
    configurable: true,
  });
  const mermaid = require('mermaid').default;
  mermaid.initialize({ startOnLoad: false });

  let failures = 0;
  for (const { file, line, diagram } of blocks) {
    try {
      await mermaid.parse(diagram, { suppressErrors: false });
      console.log(`${file}:${line}: OK`);
    } catch (error) {
      failures++;
      const message = String(error.message ?? error).split('\n').slice(0, 2).join(' | ');
      console.error(`${file}:${line}: INVALID - ${message}`);
    }
  }
  console.log(`${blocks.length - failures}/${blocks.length} mermaid block(s) valid.`);
  if (failures > 0) process.exit(1);
}

await main();
