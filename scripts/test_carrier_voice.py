#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
"""Run the real carrier sampler against isolated paths and command stand-ins."""
import os, pathlib, subprocess, tempfile, unittest
ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'node/deploy/carrier/nassimhub-carrier-voice'

class CarrierVoiceTest(unittest.TestCase):
    def run_sample(self, operator='46009', calls='No calls were found', owns=False,
                   baseline='2', compatible='thwc,ufi003', marker=True, fail_mm=False):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root/'compatible').write_text(compatible+'\0qcom,msm8916\0')
            if marker: (root/'marker').touch()
            if owns: (root/'owned').touch()
            fake_mm = root/'mmcli'
            fake_mm.write_text("""#!/bin/sh
if [ "$FAIL_MM" = yes ]; then exit 1; fi
case "$*" in
 *--voice-list-calls*) printf '%s\n' "$CALLS";;
 *'-i '*) printf 'sim.properties.operator-code : %s\n' "$OPERATOR";;
 *) printf 'modem.generic.sim : /org/freedesktop/ModemManager1/SIM/7\nmodem.3gpp.operator-code : 46001\n';;
esac
""")
            helper = root/'helper'
            helper.write_text("""#!/bin/sh
echo "$1" >> "$LOG"
printf 'voice-domain=%s\n' "$BASELINE"
if [ "$BASELINE" = 2 ] && [ "$1" = ps-preferred ]; then echo 'runtime voice domain requested=3'; fi
if [ "$BASELINE" = 3 ] && [ "$1" = cs-preferred ]; then echo 'runtime voice domain requested=2'; fi
""")
            fake_mm.chmod(0o755); helper.chmod(0o755)
            source = SCRIPT.read_text().replace('helper=/usr/local/libexec/nassimhub-voice-domain', f'helper={helper}').replace('compatible=/proc/device-tree/compatible', f'compatible={root}/compatible').replace('marker=/etc/nassimhub/local-volte.enabled', f'marker={root}/marker').replace('owned=/run/nassimhub-carrier-voice/ps-applied', f'owned={root}/owned').replace('while :; do sample; sleep 15 & wait $!; done','sample')
            script = root/'sample.sh'; script.write_text(source)
            env = dict(os.environ, PATH=str(root)+':'+os.environ['PATH'], OPERATOR=operator,
                       CALLS=calls, BASELINE=baseline, LOG=str(root/'log'), FAIL_MM='yes' if fail_mm else 'no')
            subprocess.run(['sh', str(script)], env=env, check=True, capture_output=True)
            return (root/'log').read_text().splitlines() if (root/'log').exists() else [], (root/'owned').exists()

    def test_verified_unicom_home_codes_apply_runtime_preference(self):
        for code in ('46001', '46009'):
            self.assertEqual(self.run_sample(operator=code), (['ps-preferred'], True))

    def test_other_home_carrier_is_not_mistaken_for_serving_unicom(self):
        for code in ('46011', '46000', '', '46006'):
            self.assertEqual(self.run_sample(operator=code), ([], False))

    def test_owned_runtime_preference_restored_on_carrier_change(self):
        self.assertEqual(self.run_sample(operator='46011', owns=True, baseline='3'), (['cs-preferred'], False))

    def test_existing_ps_preference_is_not_claimed_as_owned(self):
        self.assertEqual(self.run_sample(baseline='3'), (['ps-preferred'], False))

    def test_calls_and_unknown_query_never_mutate_preferences(self):
        for calls in ('', '/org/freedesktop/ModemManager1/Call/7 outgoing active', 'unexpected'):
            self.assertEqual(self.run_sample(calls=calls), ([], False))
        self.assertEqual(self.run_sample(fail_mm=True), ([], False))
        self.assertEqual(self.run_sample(operator='', owns=True, baseline='3'), ([], True))

    def test_other_hardware_and_unaccepted_device_do_nothing(self):
        self.assertEqual(self.run_sample(compatible='other,board'), ([], False))
        self.assertEqual(self.run_sample(marker=False), ([], False))

if __name__ == '__main__': unittest.main()
