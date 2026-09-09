"""PL-04: Bound silent startup/import/run failures and prove descendant cleanup."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time

from runner import execute_case
from support.backend_case import BackendCase, run_class
from support.lifecycle import start_server


class HarnessBoundaryTests(BackendCase):
    def assert_pid_gone(self, pid):
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                return
            time.sleep(0.02)
        self.fail(f"Child PID {pid} survived cleanup")

    def test_silent_and_partial_line_startups_have_real_timeouts(self):
        for prefix in ("", "print('starting', end='', flush=True)"):
            with self.subTest(prefix=prefix):
                executable = self.case_dir / ("partial" if prefix else "silent")
                executable.write_text(f"#!{sys.executable}\nimport os,time\nfrom pathlib import Path\n"
                                      "Path(__file__ + '.pid').write_text(str(os.getpid()))\n"
                                      f"{prefix}\ntime.sleep(30)\n")
                executable.chmod(0o700)
                started = time.monotonic()
                with self.assertRaises(TimeoutError):
                    start_server(executable, self.case_dir / "probe", startup_timeout=2,
                                 policy_path=self.context["policy_path"], log_dir=self.case_dir / (executable.name + "-log"))
                self.assertLess(time.monotonic() - started, 4)
                self.assert_pid_gone(int(Path(str(executable) + ".pid").read_text()))

    def test_failed_health_check_reaps_process(self):
        executable = self.case_dir / "unhealthy"
        executable.write_text(f"#!{sys.executable}\nimport os,time,json\nfrom pathlib import Path\n"
                              "Path(__file__ + '.pid').write_text(str(os.getpid()))\n"
                              "print(json.dumps({'ready': True, 'base_url': 'http://127.0.0.1:1'}), flush=True)\n"
                              "time.sleep(30)\n")
        executable.chmod(0o700)
        with self.assertRaises(TimeoutError):
            start_server(executable, self.case_dir / "probe", startup_timeout=2,
                         policy_path=self.context["policy_path"], log_dir=self.case_dir / "health-log")
        self.assert_pid_gone(int(Path(str(executable) + ".pid").read_text()))

    def probe_case(self, name, source, timeout=1):
        directory = self.case_dir / name
        directory.mkdir()
        case_file = directory / "fixture_case.py"
        case_file.write_text(source)
        context = {**self.context, "case_dir": directory, "bin_path": self.bin_path}
        result = execute_case(case_file, context, timeout)
        saved = json.loads((directory / "case-result.json").read_text())
        self.assertEqual(saved, result)
        return directory, result

    def test_import_timeout_and_invalid_results_fail_closed(self):
        probes = [("blocked_import", "import time; time.sleep(30)", 0.3),
                  ("missing_status", "def run(context): return {}", 2),
                  ("null_result", "def run(context): return None", 2),
                  ("early_exit", "import os; os._exit(0)", 2)]
        for name, source, timeout in probes:
            with self.subTest(name=name):
                _, result = self.probe_case(name, source, timeout)
                self.assertEqual(result["status"], "failed", result)
                self.assertIn("error", result)

    def test_case_timeout_and_assertion_cleanup_real_server(self):
        for name, tail in [("timeout", "import time; time.sleep(30)"),
                           ("assertion", "raise AssertionError('intentional probe')")]:
            with self.subTest(name=name):
                source = ("from support.lifecycle import start_server\n"
                          "def run(context):\n"
                          "    server = start_server(context['bin_path'], context['case_dir'] / 'data', policy_path=context['policy_path'])\n"
                          "    (context['case_dir'] / 'server.pid').write_text(str(server.pid))\n"
                          f"    {tail}\n")
                directory, result = self.probe_case(name, source, 2)
                self.assertEqual(result["status"], "failed", result)
                self.assert_pid_gone(int((directory / "server.pid").read_text()))

    def test_explicit_missing_binary_and_optimized_assertions_are_rejected(self):
        for args, env in [(["--bin", str(self.case_dir / "missing")], os.environ.copy()),
                          ([], {**os.environ, "PYTHONOPTIMIZE": "1"})]:
            proc = subprocess.run([sys.executable, str(self.context["runner_py"]), "M00", *args],
                                  capture_output=True, text=True, timeout=5, env=env)
            self.assertNotEqual(proc.returncode, 0, proc.stdout + proc.stderr)


def run(context):
    return run_class(HarnessBoundaryTests, context)
