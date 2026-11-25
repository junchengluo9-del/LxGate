"""多住宅出口（Multi-Exit）的测试。

覆盖：
  * 槽位资源分配：网卡 / 策略路由表 / 本地代理端口按槽位唯一，出口 1 与旧单出口一致
  * 旧配置迁移：只有单出口的 ui_auth.json 自动变成「出口 1」，路由策略原样保留
  * 出口 CRUD：新建不带自动连接、重命名、默认出口不可删除、删除后槽位号不复用
  * 动态内存准入：可用内存不足时拒绝新隧道并给出中文提示，已在跑的出口进程不被动到
  * 防撞车：同一节点不会被两个出口同时占用
  * 路由隔离：每个出口用自己的路由表与网卡，选路候选排除其它出口占用的节点
  * HTTP 接口 GET /api/exits：返回出口列表与内存预算

运行： python3 -m unittest discover -s tests -p "*_test.py" -v
"""

from __future__ import annotations

import json
import os
import shutil
import sys
import tempfile
import unittest
import urllib.request
from http.server import ThreadingHTTPServer
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

TMP_ROOT = Path(tempfile.mkdtemp(prefix="vpngate-multi-exit-test-"))
os.environ.setdefault("VPNGATE_DATA_DIR", str(TMP_ROOT / "import-data"))
(TMP_ROOT / "import-data").mkdir(parents=True, exist_ok=True)
os.environ.setdefault("LXGATE_OPENVPN_DEV", "lxgtun0")
os.environ.setdefault("LOCAL_PROXY_PORT", "7929")

import vpngate_manager as vm  # noqa: E402


class FakeProcess:
    """Stands in for an OpenVPN child: alive until terminated."""

    def __init__(self) -> None:
        self.pid = 0
        self.terminated = False

    def poll(self):
        return 0 if self.terminated else None

    def terminate(self) -> None:
        self.terminated = True

    def kill(self) -> None:
        self.terminated = True

    def wait(self, timeout=None) -> int:
        self.terminated = True
        return 0


def make_node(node_id: str, *, country_short: str = "JP", country: str = "日本", ip_type: str = "residential", latency_ms: int = 80, probe_status: str = "available") -> dict:
    return {
        "id": node_id,
        "country": country,
        "country_short": country_short,
        "ip": "203.0.113.10",
        "host_name": node_id,
        "remote_host": "203.0.113.10",
        "remote_port": 1194,
        "ping": latency_ms,
        "latency_ms": latency_ms,
        "score": 100,
        "probe_status": probe_status,
        "ip_type": ip_type,
        "config_text": "client\n",
        "config_file": str(Path(tempfile.gettempdir()) / f"{node_id}.ovpn"),
    }


class ExitSandbox(unittest.TestCase):
    """每个用例一份独立的数据目录、节点表和一个全新的 ExitManager。"""

    def setUp(self) -> None:
        self.data_dir = Path(tempfile.mkdtemp(prefix="data-", dir=TMP_ROOT))
        self._saved = {
            "DATA_DIR": vm.DATA_DIR,
            "NODES_FILE": vm.NODES_FILE,
            "STATE_FILE": vm.STATE_FILE,
            "EXITS_FILE": vm.EXITS_FILE,
            "CONFIG_DIR": vm.CONFIG_DIR,
            "manager": vm.exit_manager,
            "setup": vm.setup_policy_routing,
            "cleanup": vm.cleanup_policy_routing,
            "run": vm.run_openvpn_until_ready,
            "health": vm.check_proxy_health,
            "meminfo": vm.read_meminfo_mb,
            "rss": vm._our_openvpn_rss_mb,
            "proxy": vm.proxy_server.start_proxy_server,
            "ping": vm.vpn_utils.ping_latency_ms,
            "dev": vm.OPENVPN_DEV,
            "proxyPort": vm.LOCAL_PROXY_PORT,
        }
        # Import order decides whether the environment above was seen: another
        # test module may have imported the engine first. Pin the layout the
        # assertions describe instead of relying on it.
        vm.OPENVPN_DEV = "lxgtun0"
        vm.LOCAL_PROXY_PORT = 7929
        vm.DATA_DIR = self.data_dir
        vm.NODES_FILE = self.data_dir / "nodes.json"
        vm.STATE_FILE = self.data_dir / "state.json"
        vm.EXITS_FILE = self.data_dir / "exits.json"
        vm.CONFIG_DIR = self.data_dir / "configs"
        vm.CONFIG_DIR.mkdir(parents=True, exist_ok=True)
        vm.exit_manager = vm.ExitManager()

        self.routes: list[tuple[str, int]] = []
        self.cleaned: list[int] = []
        self.spawned: list[tuple[str, str]] = []

        vm.setup_policy_routing = lambda interface=None, table=None: self.routes.append((interface, table))
        vm.cleanup_policy_routing = lambda table=None: self.cleaned.append(table)

        def fake_run(config_file, keep_alive, route_nopull, timeout=None, dev=None, slot_id=None):
            self.spawned.append((dev or "", slot_id or ""))
            return True, "ok", FakeProcess()

        vm.run_openvpn_until_ready = fake_run
        vm.check_proxy_health = lambda port=None, dev=None: {"ok": True, "ip": "203.0.113.10", "latency_ms": 30}
        vm.proxy_server.start_proxy_server = lambda host, port, device=None: None
        vm.vpn_utils.ping_latency_ms = lambda ip, port, fallback=0: 12
        vm.read_meminfo_mb = lambda: {"total": 1024, "available": 600}
        vm._our_openvpn_rss_mb = lambda: []

    def tearDown(self) -> None:
        vm.DATA_DIR = self._saved["DATA_DIR"]
        vm.NODES_FILE = self._saved["NODES_FILE"]
        vm.STATE_FILE = self._saved["STATE_FILE"]
        vm.EXITS_FILE = self._saved["EXITS_FILE"]
        vm.CONFIG_DIR = self._saved["CONFIG_DIR"]
        vm.exit_manager = self._saved["manager"]
        vm.setup_policy_routing = self._saved["setup"]
        vm.cleanup_policy_routing = self._saved["cleanup"]
        vm.run_openvpn_until_ready = self._saved["run"]
        vm.check_proxy_health = self._saved["health"]
        vm.read_meminfo_mb = self._saved["meminfo"]
        vm._our_openvpn_rss_mb = self._saved["rss"]
        vm.proxy_server.start_proxy_server = self._saved["proxy"]
        vm.vpn_utils.ping_latency_ms = self._saved["ping"]
        vm.OPENVPN_DEV = self._saved["dev"]
        vm.LOCAL_PROXY_PORT = self._saved["proxyPort"]
        shutil.rmtree(self.data_dir, ignore_errors=True)

    def write_legacy_config(self, **extra: object) -> dict:
        config = {
            "username": "admin",
            "password": "exit-test-Passw0rd",
            "secret_path": "secret",
            "routing_mode": "fixed_region",
            "force_country": "JP",
            "routing_ip_type": "residential",
            "connection_enabled": True,
            "fixed_node_id": "",
            "favorite_node_ids": [],
            "fav_fail_fallback": False,
        }
        config.update(extra)
        (self.data_dir / "ui_auth.json").write_text(json.dumps(config), encoding="utf-8")
        return config

    def write_nodes(self, nodes: list[dict]) -> None:
        vm.NODES_FILE.write_text(json.dumps(nodes, ensure_ascii=False), encoding="utf-8")


class ResourceTests(ExitSandbox):
    def test_slots_get_unique_dev_table_and_port(self) -> None:
        manager = vm.exit_manager
        manager.load()
        seen = [vm.exit_resources(slot) for slot in range(1, 5)]
        self.assertEqual(seen[0]["dev"], "lxgtun0")
        self.assertEqual(seen[0]["table"], 100)
        self.assertEqual(seen[0]["proxyPort"], 7929)
        self.assertEqual(seen[1]["dev"], "lxgtun1")
        self.assertEqual(seen[1]["table"], 101)
        self.assertEqual(seen[1]["proxyPort"], 7930)
        self.assertEqual(len({item["dev"] for item in seen}), 4)
        self.assertEqual(len({item["table"] for item in seen}), 4)
        self.assertEqual(len({item["proxyPort"] for item in seen}), 4)

    def test_outbound_tag_keeps_legacy_name_for_exit_one(self) -> None:
        self.write_legacy_config()
        manager = vm.exit_manager
        manager.load()
        self.assertEqual(manager.status_for("exit-1")["outboundTag"], "vpngate-out")
        created = manager.create("第二个出口")
        self.assertEqual(created["outboundTag"], "vpngate-out-2")


class MigrationTests(ExitSandbox):
    def test_single_exit_config_becomes_exit_one(self) -> None:
        self.write_legacy_config()
        manager = vm.exit_manager
        manager.load()
        slots = manager.slots()
        self.assertEqual([slot["id"] for slot in slots], ["exit-1"])
        slot = slots[0]
        self.assertEqual(slot["name"], "出口 1")
        self.assertEqual(slot["routing"]["routing_mode"], "fixed_region")
        self.assertEqual(slot["routing"]["force_country"], "JP")
        self.assertEqual(slot["routing"]["routing_ip_type"], "residential")
        self.assertFalse(slot["routing"]["fav_fail_fallback"])
        self.assertEqual(vm.exit_resources(slot["slot"])["dev"], "lxgtun0")
        # exit-1 refers to the same proxy port the single-exit deployment had
        self.assertEqual(vm.exit_resources(slot["slot"])["proxyPort"], vm.LOCAL_PROXY_PORT)
        persisted = json.loads((self.data_dir / "exits.json").read_text(encoding="utf-8"))
        self.assertEqual(persisted["slots"][0]["id"], "exit-1")

    def test_migration_keeps_ui_auth_as_routing_owner(self) -> None:
        self.write_legacy_config()
        vm.exit_manager.load()
        # load_ui_config fills its own defaults (a hashed password, host and
        # port) exactly as it did before multi-exit; the routing keys must keep
        # their values, and they stay the single owner of exit 1's policy.
        stored = json.loads((self.data_dir / "ui_auth.json").read_text(encoding="utf-8"))
        self.assertEqual(stored["routing_mode"], "fixed_region")
        self.assertEqual(stored["force_country"], "JP")
        self.assertEqual(stored["routing_ip_type"], "residential")
        self.assertFalse(stored["fav_fail_fallback"])


class CrudTests(ExitSandbox):
    def test_create_rename_and_delete(self) -> None:
        self.write_legacy_config()
        manager = vm.exit_manager
        manager.load()
        created = manager.create("软银住宅")
        self.assertEqual(created["id"], "exit-2")
        self.assertEqual(created["name"], "软银住宅")
        self.assertFalse(created["routing"]["connection_enabled"])
        self.assertEqual(len(manager.slots()), 2)

        renamed = manager.update("exit-2", name="日本软银")
        self.assertEqual(renamed["name"], "日本软银")

        with self.assertRaises(RuntimeError):
            manager.delete("exit-1")

        manager.delete("exit-2")
        self.assertEqual([slot["id"] for slot in manager.slots()], ["exit-1"])
        # A deleted slot number is never handed out again: Xray outbounds and
        # routing rules that still mention vpngate-out-2 must not silently point
        # at a different exit.
        again = manager.create("第三个")
        self.assertNotEqual(again["id"], "exit-2")

    def test_update_rejects_unknown_policy_values(self) -> None:
        manager = vm.exit_manager
        manager.load()
        with self.assertRaises(ValueError):
            manager.update("exit-1", routing={"routing_mode": "sorcery"})
        with self.assertRaises(ValueError):
            manager.update("exit-1", routing={"routing_ip_type": "satellite"})


class MemoryAdmissionTests(ExitSandbox):
    def test_refuses_new_tunnel_when_memory_is_short(self) -> None:
        self.write_legacy_config()
        self.write_nodes([make_node("JP_a", latency_ms=30), make_node("JP_b", latency_ms=40)])
        manager = vm.exit_manager
        manager.load()
        manager.create("第二个出口")
        manager.connect("exit-1", "JP_a")
        running = manager.runtime("exit-1")
        self.assertTrue(running.running())

        # 150 MB available - 120 MB reserve = 30 MB left, less than one 32 MB tunnel.
        vm.read_meminfo_mb = lambda: {"total": 900, "available": 150}
        with self.assertRaises(RuntimeError) as ctx:
            manager.connect("exit-2", "JP_b")
        message = str(ctx.exception)
        self.assertIn("内存不足", message)
        self.assertIn("150 MB", message)
        self.assertIn("120 MB", message)
        # The running exit was neither stopped nor replaced.
        self.assertTrue(running.running())
        self.assertIsNone(manager.runtime("exit-2").process)

    def test_budget_reports_headroom_and_max(self) -> None:
        vm.read_meminfo_mb = lambda: {"total": 900, "available": 300}
        vm._our_openvpn_rss_mb = lambda: [41.0]
        budget = vm.memory_budget(active_exits=1, configured_exits=3)
        self.assertEqual(budget["reserveMb"], vm.EXIT_MEM_RESERVE_MB)
        self.assertEqual(budget["perTunnelMb"], 41)
        self.assertEqual(budget["perTunnelSource"], "measured")
        # (300 - 120) // 41 = 4 more tunnels
        self.assertTrue(budget["canStartNew"])
        self.assertEqual(budget["maxActiveExits"], 5)

        vm.read_meminfo_mb = lambda: {"total": 900, "available": 130}
        budget = vm.memory_budget(active_exits=1, configured_exits=3)
        self.assertFalse(budget["canStartNew"])


class IsolationTests(ExitSandbox):
    def test_two_exits_run_on_their_own_device_and_table(self) -> None:
        self.write_legacy_config()
        self.write_nodes([make_node("JP_a", latency_ms=30), make_node("JP_b", latency_ms=40)])
        manager = vm.exit_manager
        manager.load()
        manager.create("出口 2")
        manager.connect("exit-1", "JP_a")
        manager.connect("exit-2", "JP_b")
        self.assertIn(("lxgtun0", 100), self.routes)
        self.assertIn(("lxgtun1", 101), self.routes)
        self.assertEqual(sorted(port for _, port in [(d, t) for d, t in self.routes]), [100, 101])
        self.assertTrue(manager.runtime("exit-1").running())
        self.assertTrue(manager.runtime("exit-2").running())

    def test_a_node_cannot_be_used_by_two_exits(self) -> None:
        self.write_legacy_config()
        self.write_nodes([make_node("JP_a", latency_ms=30), make_node("JP_b", latency_ms=40)])
        manager = vm.exit_manager
        manager.load()
        manager.create("出口 2")
        manager.connect("exit-1", "JP_a")
        with self.assertRaises(RuntimeError) as ctx:
            manager.connect("exit-2", "JP_a")
        self.assertIn("占用", str(ctx.exception))
        # Exit 2 stayed down and exit 1 kept its tunnel.
        self.assertFalse(manager.runtime("exit-2").running())
        self.assertTrue(manager.runtime("exit-1").running())

    def test_nodes_in_use_names_the_exit(self) -> None:
        self.write_legacy_config()
        self.write_nodes([make_node("JP_a"), make_node("JP_b")])
        manager = vm.exit_manager
        manager.load()
        manager.create("大阪出口")
        manager.connect("exit-2", "JP_b")
        self.assertEqual(manager.occupant("JP_b"), "大阪出口")
        self.assertEqual(manager.nodes_in_use(), {"JP_b": ["大阪出口"]})

    def test_policy_pick_skips_nodes_used_by_other_exits(self) -> None:
        self.write_legacy_config(connection_enabled=False)
        self.write_nodes([make_node("JP_fast", latency_ms=20), make_node("JP_slow", latency_ms=200)])
        manager = vm.exit_manager
        manager.load()
        created = manager.create("日本住宅")
        exit_id = created["id"]
        manager.connect("exit-1", "JP_fast")
        picked = manager.auto_pick_node(exit_id)
        self.assertEqual(picked, "JP_slow")

    def test_policy_feedback_is_per_exit(self) -> None:
        self.write_legacy_config(connection_enabled=False)
        self.write_nodes([make_node("JP_good", latency_ms=40)])
        manager = vm.exit_manager
        manager.load()
        created = manager.create("日本住宅")
        exit_id = created["id"]
        manager.update(
            exit_id,
            routing={
                "routing_mode": "fixed_region",
                "force_country": "JP",
                "routing_ip_type": "residential",
                "connection_enabled": True,
            },
        )
        feedback = manager.apply_policy(exit_id)
        self.assertEqual(feedback["code"], "saved_connecting")
        self.assertEqual(feedback["node"], "JP_good")
        self.assertEqual(feedback["exit"], exit_id)
        self.assertEqual(feedback["exitName"], "日本住宅")


class HttpTests(ExitSandbox):
    @classmethod
    def setUpClass(cls) -> None:
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), vm.Handler)
        cls.thread = __import__("threading").Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.port = cls.server.server_address[1]

    @classmethod
    def tearDownClass(cls) -> None:
        cls.server.shutdown()
        cls.server.server_close()

    def setUp(self) -> None:
        super().setUp()
        self.write_legacy_config()
        self.login()

    def login(self) -> None:
        payload = json.dumps({"username": "admin", "password": "exit-test-Passw0rd"}).encode()
        req = urllib.request.Request(
            f"http://127.0.0.1:{self.port}/secret/api/login",
            data=payload,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=10) as resp:
            cookie = resp.headers.get("Set-Cookie", "")
        self.cookie = cookie.split(";")[0]

    def get_json(self, path: str) -> dict:
        req = urllib.request.Request(
            f"http://127.0.0.1:{self.port}/secret{path}",
            headers={"Cookie": self.cookie},
        )
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read().decode("utf-8"))

    def test_exit_list_reports_slots_and_memory(self) -> None:
        data = self.get_json("/api/exits")
        self.assertTrue(data["ok"])
        self.assertEqual([slot["id"] for slot in data["exits"]], ["exit-1"])
        self.assertIn("canStartNew", data["memory"])
        self.assertEqual(data["exits"][0]["dev"], "lxgtun0")

    def test_nodes_payload_carries_exits(self) -> None:
        self.write_nodes([make_node("JP_a")])
        data = self.get_json("/api/nodes")
        self.assertIn("exits", data["state"])
        self.assertIn("memory", data["state"])


if __name__ == "__main__":
    unittest.main()
