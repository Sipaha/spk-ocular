#!/usr/bin/env python3
"""Run Helm against verified disposable kind and an owned PostgreSQL pod."""
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import time
from urllib.parse import urlsplit

REPO = Path(__file__).resolve().parent.parent
SCRATCH = Path(os.environ.get('OCULAR_SCRATCH_DIR', REPO.parent / '.agents/tmp')).resolve()
SOURCE = Path(os.environ.get('OCULAR_KIND_KUBECONFIG', REPO / 'build/kind-ocular-dev.kubeconfig')).resolve()
if not SOURCE.is_file():
    sys.exit('Set OCULAR_KIND_KUBECONFIG to the disposable kind-ocular-dev kubeconfig.')
SCRATCH.mkdir(parents=True, exist_ok=True)
RUN = SCRATCH / ('helm-live-' + secrets.token_hex(4))
RUN.mkdir(mode=0o700)
ENV = os.environ.copy()
for key, name in {'HOME': 'home', 'DOCKER_CONFIG': 'docker', 'SPK_OCULAR_HOME': 'data',
                  'TMPDIR': 'tmp', 'XDG_CACHE_HOME': 'cache'}.items():
    directory = RUN / name
    directory.mkdir(mode=0o700)
    ENV[key] = str(directory)
for key in ('HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'http_proxy', 'https_proxy', 'all_proxy',
            'DOCKER_CONTEXT', 'DOCKER_HOST', 'DOCKER_TLS_VERIFY', 'DOCKER_CERT_PATH'):
    ENV.pop(key, None)
ENV['KUBECONFIG'] = str(RUN / 'kubeconfig')
ENV['OCULAR_KIND_KUBECONFIG'] = ENV['KUBECONFIG']
ENV['OCULAR_HELM_LIVE'] = '1'
ENV['NO_PROXY'] = ENV['no_proxy'] = 'localhost,127.0.0.1,::1'
ENV.setdefault('GOCACHE', str(SCRATCH / 'helm-go-cache'))
ENV.setdefault('GOMODCACHE', str(SCRATCH / 'helm-go-mod'))
ENV.setdefault('GOTOOLCHAIN', 'local')
shutil.copyfile(SOURCE, ENV['KUBECONFIG'])
os.chmod(ENV['KUBECONFIG'], 0o600)
K = ['kubectl', '--context=kind-ocular-dev', '--request-timeout=20s',
     '--cache-dir=' + str(RUN / 'cache/kube')]


def kube(*args, data=None, check=True):
    return subprocess.run(K + list(args), input=data, text=True, capture_output=True,
                          env=ENV, cwd=REPO, check=check)


def document(*args):
    return json.loads(kube(*args, '-o', 'json').stdout)


config = document('config', 'view', '--raw', '--flatten')
if config.get('current-context') != 'kind-ocular-dev' or len(config.get('contexts', [])) != 1:
    sys.exit('Refusing kubeconfig other than a single kind-ocular-dev context.')
server = urlsplit(config['clusters'][0]['cluster']['server'])
if server.scheme != 'https' or server.hostname not in ('127.0.0.1', 'localhost', '::1'):
    sys.exit('Refusing a non-loopback kind endpoint.')
nodes = document('get', 'nodes')['items']
if not nodes or any(not n.get('spec', {}).get('providerID', '').startswith('kind://docker/ocular-dev/') for n in nodes):
    sys.exit('Refusing nodes without the ocular-dev kind provider identity.')
# Embed certificates in this private copy; never change the supplied kubeconfig.
Path(ENV['KUBECONFIG']).write_text(json.dumps(config))
namespace = 'ocular-helm-live-' + secrets.token_hex(4)
ENV['OCULAR_HELM_LIVE_NAMESPACE'] = namespace
uid = None
forward = None
password = secrets.token_hex(24)
print('Verified kind-ocular-dev; scratch:', RUN, flush=True)
try:
    ns = {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': namespace,
          'labels': {'ocular.test': 'helm-live', 'ocular.helm.owner': namespace}}}
    created = json.loads(kube('create', '-f', '-', '-o', 'json', data=json.dumps(ns)).stdout)
    uid = created['metadata']['uid']
    secret = {'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'postgres', 'namespace': namespace},
              'stringData': {'password': password}}
    kube('create', '-f', '-', data=json.dumps(secret))
    pod = {'apiVersion': 'v1', 'kind': 'Pod', 'metadata': {'name': 'postgres', 'namespace': namespace},
           'spec': {'automountServiceAccountToken': False, 'containers': [{
               'name': 'postgres', 'image': 'postgres:17.6-alpine', 'imagePullPolicy': 'IfNotPresent',
               'env': [{'name': 'POSTGRES_DB', 'value': 'ocular_helm_live'},
                       {'name': 'POSTGRES_PASSWORD', 'valueFrom': {'secretKeyRef': {'name': 'postgres', 'key': 'password'}}}],
               'readinessProbe': {'exec': {'command': ['pg_isready', '-U', 'postgres', '-d', 'ocular_helm_live']},
                                  'initialDelaySeconds': 2, 'periodSeconds': 2},
               'resources': {'requests': {'cpu': '100m', 'memory': '128Mi'}, 'limits': {'memory': '512Mi'}},
               'volumeMounts': [{'name': 'data', 'mountPath': '/var/lib/postgresql/data'}]}],
               'volumes': [{'name': 'data', 'emptyDir': {}}]}}
    kube('create', '-f', '-', data=json.dumps(pod))
    kube('wait', '-n', namespace, '--for=condition=Ready', 'pod/postgres', '--timeout=180s')
    kube('exec', '-n', namespace, 'postgres', '--', 'psql', '-U', 'postgres', '-d', 'ocular_helm_live',
         '-c', "COMMENT ON DATABASE ocular_helm_live IS 'ocular disposable Helm integration fixture'")
    log_path = RUN / 'postgres-forward.log'
    with log_path.open('w') as log:
        forward = subprocess.Popen(K + ['port-forward', '-n', namespace, 'pod/postgres', ':5432', '--address=127.0.0.1'],
                                   cwd=REPO, env=ENV, stdout=log, stderr=log)
    port = None
    for _ in range(100):
        if forward.poll() is not None:
            raise RuntimeError('Owned PostgreSQL port-forward exited; see scratch log.')
        for line in log_path.read_text().splitlines():
            if line.startswith('Forwarding from 127.0.0.1:'):
                port = line.split(':')[1].split()[0]
        if port:
            break
        time.sleep(.1)
    if not port:
        raise RuntimeError('PostgreSQL port-forward did not become ready.')
    dsn_path = RUN / 'postgres.dsn'
    dsn_path.write_text(f'postgresql://postgres:{password}@127.0.0.1:{port}/ocular_helm_live?sslmode=disable')
    dsn_path.chmod(0o600)
    ENV['OCULAR_HELM_SQL_DSN_FILE'] = str(dsn_path)
    print('PostgreSQL ready in owned namespace:', namespace, flush=True)
    for package, pattern in [('./internal/helm/sqlstore', '^TestSQLLive'),
                              ('./internal/providers/kubernetes', '^TestHelmLive')]:
        result = subprocess.run(['go', 'test', '-race', '-count=1', '-v', '-timeout=10m', '-run', pattern, package],
                                cwd=REPO, env=ENV)
        if result.returncode:
            raise RuntimeError(f'Integration tests failed in {package}')
finally:
    try:
        if uid:
            live = document('get', 'namespace', namespace)
            if live['metadata']['uid'] != uid or live['metadata']['labels'].get('ocular.helm.owner') != namespace:
                raise RuntimeError('Namespace ownership changed; refusing cleanup.')
            # Only this run's namespaced data and explicitly labeled CRDs are removed.
            for crd in document('get', 'crd', '-l', 'ocular.helm.owner=' + namespace)['items']:
                kube('delete', 'crd', crd['metadata']['name'], '--wait=true', '--timeout=60s')
            kube('delete', 'namespace', namespace, '--wait=true', '--timeout=90s')
            print('Removed owned namespace and CRDs:', namespace, flush=True)
    finally:
        if forward is not None and forward.poll() is None:
            forward.terminate()
            forward.wait(timeout=10)
        (RUN / 'postgres.dsn').unlink(missing_ok=True)
