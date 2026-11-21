"""Deploy only the availability range patch on the current main image.

Server prerequisite: the existing catalog-release helper and ops DB reader.
Run from an isolated release directory containing the verified image archive.
"""
import importlib.util
import pathlib
import subprocess


BASE = pathlib.Path('/root/codebot-catalog-restore-20261118/deploy.py')
spec = importlib.util.spec_from_file_location('catalog_release', BASE)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)
release.ROOT = pathlib.Path(__file__).resolve().parent
release.QUERIES['settings'] = 'SELECT jsonb_agg(to_jsonb(t) ORDER BY key) FROM settings t'


def normalized_groups(data):
    return {(g['name'], g['platform']): {k: v for k, v in g.items() if k != 'cache_usage'}
            for g in data['groups']}


def verify_catalog(before, after):
    assert normalized_groups(before) == normalized_groups(after), 'Public catalog or model prices changed'
    assert before['billing'] == after['billing'], 'Public billing metadata changed'


original_verify_versions = release.verify_versions


def verify_versions(v1, v2):
    original_verify_versions(v1, v2)
    for payload in (v1, v2):
        for monitor in payload['monitoring']:
            for entry in [monitor] + monitor.get('models', []):
                assert 'availability_adjustment' not in entry, 'Adjustment unexpectedly enabled'


original_protected = release.protected


def protected():
    enabled = release.row("SELECT value FROM settings WHERE key='public_transit_availability_adjustment_enabled'")
    assert enabled in (None, 'false'), 'Presentation adjustment is not disabled; recheck deployment scope'
    values = original_protected()
    for container in ('sub2api-postgres', 'sub2api-redis', 'deploy-model-benchmark-1'):
        values['container_' + container] = subprocess.check_output(
            ['docker', 'inspect', '--format', '{{.Id}}', container], text=True).strip()
    return values


release.verify_catalog = verify_catalog
release.verify_versions = verify_versions
release.protected = protected

if __name__ == '__main__':
    release.main()
