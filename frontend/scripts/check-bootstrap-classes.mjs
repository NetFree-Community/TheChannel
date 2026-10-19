// Every Bootstrap-looking class used in a template must exist in the compiled
// stylesheet. Bootstrap is compiled from a subset (src/bootstrap-subset.scss);
// a class dropped from the subset would otherwise silently unstyle markup.
//
//   node scripts/check-bootstrap-classes.mjs <dist/browser dir>
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const dist = process.argv[2];
if (!dist) { console.error('usage: check-bootstrap-classes.mjs <dist/browser>'); process.exit(2); }

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (name.endsWith('.html')) out.push(p);
  }
  return out;
}

const css = readdirSync(dist).filter((f) => f.endsWith('.css')).map((f) => readFileSync(join(dist, f), 'utf8')).join('\n');
const defined = new Set();
for (const m of css.matchAll(/\.((?:[a-zA-Z0-9_-]|\\[.:%/])+)/g)) defined.add(m[1].replace(/\\/g, ''));

// Bootstrap's utility and component prefixes; everything else is component CSS.
// Component names (card, badge, alert, table) are matched exactly, because
// component styles reuse those words for their own BEM classes (.card-head).
const bs = /^(?:(?:d|flex|justify-content|align-items|align-self|align-content|order|gap|row-gap|column-gap|m[tbsexy]?|p[tbsexy]?|text|fs|fw|fst|lh|rounded|border|bg|shadow|overflow|w|h|mw|mh|vw|vh|position|top|bottom|start|end|container|row|col|g|gx|gy|spinner|img|ratio)(?:-|$)|(?:visually-hidden|text-truncate|btn-close|form-label|form-control|form-text|small|lead|list-unstyled|list-inline|mx-auto|ms-auto|me-auto|card|card-body|card-title|card-subtitle|card-text|card-header|card-footer|card-img|card-img-top|card-link|badge|alert|alert-primary|alert-secondary|alert-success|alert-danger|alert-warning|alert-info|alert-light|alert-dark|alert-link|alert-heading|table|table-sm|table-striped|table-hover|table-bordered|table-responsive)$)/;

const used = new Map();
for (const file of walk(join(root, 'src'))) {
  const html = readFileSync(file, 'utf8');
  for (const m of html.matchAll(/\sclass="([^"]*)"/g)) {
    for (const cls of m[1].split(/\s+/).filter(Boolean)) {
      if (bs.test(cls) && !defined.has(cls)) used.set(cls, (used.get(cls) || new Set()).add(file.replace(root + '/', '')));
    }
  }
}
if (used.size) {
  console.error('classes used in templates but missing from the compiled CSS:');
  for (const [cls, files] of used) console.error(`  .${cls}  (${[...files].join(', ')})`);
  process.exit(1);
}
console.log('every Bootstrap-looking template class exists in the compiled CSS');
