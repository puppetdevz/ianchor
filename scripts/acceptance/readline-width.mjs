// Opt-in generator for the pinned baseline's per-code-point readline widths.
// Run with Node 24.22.2: node --expose-internals scripts/acceptance/readline-width.mjs
// Prints Go source; review and gofmt it before replacing the generated table.
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const width = require('internal/util/inspect').getStringWidth;
if (process.version !== 'v24.22.2' || process.versions.icu !== '78.3') {
  throw new Error('width oracle requires Node v24.22.2 / ICU 78.3');
}
const ranges = [];
for (let cp = 0; cp <= 0x10ffff; cp++) {
  const w = Math.min(2, width(String.fromCodePoint(cp), false));
  if (w === 1) continue;
  const last = ranges.at(-1);
  if (last && last[1] === cp - 1 && last[2] === w) last[1] = cp;
  else ranges.push([cp, cp, w]);
}
console.log(`// Code generated from Node ${process.version} / ICU ${process.versions.icu} getStringWidth per code point; DO NOT EDIT.
// Readline treats any width above one as two. Table widths use a separate contract.
// Regenerate: node --expose-internals scripts/acceptance/readline-width.mjs
package cli

import "sort"

func readlineRuneWidth(r rune) int {
 i:=sort.Search(len(readlineWidths),func(i int) bool{return readlineWidths[i].last>=r})
 if i<len(readlineWidths)&&readlineWidths[i].first<=r {return readlineWidths[i].width};return 1
}
var readlineWidths=[]struct{first,last rune;width int}{
${ranges.map(([a,b,w]) => ` {0x${a.toString(16)},0x${b.toString(16)},${w}},`).join('\n')}
}`);
