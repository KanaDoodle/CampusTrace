"""SDK artifact integrity and source isolation guards, using no model."""
import hashlib
import json
import pathlib
import subprocess
import tempfile
import unittest

from stream_sdk import MODULE, escaped, snapshot, validate


class CandidateGuards(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.target = self.root / 'proxy' / escaped(MODULE) / '@v'
        self.target.mkdir(parents=True)
        self.manifest = {'module': MODULE, 'version': 'v0.2.0-rc.2', 'publication': 'LOCAL_UNPUBLISHED_CANDIDATE', 'artifacts': {}}
        for suffix in ['.mod', '.info', '.zip']:
            name = self.manifest['version'] + suffix
            (self.target / name).write_bytes(b'synthetic-artifact')
            self.manifest['artifacts'][name] = hashlib.sha256(b'synthetic-artifact').hexdigest()
        self.save()

    def save(self):
        (self.root / 'manifest.json').write_text(json.dumps(self.manifest))

    def tearDown(self):
        self.temp.cleanup()

    def test_candidate_is_explicitly_unpublished(self):
        self.assertEqual(validate(self.root)['publication'], 'LOCAL_UNPUBLISHED_CANDIDATE')

    def test_corrupted_archive_is_rejected_before_go_execution(self):
        (self.target / (self.manifest['version'] + '.zip')).write_bytes(b'corrupted')
        with self.assertRaises(ValueError):
            validate(self.root)

    def test_different_module_or_unsafe_version_is_rejected(self):
        for key, value in [('module', 'example.invalid/other'), ('version', '../escape')]:
            original = self.manifest[key]
            self.manifest[key] = value
            self.save()
            with self.assertRaises(ValueError):
                validate(self.root)
            self.manifest[key] = original

    def test_candidate_cannot_claim_public_release(self):
        self.manifest['publication'] = 'PUBLISHED'
        self.save()
        with self.assertRaises(ValueError):
            validate(self.root)

    def test_consumer_snapshot_omits_even_a_tracked_workspace(self):
        repo = self.root / 'repo'
        repo.mkdir()
        (repo / 'go.mod').write_text('module example.invalid/fixture\n')
        (repo / 'go.work').write_text('go 1.25.9\nuse .\n')
        subprocess.run(['git', 'init', '-q'], cwd=repo, check=True)
        subprocess.run(['git', 'add', '.'], cwd=repo, check=True)
        destination = self.root / 'consumer'
        snapshot(repo, destination)
        self.assertTrue((destination / 'go.mod').exists())
        self.assertFalse((destination / 'go.work').exists())


if __name__ == '__main__':
    unittest.main()
