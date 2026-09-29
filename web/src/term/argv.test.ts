import { describe, expect, it } from 'vitest'
import { ArgvError, formatArgv, parseArgv } from './argv'

describe('parseArgv', () => {
  it.each([
    ['ls -la /tmp', ['ls', '-la', '/tmp']],
    ['  spaced   out  ', ['spaced', 'out']],
    [`sh -c 'echo $HOME; ls'`, ['sh', '-c', 'echo $HOME; ls']],
    [`echo "a \\"quoted\\" \\\\ word"`, ['echo', 'a "quoted" \\ word']],
    [`echo "keep \\n as is"`, ['echo', 'keep \\n as is']],
    [`a\\ b c`, ['a b', 'c']],
    [`x '' y ""`, ['x', '', 'y', '']],
    [`pre'mid'"post"`, ['premidpost']],
    ['', []],
  ])('%s', (line, argv) => expect(parseArgv(line)).toEqual(argv))

  it.each([[`echo 'open`], [`echo "open`], [`trailing\\`]])('rejects %s', (line) => {
    expect(() => parseArgv(line)).toThrow(ArgvError)
  })

  it('formats back to the same argv', () => {
    for (const argv of [['sh', '-c', `echo "it's" $X`], ['a', '', 'b c'], ['psql', '-U', 'postgres']]) {
      expect(parseArgv(formatArgv(argv))).toEqual(argv)
    }
  })
})
