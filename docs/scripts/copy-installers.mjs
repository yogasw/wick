// Copy installer scripts from repo `scripts/` into docs `public/` so
// VitePress serves them at site root (e.g. /wick/install.sh).
//
// Run automatically before `npm run build` and `npm run dev` via the
// `prebuild` / `predev` lifecycle scripts in docs/package.json. The
// copies are gitignored — the source of truth lives in scripts/.

import { copyFileSync, mkdirSync, existsSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const repoRoot = resolve(here, '..', '..')
const publicDir = resolve(here, '..', 'public')

const files = [
  'install.sh',
  'install.ps1',
  'install-claude-termux.sh',
]

mkdirSync(publicDir, { recursive: true })

for (const f of files) {
  const src = resolve(repoRoot, 'scripts', f)
  const dst = resolve(publicDir, f)
  if (!existsSync(src)) {
    console.warn(`  skip: ${src} (not found)`)
    continue
  }
  copyFileSync(src, dst)
  console.log(`  copied: scripts/${f} -> docs/public/${f}`)
}
