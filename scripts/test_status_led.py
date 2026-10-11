#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
"""Run the real shell controller against isolated LEDs and a cached-state fixture."""
import os
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'firmware/overlay/usr/local/libexec/nassimhub-status-led'


class StatusLEDTests(unittest.TestCase):
    def run_states(self, states, enabled='yes'):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            leds = root / 'leds'
            for name in ('blue:', 'green:wan', 'red:', 'mmc0::'):
                p = leds / name
                p.mkdir(parents=True)
                for attr in ('trigger', 'brightness', 'delay_on', 'delay_off'):
                    (p / attr).write_text('unchanged')
            binary = root / 'bin'
            binary.mkdir()
            mmcli = binary / 'mmcli'
            mmcli.write_text('#!/bin/sh\n[ "$*" = "--timeout=3 -m any -K" ] || exit 20\n'
                             '[ "$TEST_STATE" != unavailable ] || exit 1\n'
                             'printf "modem.generic.state : %s\\n" "$TEST_STATE"\n')
            mmcli.chmod(0o755)
            checks = []
            for state in states:
                checks.append(f'TEST_STATE={state}; export TEST_STATE; sample\n'
                              'printf "sample=%s\\n" "$last"\n')
            # Record the real controller's final state before its EXIT cleanup.
            checks.append('for name in blue: green:wan red: mmc0::; do\n'
                          'for attr in trigger brightness delay_on delay_off; do\n'
                          'printf "%s/%s=%s\\n" "$name" "$attr" "$(cat "$leds/$name/$attr")"\n'
                          'done; done\n')
            source = SCRIPT.read_text().replace('leds=/sys/class/leds', f'leds="{leds}"')
            source = source.replace('while :; do sample; sleep 10 & wait $!; done', ''.join(checks))
            script = root / 'controller'
            script.write_text(source)
            result = subprocess.run(['sh', str(script)], text=True, capture_output=True,
                                    env=dict(os.environ, PATH=str(binary)+':'+os.environ['PATH'],
                                             NSH_STATUS_LED_ENABLED=enabled))
            self.assertEqual(result.returncode, 0, result.stderr)
            for name in ('blue:', 'green:wan', 'red:'):
                self.assertEqual((leds/name/'trigger').read_text().strip(), 'none')
                self.assertEqual((leds/name/'brightness').read_text().strip(), '0')
            self.assertEqual((leds/'mmc0::'/'trigger').read_text(), 'unchanged')
            return result.stdout

    def test_registered_and_data_connection_are_green(self):
        out = self.run_states(['registered', 'connecting', 'connected', 'disconnecting'])
        self.assertEqual(out.count('status-led: registered'), 1)
        self.assertIn('green:wan/trigger=default-on', out)
        self.assertIn('red:/brightness=0', out)

    def test_no_sim_and_unavailable_are_waiting_not_fault(self):
        for state in ('locked', 'disabled', 'searching', 'unavailable'):
            out = self.run_states([state])
            self.assertIn('sample=waiting', out)
            self.assertIn('blue:/trigger=timer', out)
            self.assertIn('blue:/delay_on=300', out)
            self.assertIn('blue:/delay_off=1700', out)

    def test_failure_debounce_and_recovery(self):
        out = self.run_states(['failed', 'failed', 'unavailable', 'failed', 'failed', 'failed', 'registered'])
        self.assertEqual([x for x in out.splitlines() if x.startswith('sample=')],
                         ['sample=waiting']*5+['sample=failed', 'sample=registered'])
        self.assertIn('status-led: failed', out)
        self.assertIn('green:wan/trigger=default-on', out)

    def test_persistent_failure_is_red_slow_flash(self):
        out = self.run_states(['failed']*3)
        self.assertIn('red:/trigger=timer', out)
        self.assertIn('red:/delay_off=1700', out)
        self.assertIn('green:wan/brightness=0', out)

    def test_disabled_never_reads_modem(self):
        self.assertEqual(self.run_states(['registered'], enabled='no'), '')


if __name__ == '__main__':
    unittest.main()
