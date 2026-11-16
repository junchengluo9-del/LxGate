"""隧道网卡与重启单元的测试。

覆盖 Part B 的两处部署修复：
  * LXGATE_OPENVPN_DEV 覆盖默认的 tun0（openvpn 命令、策略路由、代理绑定都用它）
  * 嵌入式模式下重启目标是 lxgate.service，而不是独立版的 aimilivpn.service

运行： python3 -m unittest discover -s tests -p "*_test.py" -v
"""

from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent


# 导入引擎时会往 stdout 打初始化日志，所以每个结果带前缀，只取最后一段。
PROBE = (
    "import vpngate_manager as vm\n"
    "import proxy_server as ps\n"
    'print("RESULT device=" + vm.OPENVPN_DEV)\n'
    'print("RESULT proxy=" + ps.TUN_DEVICE)\n'
    'print("RESULT unit=" + vm.service_restart_unit())\n'
    'print("RESULT command=" + " ".join(vm.openvpn_command("cfg.ovpn", True)))\n'
)


def results(env: dict[str, str]) -> dict[str, str]:
    tmp = tempfile.mkdtemp(prefix="lxgate-device-test-")
    child_env = {**os.environ, "VPNGATE_DATA_DIR": tmp, "PYTHONPATH": str(REPO_ROOT)}
    child_env.pop("LXGATE_OPENVPN_DEV", None)
    child_env.update(env)
    out = subprocess.run(
        [sys.executable, "-c", PROBE],
        cwd=str(REPO_ROOT),
        env=child_env,
        capture_output=True,
        text=True,
        timeout=120,
    )
    if out.returncode != 0:
        raise AssertionError(f"probe failed: {out.stderr}")
    parsed: dict[str, str] = {}
    for line in out.stdout.splitlines():
        if line.startswith("RESULT "):
            key, _, value = line[len("RESULT "):].partition("=")
            parsed[key] = value
    if len(parsed) != 4:
        raise AssertionError(f"incomplete probe output: {out.stdout!r}")
    return parsed


class TunnelDeviceTest(unittest.TestCase):
    def test_defaults_to_tun0(self) -> None:
        values = results({})
        self.assertEqual(values["device"], "tun0")
        self.assertEqual(values["proxy"], "tun0")
        self.assertEqual(values["unit"], "aimilivpn.service")
        self.assertIn("--dev tun0", values["command"])

    def test_environment_override_reaches_every_path(self) -> None:
        values = results({"LXGATE_OPENVPN_DEV": "lxgtun0"})
        self.assertEqual(values["device"], "lxgtun0")
        self.assertEqual(values["proxy"], "lxgtun0")
        self.assertIn("--dev lxgtun0", values["command"])

    def test_embedded_restart_targets_lxgate(self) -> None:
        self.assertEqual(results({"LXGATE_EMBEDDED": "1"})["unit"], "lxgate.service")

    def test_blank_override_falls_back(self) -> None:
        self.assertEqual(results({"LXGATE_OPENVPN_DEV": "   "})["device"], "tun0")


if __name__ == "__main__":
    unittest.main()
