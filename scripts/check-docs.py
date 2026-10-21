#!/usr/bin/env python3
"""Validate public Markdown and prevent internal docs returning to the source tree."""
from pathlib import Path
import re
import sys
from urllib.parse import unquote, urlsplit

root = Path(__file__).resolve().parent.parent
files = [root / 'README.md', root / 'AGENTS.md', *sorted((root / 'changelog').rglob('*.md'))]
errors = []
if (root / 'docs').exists():
    errors.append('docs/: internal documentation belongs in the private documentation repository')
for path in files:
    text = path.read_text()
    localized_notes = path.is_relative_to(root / 'changelog') and path.name != 'en.md'
    if not localized_notes and re.search(r'[\u0400-\u04ff]', text):
        errors.append(f'{path.relative_to(root)}: documentation must be English')
    targets = re.findall(r'\[[^\]]*\]\(([^)]+)\)', text)
    targets += re.findall(r'(?:src|href)=[\"\']([^\"\']+)', text)
    for target in targets:
        target = target.strip('<>')
        url = urlsplit(target)
        if url.scheme or url.netloc or not url.path:
            continue
        resolved = (path.parent / unquote(url.path)).resolve()
        if not resolved.is_relative_to(root) or not resolved.exists():
            errors.append(f'{path.relative_to(root)}: missing local link {target}')
if errors:
    sys.exit('\n'.join(errors))
print(f'Validated {len(files)} maintained documentation files')
