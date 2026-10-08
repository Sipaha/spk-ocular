"""Expose actionable CI failures without requiring access to private job logs."""
import os


def report_failure(error):
    if os.environ.get('GITHUB_ACTIONS') != 'true':
        return
    detail = str(error)[:2048]
    for field in ('stderr', 'stdout'):
        value = getattr(error, field, None)
        if value:
            detail += '\n' + (value.decode('utf-8', errors='replace') if isinstance(value, bytes) else value)[-4096:]
    # Workflow command data must be one physical line, even for compiler errors.
    detail = detail.replace('%', '%25').replace('\r', '%0D').replace('\n', '%0A')
    print('::error title=Native release verification::' + detail, flush=True)
