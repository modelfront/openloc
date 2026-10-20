import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

// Tailwind emits only the classes its scanner can read verbatim from the
// source. An arbitrary-value class assembled at runtime — `grid-cols-[...${x}]`
// — compiles to nothing, and a grid with no tracks stacks every cell on its
// own line. This scan fails the build on the pattern instead of leaving it to
// someone's eyes.
function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p)
  }
  return out
}

/** Every backtick string in `src` that interpolates `${…}` inside an
 *  arbitrary-value class (`<utility>-[` … `]`). */
export function findInterpolatedArbitraryClasses(src: string): string[] {
  return src.match(/`[^`]*?\b[a-z-]+-\[[^\]`]*\$\{[^`]*`/g) ?? []
}

describe('Tailwind arbitrary classes are literals', () => {
  // Positive control: the scanner must bite on the shape it exists to catch,
  // otherwise a broken regex would keep the suite green forever.
  it('the scanner matches an interpolated arbitrary value', () => {
    expect(findInterpolatedArbitraryClasses('const c = `grid-cols-[${engineCol}] gap-2`')).toHaveLength(1)
    expect(findInterpolatedArbitraryClasses('cn(`w-[calc(100%-${gap}px)]`)')).toHaveLength(1)
  })

  it('the scanner leaves literal arbitrary values and unrelated interpolation alone', () => {
    expect(findInterpolatedArbitraryClasses('const c = `w-[100px] ${cls}`')).toEqual([])
    expect(findInterpolatedArbitraryClasses("const c = 'grid-cols-[1fr_2rem]'")).toEqual([])
    expect(findInterpolatedArbitraryClasses('const c = `${base} h-input`')).toEqual([])
  })

  it('no source file interpolates inside an arbitrary-value class', () => {
    const files = walk(join(__dirname))
    expect(files.length).toBeGreaterThan(0)
    const offenders: string[] = []
    for (const file of files) {
      for (const m of findInterpolatedArbitraryClasses(readFileSync(file, 'utf8'))) {
        offenders.push(`${file.replace(__dirname, 'src')}: ${m.slice(0, 80)}…`)
      }
    }
    expect(offenders, 'assemble the full class strings as literals instead').toEqual([])
  })
})
