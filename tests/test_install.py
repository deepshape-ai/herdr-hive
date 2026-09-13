"""Exercise the real shell installer with offline GitHub fixtures."""
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


INSTALLER = Path(__file__).resolve().parents[1] / "install.sh"


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.destination = self.root / "installed with spaces"
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                        FIXTURES=str(self.root), TEST_SYSTEM="Linux", TEST_ARCH="x86_64")
        self.executable("uname", '#!/bin/sh\ncase "$1" in -s) echo "$TEST_SYSTEM";; -m) echo "$TEST_ARCH";; esac\n')
        self.executable("herdr", '#!/bin/sh\nprintf "%s\\n" "$@" > "$FIXTURES/herdr-args"\n')
        self.executable("curl", "#!" + sys.executable + '''
import os, pathlib, shutil, sys
root = pathlib.Path(os.environ['FIXTURES'])
args = sys.argv[1:]
url = args[-1]
if (root / 'fail-download').exists(): sys.exit(22)
if '/releases?' in url:
    source = root / ('page-' + url.rsplit('=', 1)[1] + '.json')
elif url.endswith('SHA256SUMS'): source = root / 'SHA256SUMS'
else: source = root / url.rsplit('/', 1)[1]
shutil.copyfile(source, args[args.index('--output') + 1])
''')

    def executable(self, name, content):
        path = self.bin / name
        path.write_text(content)
        path.chmod(0o755)

    def fixtures(self, component="hive", version="2.10.0", target="linux-amd64", extra=None):
        releases = [dict(tag_name=component + "/v" + v, draft=d, prerelease=p,
                         body='quoted "tag_name": "hive/v99.0.0"',
                         assets=[dict(tag_name="hive/v88.0.0", draft=False, prerelease=False)])
                    for v, d, p in [(version, False, False), ("2.9.0", False, False),
                                    ("99.0.0", True, False), ("98.0.0", False, True),
                                    ("97.0.0-rc.1", False, False)]]
        releases += [dict(tag_name="bee/v96.0.0" if component == "hive" else "hive/v96.0.0",
                          draft=False, prerelease=False)]
        (self.root / "page-1.json").write_text(json.dumps(releases))
        name = component + "-" + version + "-" + target + ".tar.gz"
        prefix = component + "-" + target + "/"
        files = {component: version, "README.md": "readme", "LICENSE": "license"}
        if component == "bee":
            files["herdr-plugin.toml"] = 'version = "' + version + '"\n'
        with tarfile.open(self.root / name, "w:gz") as archive:
            for filename, text in files.items():
                member = tarfile.TarInfo(prefix + filename)
                data = text.encode()
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
            if extra:
                archive.addfile(extra)
        digest = hashlib.sha256((self.root / name).read_bytes()).hexdigest()
        (self.root / "SHA256SUMS").write_text(digest + "  " + name + "\n")

    def run_install(self, component="hive"):
        return subprocess.run(["sh", str(INSTALLER), component, "--install-dir", str(self.destination)],
                              env=self.env, capture_output=True, text=True, timeout=15)

    def test_latest_component_version_and_private_state(self):
        self.fixtures()
        result = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.destination / "hive").read_text(), "2.10.0")
        self.assertEqual((self.destination / "state").stat().st_mode & 0o777, 0o700)

    def test_bee_mac_arm64_registers_exact_path(self):
        self.env.update(TEST_SYSTEM="Darwin", TEST_ARCH="arm64")
        self.fixtures("bee", target="darwin-arm64")
        result = self.run_install("bee")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / "herdr-args").read_text().splitlines(),
                         ["plugin", "link", str(self.destination), "--enabled"])

    def test_pagination(self):
        self.fixtures()
        (self.root / "page-2.json").write_bytes((self.root / "page-1.json").read_bytes())
        (self.root / "page-1.json").write_text(json.dumps([
            dict(tag_name="bee/v1.0.0", draft=False, prerelease=False)] * 100))
        result = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_bad_checksum_leaves_destination_absent(self):
        self.fixtures()
        p = self.root / "SHA256SUMS"
        p.write_text("0" * 64 + p.read_text()[64:])
        self.assertNotEqual(self.run_install().returncode, 0)
        self.assertFalse(self.destination.exists())

    def test_archive_traversal_and_links_rejected(self):
        for name, kind in [("../escape", tarfile.REGTYPE),
                           ("hive-linux-amd64/README.zh.md", tarfile.SYMTYPE)]:
            with self.subTest(name=name):
                member = tarfile.TarInfo(name)
                member.type = kind
                member.linkname = str(self.root / "escape") if kind == tarfile.SYMTYPE else ""
                self.fixtures(extra=member)
                self.assertNotEqual(self.run_install().returncode, 0)
                self.assertFalse(self.destination.exists())
                self.assertFalse((self.root / "escape").exists())

    def test_existing_installation_untouched(self):
        self.destination.mkdir()
        marker = self.destination / "hive"
        marker.write_text("running version")
        self.assertNotEqual(self.run_install().returncode, 0)
        self.assertEqual(marker.read_text(), "running version")

    def test_copy_failure_can_be_retried(self):
        self.fixtures()
        self.executable("cp", "#!/bin/sh\nexit 1\n")
        self.assertNotEqual(self.run_install().returncode, 0)
        self.assertFalse(self.destination.exists())
        (self.bin / "cp").unlink()
        result = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_registration_failure_keeps_complete_plugin(self):
        self.fixtures("bee")
        self.executable("herdr", "#!/bin/sh\nexit 1\n")
        result = self.run_install("bee")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Files installed. Retry", result.stderr)
        self.assertTrue((self.destination / "bee").is_file())
        self.assertTrue((self.destination / "herdr-plugin.toml").is_file())

    def test_failed_download_and_unsupported_platform(self):
        (self.root / "fail-download").touch()
        self.assertNotEqual(self.run_install().returncode, 0)
        self.env["TEST_ARCH"] = "unsupported"
        self.assertIn("Supported architectures", self.run_install().stderr)
        self.assertFalse(self.destination.exists())


if __name__ == "__main__":
    unittest.main()
