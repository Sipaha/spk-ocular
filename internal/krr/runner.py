"""Fixed entrypoint for the pinned KRR container, not a host Python runtime.

Only validated argv selected by the Go provider is accepted. Suppress raw engine
output, block unrelated API/HTTP routes, then emit a bounded report projection.
The Go backend independently validates that projection before retaining it.
"""
import asyncio
import contextlib
import json
import logging
import os
import sys
from urllib.parse import urlsplit


def run():
    sys.path.insert(0, "/app")
    namespace, source, label, cluster = sys.argv[1:5]
    snapshot = json.loads(sys.argv[5]) if len(sys.argv) == 6 else None
    allowed_api = {
        "/apis/apps/v1/namespaces/{namespace}/deployments",
        "/apis/apps/v1/namespaces/{namespace}/statefulsets",
        "/apis/apps/v1/namespaces/{namespace}/daemonsets",
        "/apis/autoscaling/v2/namespaces/{namespace}/horizontalpodautoscalers",
        "/api/v1/namespaces/{namespace}/pods",
    }
    # Imports and engine diagnostics stay out of Kubernetes logs/report output.
    with open(os.devnull, "w") as sink, contextlib.redirect_stdout(sink), contextlib.redirect_stderr(sink):
        import requests
        from kubernetes.client import ApiClient
        from robusta_krr.core.models.objects import PodData
        from robusta_krr import formatters, strategies  # register the pinned engine
        from robusta_krr.core.models.config import Config
        from robusta_krr.core.runner import Runner
        from robusta_krr.core.integrations.kubernetes import ClusterLoader
        from robusta_krr.core.integrations.prometheus.loader import PrometheusMetricsLoader

        del formatters, strategies
        failures = []
        api_call = ApiClient.call_api

        def guarded_api(self, path, method, path_params=None, *args, **kwargs):
            if method != "GET" or path not in allowed_api or (path_params or {}).get("namespace") != namespace:
                failures.append("api_scope")
                raise RuntimeError("unsupported API request")
            try:
                if snapshot is not None:
                    from types import SimpleNamespace
                    if path not in snapshot["api"]:
                        raise RuntimeError("missing snapshot route")
                    response_type = kwargs.get("response_type", args[5] if len(args) > 5 else None)
                    result = self.deserialize(SimpleNamespace(data=json.dumps(snapshot["api"][path])), response_type)
                    if kwargs.get("_return_http_data_only", False):
                        return result
                    return result, 200, {}
                return api_call(self, path, method, path_params, *args, **kwargs)
            except Exception:
                failures.append("api_unavailable")
                raise

        ApiClient.call_api = guarded_api
        if snapshot is not None:
            async def snapshot_pods(self, obj):
                key = obj.kind + "/" + obj.name
                if key not in snapshot["pods"]:
                    failures.append("missing_pod_membership")
                    raise RuntimeError("missing pod membership")
                return [PodData(name=name, deleted=False) for name in snapshot["pods"][key]]

            ClusterLoader.list_pods = snapshot_pods
        # Upstream catches HPA denial and assumes none. Here denial fails the run.
        ClusterLoader._try_list_hpa = ClusterLoader._ClusterLoader__list_hpa

        async def no_summary(self):
            return {}

        # Namespace workload analysis must not issue machine/kube-system summary queries.
        PrometheusMetricsLoader.get_cluster_summary = no_summary
        request = requests.sessions.Session.request
        base = urlsplit(source)
        prefix = base.path.rstrip("/") + "/api/v1/"

        def guarded_http(self, method, url, **kwargs):
            parsed = urlsplit(url)
            if method.upper() not in ("GET", "POST") or (parsed.scheme, parsed.netloc) != (base.scheme, base.netloc) or not parsed.path.startswith(prefix):
                failures.append("source_scope")
                raise RuntimeError("unsupported metrics request")
            route = parsed.path[len(prefix):]
            if route not in ("query", "query_range") and not (method.upper() == "GET" and route.startswith("label/") and route.endswith("/values")):
                failures.append("source_route")
                raise RuntimeError("unsupported metrics route")
            self.trust_env = False
            self.cookies.clear()
            # First supported runner mode uses a source requiring no credentials.
            self.headers.pop("Authorization", None)
            headers = dict(kwargs.get("headers") or {})
            for name in list(headers):
                if name.lower() in ("authorization", "cookie", "proxy-authorization"):
                    del headers[name]
            kwargs.update(headers=headers, allow_redirects=False, verify=True, timeout=15)
            response = request(self, method, url, **kwargs)
            if 300 <= response.status_code < 400:
                failures.append("redirect")
                raise RuntimeError("metrics redirect rejected")
            if response.status_code >= 400:
                failures.append("source_unavailable")
                raise RuntimeError("metrics source unavailable")
            return response

        requests.sessions.Session.request = guarded_http
        if snapshot is not None and not label:
            # Reject an observed shared source; a single/absent label still does
            # not authenticate this cluster or its historical workload identity.
            probe = requests.get(source.rstrip("/") + "/api/v1/label/cluster/values").json()
            if probe.get("status") != "success" or not isinstance(probe.get("data"), list) or len(probe["data"]) > 1:
                raise RuntimeError("metrics scope needs an exact filter")
        settings = dict(cpu_percentile=95, memory_buffer_percentage=15, points_required=100,
                        allow_hpa=False, use_oomkill_data=False, oom_memory_buffer_percentage=25,
                        history_duration=336, timeframe_duration=1.25)
        config = Config(namespaces=[namespace], resources=["Deployment", "StatefulSet", "DaemonSet"],
                        prometheus_url=source, prometheus_ssl_enabled=True,
                        prometheus_cluster_label=cluster or None, prometheus_label=label or None,
                        cpu_min_value=10, memory_min_value=100,
                        max_workers=2, format="json", strategy="simple", other_args=settings,
                        quiet=True, log_to_stderr=True, show_cluster_name=False)
        Config.set_config(config)
        logging.disable(logging.CRITICAL)
        if snapshot is None:
            config.load_kubeconfig()
        else:
            config.inside_cluster = True
        async def unknown_history(self, cluster):
            self.errors.append({"name": "HistoryRangeUnknown"})

        # Avoid a global prometheus_tsdb_head_series probe. Per-resource sample
        # sufficiency remains KRR's own guard; actual overall coverage stays unknown.
        Runner._check_data_availability = unknown_history
        engine = Runner()
        result = asyncio.run(engine._collect_result())
        if failures or len(result.scans) > 256 or len(engine.errors) > 256:
            raise RuntimeError("incomplete or bounded run")
        scans = []
        for scan in result.scans:
            obj = scan.object
            # No config, labels, annotations, Pod names, arbitrary error bodies or
            # prose can reach stdout, even before backend admission.
            scans.append({"object": {"cluster": obj.cluster, "kind": obj.kind,
                          "namespace": obj.namespace, "name": obj.name, "container": obj.container,
                          "hpa": {} if obj.hpa is not None else None,
                          "allocations": {"requests": obj.allocations.requests, "limits": obj.allocations.limits},
                          "warnings": sorted(obj.warnings)},
                          "recommended": scan.recommended.dict(), "severity": scan.severity})
        output = {"config": {"cpu_min_value": config.cpu_min_value, "memory_min_value": config.memory_min_value},
                  "resources": ["cpu", "memory"], "strategy": {"name": "simple", "settings": settings},
                  "scans": scans, "errors": [{} for _ in engine.errors]}
        for scan in output["scans"]:
            scan["recommended"]["info"] = {
                key: value if value in (None, "No data", "Not enough data", "HPA detected", "OOMKill detected") else "unknown"
                for key, value in scan["recommended"]["info"].items()
            }
        encoded = json.dumps(output, allow_nan=False, separators=(",", ":")).encode("utf-8")
        if len(encoded) > 1024 * 1024:
            raise RuntimeError("report too large")
    sys.stdout.buffer.write(encoded + b"\n")
    sys.stdout.buffer.flush()


if __name__ == "__main__":
    try:
        run()
    except BaseException:
        # No raw exception, URL, token, API response, manifest or traceback.
        sys.exit(1)
