"""节点 IP 类型与路由策略联动的测试。

覆盖：
  * node_matches_country / node_matches_ip_type / select_routing_candidates 的筛选语义
  * apply_routing_policy：新策略与当前节点相符、需切换、无匹配节点、固定节点、
    已关闭自动连接、检测进行中六种反馈
  * HTTP 接口 POST /api/update_routing：保存设置并回传可翻译的反馈码

运行： python3 -m unittest discover -s tests -p "*_test.py" -v
"""

from __future__ import annotations

import json
import os
import shutil
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

TMP_ROOT = Path(tempfile.mkdtemp(prefix="vpngate-routing-test-"))
os.environ.setdefault("VPNGATE_DATA_DIR", str(TMP_ROOT / "import-data"))
(TMP_ROOT / "import-data").mkdir(parents=True, exist_ok=True)

import vpngate_manager as vm  # noqa: E402


def make_node(
    node_id: str,
    *,
    country_short: str,
    country: str,
    ip_type: str,
    probe_status: str = "available",
    latency_ms: int = 100,
    score: int = 1000,
) -> dict[str, object]:
    return {
        "id": node_id,
        "country_short": country_short,
        "country": country,
        "ip_type": ip_type,
        "probe_status": probe_status,
        "latency_ms": latency_ms,
        "score": score,
        "ip": "203.0.113.10",
        "config_file": str(TMP_ROOT / f"{node_id}.ovpn"),
        "config_text": "client\n",
    }


class PolicySandbox(unittest.TestCase):
    """每个用例一份独立的运行数据目录与节点表。"""

    def setUp(self) -> None:
        self.data_dir = Path(tempfile.mkdtemp(prefix="data-", dir=TMP_ROOT))
        self._saved = {
            "DATA_DIR": vm.DATA_DIR,
            "NODES_FILE": vm.NODES_FILE,
            "STATE_FILE": vm.STATE_FILE,
            "active_openvpn_node_id": vm.active_openvpn_node_id,
            "is_connecting": vm.is_connecting,
            "running": vm.active_openvpn_running,
            "schedule": vm.schedule_routing_switch,
        }
        vm.DATA_DIR = self.data_dir
        vm.NODES_FILE = self.data_dir / "nodes.json"
        vm.STATE_FILE = self.data_dir / "state.json"
        vm.active_openvpn_node_id = ""
        vm.is_connecting = False
        vm.active_openvpn_running = lambda: False  # type: ignore[assignment]
        self.switches: list[tuple[str, str]] = []

        def record(node_id: str, ui_cfg: dict, reason: str) -> None:
            self.switches.append((node_id, reason))

        self.record_switch = record
        vm.schedule_routing_switch = record  # type: ignore[assignment]

    def tearDown(self) -> None:
        vm.DATA_DIR = self._saved["DATA_DIR"]
        vm.NODES_FILE = self._saved["NODES_FILE"]
        vm.STATE_FILE = self._saved["STATE_FILE"]
        vm.active_openvpn_node_id = self._saved["active_openvpn_node_id"]
        vm.is_connecting = self._saved["is_connecting"]
        vm.active_openvpn_running = self._saved["running"]  # type: ignore[assignment]
        vm.schedule_routing_switch = self._saved["schedule"]  # type: ignore[assignment]
        shutil.rmtree(self.data_dir, ignore_errors=True)

    def write_nodes(self, nodes: list[dict[str, object]]) -> None:
        vm.NODES_FILE.write_text(json.dumps(nodes, ensure_ascii=False), encoding="utf-8")

    def write_config(self, **extra: object) -> dict[str, object]:
        config = {
            "username": "admin",
            "password": "",
            "secret_path": "secret",
            "routing_mode": "fixed_region",
            "force_country": "JP",
            "routing_ip_type": "residential",
            "connection_enabled": True,
            "fav_fail_fallback": True,
        }
        config.update(extra)
        (self.data_dir / "ui_auth.json").write_text(json.dumps(config), encoding="utf-8")
        return config


class MatchTests(PolicySandbox):
    def test_country_accepts_code_name_and_english(self) -> None:
        node = make_node("JP_1", country_short="JP", country="日本", ip_type="residential")
        self.assertTrue(vm.node_matches_country(node, "JP"))
        self.assertTrue(vm.node_matches_country(node, "jp"))
        self.assertTrue(vm.node_matches_country(node, "日本"))
        self.assertTrue(vm.node_matches_country(node, "Japan"))
        self.assertFalse(vm.node_matches_country(node, "TH"))
        self.assertTrue(vm.node_matches_country(node, ""))

    def test_ip_type_filter_semantics(self) -> None:
        residential = make_node("a", country_short="JP", country="日本", ip_type="residential")
        mobile = make_node("b", country_short="JP", country="日本", ip_type="mobile")
        hosting = make_node("c", country_short="JP", country="日本", ip_type="hosting")
        unknown = make_node("d", country_short="JP", country="日本", ip_type="")
        for node in (residential, mobile, hosting, unknown):
            self.assertTrue(vm.node_matches_ip_type(node, "all"))
        self.assertTrue(vm.node_matches_ip_type(residential, "residential"))
        self.assertTrue(vm.node_matches_ip_type(mobile, "residential"))
        self.assertFalse(vm.node_matches_ip_type(hosting, "residential"))
        self.assertFalse(vm.node_matches_ip_type(unknown, "residential"))
        self.assertTrue(vm.node_matches_ip_type(hosting, "hosting"))
        self.assertFalse(vm.node_matches_ip_type(unknown, "hosting"))

    def test_candidates_respect_country_type_availability_and_order(self) -> None:
        nodes = [
            make_node("JP_slow", country_short="JP", country="日本", ip_type="residential", latency_ms=300),
            make_node("JP_fast", country_short="JP", country="日本", ip_type="residential", latency_ms=80),
            make_node("JP_datacenter", country_short="JP", country="日本", ip_type="hosting", latency_ms=20),
            make_node("TH_res", country_short="TH", country="泰国", ip_type="residential", latency_ms=10),
            make_node("JP_down", country_short="JP", country="日本", ip_type="residential", latency_ms=5, probe_status="unavailable"),
        ]
        cfg = {"routing_mode": "fixed_region", "force_country": "JP", "routing_ip_type": "residential"}
        picked = [n["id"] for n in vm.select_routing_candidates(nodes, cfg)]
        self.assertEqual(picked, ["JP_fast", "JP_slow"])

        picked = [n["id"] for n in vm.select_routing_candidates(nodes, cfg, exclude_id="JP_fast")]
        self.assertEqual(picked, ["JP_slow"])

        hosting_cfg = dict(cfg, routing_ip_type="hosting")
        picked = [n["id"] for n in vm.select_routing_candidates(nodes, hosting_cfg)]
        self.assertEqual(picked, ["JP_datacenter"])

    def test_policy_label(self) -> None:
        self.assertEqual(
            vm.routing_policy_label({"routing_mode": "fixed_region", "force_country": "JP", "routing_ip_type": "residential"}),
            "JP · 住宅 IP",
        )
        self.assertEqual(
            vm.routing_policy_label({"routing_mode": "auto", "force_country": "JP", "routing_ip_type": "all"}),
            "",
        )


class ApplyPolicyTests(PolicySandbox):
    def test_match_keeps_current_node(self) -> None:
        self.write_config()
        self.write_nodes([make_node("JP_good", country_short="JP", country="日本", ip_type="residential")])
        vm.active_openvpn_node_id = "JP_good"
        vm.active_openvpn_running = lambda: True  # type: ignore[assignment]

        result = vm.apply_routing_policy(vm.load_ui_config())

        self.assertEqual(result["code"], "saved_policy_match")
        self.assertEqual(result["level"], "success")
        self.assertEqual(result["node"], "JP_good")
        self.assertEqual(self.switches, [])

    def test_mismatch_schedules_switch_to_best_candidate(self) -> None:
        self.write_config()
        self.write_nodes(
            [
                make_node("TH_host", country_short="TH", country="泰国", ip_type="hosting"),
                make_node("JP_slow", country_short="JP", country="日本", ip_type="residential", latency_ms=200),
                make_node("JP_fast", country_short="JP", country="日本", ip_type="residential", latency_ms=90),
            ]
        )
        vm.active_openvpn_node_id = "TH_host"
        vm.active_openvpn_running = lambda: True  # type: ignore[assignment]

        result = vm.apply_routing_policy(vm.load_ui_config())

        self.assertEqual(result["code"], "saved_switching")
        self.assertEqual(result["node"], "JP_fast")
        self.assertEqual(result["candidates"], 2)
        self.assertEqual([switch[0] for switch in self.switches], ["JP_fast"])

    def test_mismatch_without_candidate_keeps_current_node(self) -> None:
        self.write_config()
        self.write_nodes(
            [
                make_node("TH_host", country_short="TH", country="泰国", ip_type="hosting"),
                make_node("JP_dc", country_short="JP", country="日本", ip_type="hosting"),
            ]
        )
        vm.active_openvpn_node_id = "TH_host"
        vm.active_openvpn_running = lambda: True  # type: ignore[assignment]

        result = vm.apply_routing_policy(vm.load_ui_config())

        self.assertEqual(result["code"], "saved_no_match_kept")
        self.assertEqual(result["level"], "warning")
        self.assertEqual(result["candidates"], 0)
        self.assertEqual(self.switches, [])

    def test_disconnected_connects_to_matching_node(self) -> None:
        self.write_config()
        self.write_nodes([make_node("JP_good", country_short="JP", country="日本", ip_type="residential")])

        result = vm.apply_routing_policy(vm.load_ui_config())

        self.assertEqual(result["code"], "saved_connecting")
        self.assertEqual([switch[0] for switch in self.switches], ["JP_good"])

    def test_disconnected_without_match_says_so(self) -> None:
        self.write_config()
        self.write_nodes([make_node("TH_host", country_short="TH", country="泰国", ip_type="hosting")])

        result = vm.apply_routing_policy(vm.load_ui_config())

        self.assertEqual(result["code"], "saved_no_match")
        self.assertEqual(result["level"], "warning")
        self.assertEqual(self.switches, [])

    def test_busy_engine_defers_switch_to_detection_cycle(self) -> None:
        self.write_config()
        self.write_nodes([make_node("JP_good", country_short="JP", country="日本", ip_type="residential")])
        vm.is_connecting = True

        result = vm.apply_routing_policy(vm.load_ui_config())

        self.assertEqual(result["code"], "saved_pending_switch")
        self.assertEqual(self.switches, [])

    def test_fixed_ip_and_disabled_are_reported_not_switched(self) -> None:
        self.write_config(routing_mode="fixed_ip", routing_ip_type="all")
        result = vm.apply_routing_policy(vm.load_ui_config())
        self.assertEqual(result["code"], "saved_fixed_ip")
        self.assertEqual(self.switches, [])

        self.write_config(connection_enabled=False)
        result = vm.apply_routing_policy(vm.load_ui_config())
        self.assertEqual(result["code"], "saved_disabled")
        self.assertEqual(self.switches, [])

    def test_merge_keeps_ip_type_and_geo_from_previous_cycle(self) -> None:
        # 页面上看到的 IP 类型不能因为一次新的 CSV 拉取就消失。
        previous = make_node("JP_good", country_short="JP", country="日本", ip_type="residential")
        previous["owner"] = "Example ISP"
        previous["probe_status"] = "available"
        previous["latency_ms"] = 88
        fresh = make_node("JP_good", country_short="JP", country="日本", ip_type="", probe_status="not_checked")
        new_node = make_node("KR_new", country_short="KR", country="韩国", ip_type="")

        merged = vm.merge_candidate_nodes([fresh, new_node], [previous])

        by_id = {node["id"]: node for node in merged}
        self.assertEqual(by_id["JP_good"]["ip_type"], "residential")
        self.assertEqual(by_id["JP_good"]["owner"], "Example ISP")
        # 探测结果仍然归零，等待本轮重新测试。
        self.assertEqual(by_id["JP_good"]["probe_status"], "not_checked")
        self.assertEqual(by_id["KR_new"]["ip_type"], "")

    def test_probe_result_does_not_erase_ip_type(self) -> None:
        # 探测失败的节点会带一个空的 ip_type，不能把已有的类型抹掉。
        node = make_node("JP_good", country_short="JP", country="日本", ip_type="residential")
        node["owner"] = "Example ISP"
        patch: dict[str, object] = {
            "id": "JP_good",
            "probe_status": "unavailable",
            "probe_message": "timeout",
            "latency_ms": 0,
            "ip_type": "",
            "owner": "",
        }
        vm.carry_ip_metadata(patch, node)
        self.assertEqual(patch["ip_type"], "residential")
        self.assertEqual(patch["owner"], "Example ISP")
        self.assertEqual(patch["probe_status"], "unavailable")

    def test_merge_keeps_active_node_first_and_dedupes(self) -> None:
        active = make_node("JP_live", country_short="JP", country="日本", ip_type="residential")
        active["active"] = True
        merged = vm.merge_candidate_nodes(
            [make_node("JP_live", country_short="JP", country="日本", ip_type="hosting"), active],
            [],
            active,
        )
        self.assertEqual([node["id"] for node in merged], ["JP_live"])
        self.assertEqual(merged[0]["ip_type"], active["ip_type"])


class UpdateRoutingHttpTests(unittest.TestCase):
    SECRET = "routesecret"
    USER = "admin"
    PASSWORD = "test-password"

    @classmethod
    def setUpClass(cls) -> None:
        cls.data_dir = Path(tempfile.mkdtemp(prefix="api-", dir=TMP_ROOT))
        (cls.data_dir / "ui_auth.json").write_text(
            json.dumps(
                {
                    "username": cls.USER,
                    "password": cls.PASSWORD,
                    "secret_path": cls.SECRET,
                    "host": "127.0.0.1",
                    "port": 8787,
                    "proxy_port": 7928,
                }
            ),
            encoding="utf-8",
        )
        cls._saved = {
            "DATA_DIR": vm.DATA_DIR,
            "NODES_FILE": vm.NODES_FILE,
            "STATE_FILE": vm.STATE_FILE,
            "is_connecting": vm.is_connecting,
            "schedule": vm.schedule_routing_switch,
        }
        vm.DATA_DIR = cls.data_dir
        vm.NODES_FILE = cls.data_dir / "nodes.json"
        vm.STATE_FILE = cls.data_dir / "state.json"
        vm.is_connecting = False
        cls.switches: list[str] = []
        vm.schedule_routing_switch = lambda node_id, ui_cfg, reason: cls.switches.append(node_id)  # type: ignore[assignment]
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), vm.Handler)
        cls.port = cls.server.server_address[1]
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.cookie = cls._login()

    @classmethod
    def tearDownClass(cls) -> None:
        cls.server.shutdown()
        cls.server.server_close()
        vm.DATA_DIR = cls._saved["DATA_DIR"]
        vm.NODES_FILE = cls._saved["NODES_FILE"]
        vm.STATE_FILE = cls._saved["STATE_FILE"]
        vm.is_connecting = cls._saved["is_connecting"]
        vm.schedule_routing_switch = cls._saved["schedule"]  # type: ignore[assignment]
        shutil.rmtree(cls.data_dir, ignore_errors=True)

    @classmethod
    def _login(cls) -> str:
        req = urllib.request.Request(
            f"http://127.0.0.1:{cls.port}/{cls.SECRET}/api/login",
            data=json.dumps({"username": cls.USER, "password": cls.PASSWORD}).encode(),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=10) as resp:
            body = json.loads(resp.read().decode())
            assert body.get("ok"), body
            raw = resp.headers.get("Set-Cookie", "")
        return raw.split(";")[0]

    def post(self, payload: dict) -> tuple[int, dict]:
        req = urllib.request.Request(
            f"http://127.0.0.1:{self.port}/{self.SECRET}/api/update_routing",
            data=json.dumps(payload).encode(),
            headers={"Content-Type": "application/json", "Cookie": self.cookie},
            method="POST",
        )
        try:
            with urllib.request.urlopen(req, timeout=20) as resp:
                return resp.status, json.loads(resp.read().decode())
        except urllib.error.HTTPError as err:
            return err.code, json.loads(err.read().decode() or "{}")

    def test_saves_and_reports_policy_without_matching_nodes(self) -> None:
        vm.NODES_FILE.write_text(
            json.dumps([make_node("TH_host", country_short="TH", country="泰国", ip_type="hosting")]),
            encoding="utf-8",
        )
        status, body = self.post(
            {"routing_mode": "fixed_region", "force_country": "JP", "routing_ip_type": "residential"}
        )
        self.assertEqual(status, 200, body)
        self.assertTrue(body["ok"], body)
        self.assertEqual(body["code"], "saved_no_match")
        self.assertEqual(body["level"], "warning")
        self.assertIn("JP", body["message"])
        saved = json.loads((self.data_dir / "ui_auth.json").read_text(encoding="utf-8"))
        self.assertEqual(saved["routing_mode"], "fixed_region")
        self.assertEqual(saved["force_country"], "JP")
        self.assertEqual(saved["routing_ip_type"], "residential")

    def test_switches_when_policy_disagrees_with_active_node(self) -> None:
        vm.NODES_FILE.write_text(
            json.dumps(
                [
                    make_node("TH_host", country_short="TH", country="泰国", ip_type="hosting"),
                    make_node("JP_res", country_short="JP", country="日本", ip_type="residential"),
                ]
            ),
            encoding="utf-8",
        )
        saved_state = (vm.active_openvpn_node_id, vm.active_openvpn_running)
        vm.active_openvpn_node_id = "TH_host"
        vm.active_openvpn_running = lambda: True  # type: ignore[assignment]
        try:
            status, body = self.post(
                {"routing_mode": "fixed_region", "force_country": "JP", "routing_ip_type": "residential"}
            )
        finally:
            vm.active_openvpn_node_id, vm.active_openvpn_running = saved_state
        self.assertEqual(status, 200, body)
        self.assertEqual(body["code"], "saved_switching")
        self.assertEqual(body["node"], "JP_res")
        self.assertIn("JP_res", self.switches)


if __name__ == "__main__":
    unittest.main()
