"""Resolve a versioned SDK candidate through Go modules, without a workspace."""
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess

MODULE = 'github.com/KanaDoodle/KanaRPC-Go'


def escaped(value):
    return ''.join('!' + c.lower() if 'A' <= c <= 'Z' else c for c in value)


def validate(candidate):
    manifest = json.loads((candidate / 'manifest.json').read_text())
    version = manifest['version']
    if manifest['module'] != MODULE or not re.fullmatch(r'v0\.\d+\.\d+(?:-rc\.\d+)?', version) or manifest.get('publication') != 'LOCAL_UNPUBLISHED_CANDIDATE':
        raise ValueError('invalid SDK module/version')
    target = candidate / 'proxy' / escaped(MODULE) / '@v'
    for suffix in ('.mod', '.info', '.zip'):
        name = version + suffix
        if hashlib.sha256((target / name).read_bytes()).hexdigest() != manifest['artifacts'].get(name):
            raise ValueError('SDK artifact checksum mismatch: ' + suffix)
    return manifest


def prepare(repo, candidate, output):
    manifest = validate(candidate)
    if output.exists() and any(output.iterdir()):
        raise ValueError('SDK resolution requires a new empty output directory')
    output.mkdir(parents=True, exist_ok=True)
    cache = output / 'module-cache'
    env = dict(os.environ, GOWORK='off', GOMODCACHE=str(cache), GONOSUMDB=MODULE,
               GOPRIVATE='', GONOPROXY='')
    # Existing Go download archives are a read-only fallback for dependencies.
    # The candidate itself is downloaded only into this run's private cache.
    shared_cache = pathlib.Path(subprocess.check_output(['go', 'env', 'GOMODCACHE'], text=True).strip())
    env['GOPROXY'] = ','.join([(candidate / 'proxy').as_uri(), (shared_cache / 'cache/download').as_uri(), 'https://proxy.golang.org'])
    return resolve(repo, manifest['version'], output, env, manifest['publication'])


def prepare_published(repo, version, output):
    if not re.fullmatch(r'v0\.\d+\.\d+(?:-rc\.\d+)?', version) or int(version.split('.')[1]) < 2:
        raise ValueError('streaming requires an explicit v0.2+ SDK version')
    if output.exists() and any(output.iterdir()):
        raise ValueError('published SDK resolution requires a new empty output directory')
    output.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, GOWORK='off', GOMODCACHE=str(output / 'module-cache'), GOPRIVATE='', GONOPROXY='',
               GONOSUMDB='', GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org')
    return resolve(repo, version, output, env, 'PUBLIC_PROXY_AND_CHECKSUM_VERIFIED')


def resolve(repo, version, output, env, publication):
    cache = pathlib.Path(env['GOMODCACHE'])
    modfile = output / 'campustrace-stream.mod'
    shutil.copyfile(repo / 'go.mod', modfile)
    shutil.copyfile(repo / 'go.sum', modfile.with_suffix('.sum'))

    def go(*args):
        try:
            return subprocess.check_output(['go', *args], cwd=repo, env=env, text=True, stderr=subprocess.STDOUT)
        except subprocess.CalledProcessError as error:
            raise RuntimeError('SDK resolution failed: ' + error.output.strip()) from None

    go('mod', 'edit', '-modfile=' + str(modfile), '-require=' + MODULE + '@' + version)
    downloaded = json.loads(go('mod', 'download', '-modfile=' + str(modfile), '-json', MODULE + '@' + version))
    selected = json.loads(go('list', '-modfile=' + str(modfile), '-m', '-json', MODULE))
    sdk_dir = pathlib.Path(selected['Dir']).resolve()
    if selected.get('Replace') or selected['Version'] != version or not sdk_dir.is_relative_to(cache.resolve()):
        raise ValueError('SDK must resolve from the private module cache with no replacement')
    result = {'module': MODULE, 'version': version, 'publication': publication,
              'sum': downloaded['Sum'], 'go_mod_sum': downloaded['GoModSum'], 'replace': False,
              'workspace': 'off', 'sdk_source': str(sdk_dir), 'modfile': str(modfile),
              'real_vllm': 'NOT_VALIDATED'}
    (output / 'resolution.json').write_text(json.dumps(result, indent=2) + '\n')
    return env, modfile, sdk_dir, result


def snapshot(repo, destination):
    files = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=repo).decode().split('\0')
    for value in set(filter(None, files)):
        if value in ('go.work', 'go.work.sum') or value.startswith('docs/verification/') or '__pycache__' in pathlib.PurePosixPath(value).parts:
            continue
        source = repo / value
        if source.is_file() and not source.is_symlink():
            target = destination / value
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, target)
