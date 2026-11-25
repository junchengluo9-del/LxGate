"""独立模式登录与节点列表拉取的安全回归测试。

覆盖：
  * 密码以 scrypt 哈希保存，旧版明文密码自动迁移且原密码仍可登录
  * 公开的旧默认秘密路径会被替换成随机值
  * 登录失败次数限制、过期会话清理
  * 节点列表默认只做校验证书的 HTTPS 拉取，不安全重试需显式开启
"""
from __future__ import annotations

import json
import shutil
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import vpngate_manager as vm  # noqa: E402


class AuthSandbox(unittest.TestCase):
    def setUp(self) -> None:
        self.data_dir = Path(tempfile.mkdtemp(prefix="auth-sec-"))
        self._orig = (vm.DATA_DIR, vm.EMBEDDED_MODE)
        vm.DATA_DIR = self.data_dir
        vm.EMBEDDED_MODE = False
        vm.active_sessions.clear()
        vm.login_failures.clear()

    def tearDown(self) -> None:
        vm.DATA_DIR, vm.EMBEDDED_MODE = self._orig
        vm.active_sessions.clear()
        vm.login_failures.clear()
        shutil.rmtree(self.data_dir, ignore_errors=True)

    def stored(self) -> dict:
        return json.loads((self.data_dir / "ui_auth.json").read_text(encoding="utf-8"))


class PasswordStorageTests(AuthSandbox):
    def test_hash_roundtrip(self) -> None:
        h = vm.hash_ui_password("S3cret-pass")
        self.assertTrue(h.startswith("scrypt$"))
        self.assertTrue(vm.verify_ui_password(h, "S3cret-pass"))
        self.assertFalse(vm.verify_ui_password(h, "wrong"))
        self.assertFalse(vm.verify_ui_password("", "S3cret-pass"))
        self.assertFalse(vm.verify_ui_password("garbage", "S3cret-pass"))

    def test_legacy_plaintext_is_migrated(self) -> None:
        (self.data_dir / "ui_auth.json").write_text(
            json.dumps({"username": "admin", "password": "Legacy-Pass1", "secret_path": "mysecret"}),
            encoding="utf-8",
        )
        cfg = vm.load_ui_config()
        self.assertNotIn("password", cfg)
        disk = self.stored()
        self.assertNotIn("password", disk)
        self.assertTrue(vm.verify_ui_password(disk["password_hash"], "Legacy-Pass1"))
        self.assertEqual(disk["secret_path"], "mysecret")

    def test_fresh_install_gets_random_secret_path(self) -> None:
        cfg = vm.load_ui_config()
        self.assertTrue(cfg["password_hash"].startswith("scrypt$"))
        self.assertRegex(cfg["secret_path"], r"^[A-Za-z0-9]{16}$")
        self.assertNotIn(cfg["secret_path"], vm.LEGACY_DEFAULT_SECRET_PATHS)

    def test_public_default_secret_path_is_rotated(self) -> None:
        (self.data_dir / "ui_auth.json").write_text(
            json.dumps({"username": "admin", "password": "x", "secret_path": "EJsW2EeBo9lY"}),
            encoding="utf-8",
        )
        cfg = vm.load_ui_config()
        self.assertNotEqual(cfg["secret_path"], "EJsW2EeBo9lY")
        self.assertEqual(self.stored()["secret_path"], cfg["secret_path"])

    def test_session_token_helper_removed(self) -> None:
        self.assertFalse(hasattr(vm, "get_session_token"))


class LoginGuardTests(AuthSandbox):
    def test_rate_limit_after_failures(self) -> None:
        ip = "203.0.113.9"
        for _ in range(vm.LOGIN_MAX_FAILURES - 1):
            vm.record_login_failure(ip)
        self.assertFalse(vm.login_blocked(ip))
        vm.record_login_failure(ip)
        self.assertTrue(vm.login_blocked(ip))
        later = time.time() + vm.LOGIN_FAILURE_WINDOW_SECONDS + 1
        self.assertFalse(vm.login_blocked(ip, now=later))

    def test_expired_sessions_are_purged(self) -> None:
        vm.active_sessions["old"] = time.time() - 1
        vm.active_sessions["new"] = time.time() + 60
        vm.purge_expired_sessions()
        self.assertNotIn("old", vm.active_sessions)
        self.assertIn("new", vm.active_sessions)


class NodeListTlsTests(unittest.TestCase):
    def _targets(self, insecure: bool) -> list[tuple[str, bool]]:
        seen: list[tuple[str, bool]] = []

        def fake_fetch(url: str, verify: bool = True) -> str:
            seen.append((url, verify))
            raise RuntimeError("offline")

        with mock.patch.object(vm, "INSECURE_TLS", insecure), \
             mock.patch.object(vm, "fetch_api_text", fake_fetch), \
             mock.patch.object(vm, "cached_nodes", lambda: [{"id": "x"}]), \
             mock.patch.object(vm, "log_to_json", lambda *a, **k: None), \
             mock.patch.object(vm, "set_state", lambda **k: None), \
             mock.patch.object(vm, "load_blacklist", lambda: {}), \
             mock.patch.object(vm.vpn_utils, "diagnose_api_failure", lambda url: ("E", "diag")):
            with self.assertRaises(RuntimeError):
                vm.fetch_candidates()
        return seen

    def test_default_never_disables_verification(self) -> None:
        seen = self._targets(insecure=False)
        self.assertTrue(seen)
        self.assertTrue(all(verify for _, verify in seen))
        self.assertTrue(all(url.startswith("https://") for url, _ in seen))

    def test_opt_in_allows_insecure_retry(self) -> None:
        seen = self._targets(insecure=True)
        self.assertIn((vm.API_URL, False), seen)

    def test_fetch_api_text_forces_verification(self) -> None:
        captured = {}

        class FakeResp:
            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

            def read(self):
                return b"ok"

        def fake_urlopen(request, timeout=0, context=None):
            captured["context"] = context
            return FakeResp()

        with mock.patch.object(vm, "INSECURE_TLS", False), \
             mock.patch.object(vm.vpn_utils, "get_upstream_proxy", lambda: (None, None, None)), \
             mock.patch.object(vm.urllib.request, "urlopen", fake_urlopen):
            self.assertEqual(vm.fetch_api_text("https://example.invalid/", use_ssl_verify=False), "ok")
        self.assertIsNone(captured["context"])


if __name__ == "__main__":
    unittest.main()
