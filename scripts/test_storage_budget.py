#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
"""Offline checks of the image's storage budget: the journal cap and the zram swap unit.

The real controller script runs against an isolated fake sysfs with PATH shims
for mkswap/swapon/swapoff, the same harness idea as scripts/test_status_led.py.
Whether the stick's kernel has zram, and whether swapon succeeds there, is a
device question this test cannot answer and does not claim to.
"""
import configparser
import os
import pathlib
import re
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'firmware/overlay/usr/local/libexec/nassimhub-zram'
JOURNALD = ROOT / 'firmware/overlay/etc/systemd/journald.conf.d/00-nassimhub-storage-budget.conf'
UNIT = ROOT / 'firmware/overlay/etc/systemd/system/nassimhub-zram.service'


class ZramScriptTests(unittest.TestCase):
    def run_script(self, tmp, arguments=(), zram_control=True, environment=None):
        """Run the real script against a fake sysfs; return (code, stdout, stderr, fake-tree)."""
        root = pathlib.Path(tmp)
        control = root / 'sys/class/zram-control'
        block = root / 'sys/block'
        run = root / 'run'
        if zram_control:
            control.mkdir(parents=True, exist_ok=True)
            (control / 'hot_add').write_text('0\n')
            (control / 'hot_remove').write_text('')
            (block / 'zram0').mkdir(parents=True, exist_ok=True)
            (block / 'zram0/disksize').write_text('0')
        run.mkdir(parents=True, exist_ok=True)
        log = root / 'calls.log'
        log.write_text('')
        binary = root / 'bin'
        binary.mkdir(exist_ok=True)
        # POSIX separators, so the same shim text works under any sh (the build
        # hosts run Linux; developers run this suite elsewhere too).
        log_posix = log.as_posix()
        for name, line in (('mkswap', f'echo "mkswap $*" >> "{log_posix}"'),
                           ('swapon', f'echo "swapon $*" >> "{log_posix}"'),
                           ('swapoff', f'echo "swapoff $*" >> "{log_posix}"')):
            shim = binary / name
            shim.write_text(f'#!/bin/sh\n{line}\n')
            shim.chmod(0o755)
        source = SCRIPT.read_text()
        source = source.replace('control=/sys/class/zram-control', f'control="{control}"')
        source = source.replace('block=/sys/block', f'block="{block}"')
        source = source.replace('run=/run/nassimhub-zram-device', f'run="{run}/nassimhub-zram-device"')
        script = root / 'nassimhub-zram'
        script.write_text(source)
        result = subprocess.run(['sh', str(script), *arguments], text=True, capture_output=True,
                                env=dict(os.environ, PATH=str(binary) + ':' + os.environ['PATH'], **(environment or {})))
        return result, root

    def test_start_creates_swap_with_default_size(self):
        with tempfile.TemporaryDirectory() as tmp:
            result, root = self.run_script(tmp)
            self.assertEqual(result.returncode, 0, result.stderr)
            # 192 MiB default, byte-for-byte what the script must ask the kernel for.
            self.assertEqual((root / 'sys/block/zram0/disksize').read_text().strip(), str(192 * 1048576))
            self.assertIn('swapon -p 100 /dev/zram0', (root / 'calls.log').read_text())
            self.assertIn('mkswap /dev/zram0', (root / 'calls.log').read_text())
            self.assertTrue((root / 'run/nassimhub-zram-device').exists())

    def test_configuration_overrides_the_size(self):
        with tempfile.TemporaryDirectory() as tmp:
            result, root = self.run_script(tmp, environment={'zram_mib': '96'})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / 'sys/block/zram0/disksize').read_text().strip(), str(96 * 1048576))

    def test_unset_or_empty_size_means_the_default(self):
        for empty in ('', None):
            environment = None if empty is None else {'zram_mib': ''}
            with tempfile.TemporaryDirectory() as tmp:
                result, root = self.run_script(tmp, environment=environment)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual((root / 'sys/block/zram0/disksize').read_text().strip(), str(192 * 1048576))

    def test_invalid_size_is_refused_not_guessed(self):
        for bad in ('0', '-5', 'lots'):
            with tempfile.TemporaryDirectory() as tmp:
                result, _ = self.run_script(tmp, environment={'zram_mib': bad})
                self.assertNotEqual(result.returncode, 0, f'zram_mib={bad!r} must be refused')
                self.assertIn('zram_mib', result.stderr)

    def test_kernel_without_zram_is_reported_and_is_not_a_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            result, root = self.run_script(tmp, zram_control=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('no zram-control', result.stdout)
            self.assertEqual((root / 'calls.log').read_text(), '')

    def test_second_start_while_running_is_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            first, _ = self.run_script(tmp)
            self.assertEqual(first.returncode, 0, first.stderr)
            second, _ = self.run_script(tmp)
            self.assertNotEqual(second.returncode, 0, 'a second start must not add a second device')
            self.assertIn('already running', second.stderr)

    def test_stop_removes_the_device_and_is_idempotent(self):
        with tempfile.TemporaryDirectory() as tmp:
            started, _ = self.run_script(tmp)
            self.assertEqual(started.returncode, 0, started.stderr)
            stopped, root = self.run_script(tmp, arguments=('stop',))
            self.assertEqual(stopped.returncode, 0, stopped.stderr)
            self.assertIn('swapoff /dev/zram0', (root / 'calls.log').read_text())
            self.assertEqual((root / 'sys/class/zram-control/hot_remove').read_text().strip(), '0')
            self.assertFalse((root / 'run/nassimhub-zram-device').exists())
            again, _ = self.run_script(tmp, arguments=('stop',))
            self.assertEqual(again.returncode, 0, 'stopping with nothing to stop is a no-op, not an error')

    def test_unknown_mode_is_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            result, _ = self.run_script(tmp, arguments=('restart',))
            self.assertEqual(result.returncode, 2)


class StorageBudgetFileTests(unittest.TestCase):
    def test_journal_drop_in_caps_usage_and_keeps_headroom(self):
        parser = configparser.ConfigParser(strict=True)
        parser.read_string(JOURNALD.read_text())
        self.assertIn('Journal', parser.sections())
        maximum = parser['Journal']['SystemMaxUse']
        keep_free = parser['Journal']['SystemKeepFree']
        # Plain size units systemd accepts; the values themselves must be sane
        # for a few GiB of eMMC: a cap that allows more than the kept-free
        # headroom would defeat the point of having either.
        for value in (maximum, keep_free):
            self.assertRegex(value, r'^\d+[KMGT]?$')
        self.assertLess(int(re.match(r'^(\d+)', maximum).group(1)),
                        int(re.match(r'^(\d+)', keep_free).group(1)),
                        'SystemMaxUse must stay below SystemKeepFree')

    def test_unit_runs_the_script_and_tolerates_a_missing_module(self):
        text = UNIT.read_text()
        self.assertIn('ExecStart=/usr/local/libexec/nassimhub-zram', text)
        self.assertIn('ExecStop=/usr/local/libexec/nassimhub-zram stop', text)
        # The minus sign is the whole tolerance: without it a kernel that has
        # zram built in (no module to load) would fail the unit on every boot.
        self.assertIn('ExecStartPre=-/sbin/modprobe zram', text)
        self.assertIn('EnvironmentFile=-/etc/nassimhub/zram.conf', text)
        self.assertIn('WantedBy=multi-user.target', text)
        self.assertIn('RemainAfterExit=yes', text)


if __name__ == '__main__':
    unittest.main()
