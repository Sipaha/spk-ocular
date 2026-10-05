#!/usr/bin/env python3
"""Validate maintained Markdown language and local file links without network I/O."""
from pathlib import Path
import re
import sys
from urllib.parse import unquote, urlsplit

root = Path(__file__).resolve().parent.parent
files = [root / 'README.md', root / 'AGENTS.md', *sorted((root / 'docs').rglob('*.md')), *sorted((root / 'changelog').rglob('*.md'))]
errors = []
for path in files:
    text = path.read_text()
    localized_notes = path.is_relative_to(root / 'changelog') and path.name != 'en.md'
    if not localized_notes and re.search(r'[\u0400-\u04ff]', text):
        errors.append(f'{path.relative_to(root)}: documentation must be English')
    for target in re.findall(r'\[[^\]]*\]\(([^)]+)\)', text):
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
