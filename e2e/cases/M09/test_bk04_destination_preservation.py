"""BK-04: Real CLI backup/restore may not overwrite sources or existing destinations."""
import hashlib
import subprocess
from support.backend_case import BackendCase, run_class


class BackupDestinationTests(BackendCase):
    def cli(self, *args):
        result = subprocess.run([str(self.bin_path), "backup", *map(str, args)],
                                capture_output=True, text=True, timeout=15)
        self.http_events.append({"cli": list(map(str, args)), "exit_code": result.returncode,
                                 "stdout": result.stdout, "stderr": result.stderr})
        return result

    def backup(self):
        directory = self.case_dir / "backup"
        result = self.cli("create", "--data-dir", self.data_dir, "--output", directory)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return directory

    def hashes(self, directory):
        return {str(p.relative_to(directory)): hashlib.sha256(p.read_bytes()).hexdigest()
                for p in directory.rglob("*") if p.is_file() and p.name != "process.lock"}

    def test_backup_cannot_replace_live_source_directory(self):
        token = (self.data_dir / "worker.token").read_bytes()
        marker = self.data_dir / "keep.txt"
        marker.write_text("source must survive")
        result = self.cli("create", "--data-dir", self.data_dir, "--output", self.data_dir)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(marker.read_text(), "source must survive")
        self.assertEqual((self.data_dir / "worker.token").read_bytes(), token)
        self.get("/api/v1/health")
        self.restart()
        self.create_practice()

    def test_existing_backup_is_preserved_byte_for_byte(self):
        directory = self.backup()
        before = self.hashes(directory)
        result = self.cli("create", "--data-dir", self.data_dir, "--output", directory)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.hashes(directory), before)
        self.assertEqual(self.cli("verify", "--input", directory).returncode, 0)

    def test_restore_cannot_destroy_its_own_input(self):
        directory = self.backup()
        before = self.hashes(directory)
        result = self.cli("restore", "--input", directory, "--target", directory)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.hashes(directory), before)
        self.assertEqual(self.cli("verify", "--input", directory).returncode, 0)

    def test_restore_cannot_overwrite_existing_inactive_data(self):
        directory = self.backup()
        self.server.stop()
        nested = self.data_dir / "nested" / "preserve.txt"
        nested.parent.mkdir()
        nested.write_text("nested data must also survive")
        before = self.hashes(self.data_dir)
        result = self.cli("restore", "--input", directory, "--target", self.data_dir)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.hashes(self.data_dir), before)
        self.restart()
        self.create_practice()


def run(context):
    return run_class(BackupDestinationTests, context)
