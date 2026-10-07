#!/usr/bin/env python3
"""Collect upstream legal texts for production builds; fail on missing inputs.

Uses Go's selected package graph (not test/tool modules), for browser and desktop
builds on all supported targets. Root legal texts and legal files below used
package directories/ancestors preserve embedded third-party notices as well.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / 'THIRD-PARTY-NOTICES.txt'
LEGAL_NAME = re.compile(r'^(?:(?:third[-_ ]party[-_ ])?(?:licen[cs]es?|notices?)|copying|copyright|patents|authors)(?:[._ -].*)?$', re.I)


def legal_files(directory):
    return sorted(p for p in directory.iterdir() if p.is_file() and LEGAL_NAME.fullmatch(p.name)
                  and p.suffix.lower() not in ('.go', '.c', '.h', '.js', '.json', '.pdf'))


def decode_stream(text):
    decoder = json.JSONDecoder()
    offset = 0
    while offset < len(text):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        if offset == len(text):
            break
        obj, offset = decoder.raw_decode(text, offset)
        yield obj


def package_graph():
    modules = {}
    standard_dirs = set()
    for platform in ('linux', 'windows', 'darwin'):
        for arch in ('amd64', 'arm64'):
            for desktop in (False, True):
                tags = 'wails production' + (' gtk3' if platform == 'linux' else '') if desktop else ''
                env = {**os.environ, 'GOOS': platform, 'GOARCH': arch,
                       'CGO_ENABLED': '1' if desktop and platform != 'windows' else '0'}
                args = ['go', 'list', '-mod=readonly', '-deps', '-json']
                if tags:
                    args += ['-tags', tags]
                args += ['./cmd/spk-ocular']
                result = subprocess.run(args, cwd=ROOT, env=env, text=True, encoding='utf-8', capture_output=True, check=True)
                for pkg in decode_stream(result.stdout):
                    if pkg.get('Standard'):
                        standard_dirs.add(Path(pkg['Dir']))
                        continue
                    mod = pkg.get('Module')
                    if not mod or mod.get('Main'):
                        continue
                    if mod.get('Replace'):
                        raise ValueError(f'Review replacement license provenance: {mod["Path"]}')
                    key = (mod['Path'], mod['Version'])
                    entry = modules.setdefault(key, {'root': Path(mod['Dir']), 'dirs': set()})
                    entry['dirs'].add(Path(pkg['Dir']))
    return modules, standard_dirs


def component_files(root, directories):
    files = set(legal_files(root))
    if not any(p.name.lower().startswith(('license', 'licence', 'copying')) for p in files):
        raise ValueError(f'Missing upstream license: {root.name}')
    for directory in directories:
        # Package trees may embed native code with its own license.
        for parent in (directory, *directory.parents):
            if not parent.is_relative_to(root):
                break
            files.update(legal_files(parent))
        for p in directory.rglob('*'):
            if p.is_file() and LEGAL_NAME.fullmatch(p.name) and p in legal_files(p.parent):
                files.add(p)
    return [{'name': p.relative_to(root).as_posix(), 'text': p.read_text(encoding='utf-8')}
            for p in sorted(files)]


def render_component(name, files):
    if not files or not any(f['text'].strip() for f in files):
        raise ValueError(f'Empty legal texts: {name}')
    chunks = [f'\n{"=" * 78}\n{name}\n{"=" * 78}\n']
    for file in files:
        if not file['text'].strip():
            raise ValueError(f'Empty legal text: {name}/{file["name"]}')
        # Normalize line endings/trailing whitespace only; preserve all legal
        # wording, copyright statements and paragraph boundaries.
        text = '\n'.join(line.rstrip() for line in file['text'].splitlines())
        chunks += [f'\n--- {file["name"]} ---\n', text.rstrip() + '\n']
    return ''.join(chunks)


def collect():
    frontend = json.loads((ROOT / 'web/license-inputs.json').read_text(encoding='utf-8'))
    if frontend.get('schema') != 1 or not frontend.get('packages'):
        raise ValueError('Missing frontend license inventory; run make build-web')
    modules, standard_dirs = package_graph()
    texts = ['SPK Ocular third-party licenses and notices\n\n'
             'SPK Ocular source is Apache-2.0. The components below retain their own\n'
             'licenses. This collection covers browser and desktop builds for Linux,\n'
             'Windows and macOS on amd64 and arm64; some components are target-specific.\n'
             'Full upstream legal texts are reproduced, including inherited notices.\n'
             'Upstream files may describe optional components not linked by Ocular.\n']
    goroot = Path(subprocess.check_output(['go', 'env', 'GOROOT'], cwd=ROOT, text=True, encoding='utf-8').strip())
    goversion = subprocess.check_output(['go', 'env', 'GOVERSION'], cwd=ROOT, text=True, encoding='utf-8').strip()
    texts.append(render_component(f'Go runtime and standard library ({goversion})', component_files(goroot, standard_dirs)))
    for (name, version), entry in sorted(modules.items()):
        texts.append(render_component(f'{name} {version}', component_files(entry['root'], entry['dirs'])))
    for pkg in frontend['packages']:
        if pkg['license'] != 'MIT':
            raise ValueError(f'Review frontend license: {pkg["name"]}: {pkg["license"]}')
        if pkg['name'] == '@wailsio/runtime':
            key = ('github.com/wailsapp/wails/v3', 'v' + pkg['version'])
            if key not in modules:
                raise ValueError('Wails runtime and Go module versions differ')
            if not any(re.search(r'licen[cs]e|copying', f['name'], re.I) for f in pkg['files']):
                path = modules[key]['root'] / 'LICENSE'
                pkg['files'].append({'name': 'LICENSE (matching Wails Go module)', 'text': path.read_text(encoding='utf-8')})
        texts.append(render_component(f'{pkg["name"]} {pkg["version"]} (frontend)', pkg['files']))
    return ''.join(texts), len(modules), len(frontend['packages'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='Fail if the committed notices are stale')
    args = parser.parse_args()
    text, go_count, web_count = collect()
    if args.check:
        if not OUTPUT.exists() or OUTPUT.read_text(encoding='utf-8') != text:
            raise ValueError('THIRD-PARTY-NOTICES.txt is stale; run make licenses')
    else:
        OUTPUT.write_text(text, encoding='utf-8', newline='\n')
        (ROOT / 'web/dist/THIRD-PARTY-NOTICES.txt').write_text(text, encoding='utf-8', newline='\n')
    print(f'licenses: {go_count} Go modules, Go runtime, {web_count} frontend components; '
          f'{"verified" if args.check else "wrote"} THIRD-PARTY-NOTICES.txt')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as error:
        raise SystemExit(str(error)) from error
