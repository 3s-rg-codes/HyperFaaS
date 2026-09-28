#!/usr/bin/env python3
"""Summarize one local whole-platform load run.

Reads the generator request CSV, the generator log, and a small CPU JSON
produced by scripts/load-local.sh, then prints a compact metrics block, writes
latest.json, and (when a baseline exists) prints per-metric deltas.

The percentile estimator matches bench/final/analysis/finalbench_analysis
(numpy-style linear interpolation) so local numbers stay comparable with the
GCE analysis tooling.
"""
from __future__ import annotations

import argparse
import csv
import json
import math
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

FINISHED_RE = re.compile(
    r"finished in (?P<window>[0-9.]+)s: "
    r"scheduled=(?P<scheduled>\d+) "
    r"issued=(?P<issued>\d+) "
    r"dropped=(?P<dropped>\d+) "
    r"missed=(?P<missed>\d+) "
    r"completed=(?P<completed>\d+) "
    r"peak_in_flight=(?P<peak>\d+)"
)

TRUTHY = {"1", "true", "yes", "y", "t"}


def truthy(value: object) -> bool:
    return str(value).strip().lower() in TRUTHY


def parse_float(value: object) -> float | None:
    try:
        if value is None or value == "":
            return None
        return float(value)
    except (TypeError, ValueError):
        return None


def percentile(sorted_values: list[float], pct: float) -> float:
    """Same estimator as finalbench_analysis.cli.percentile."""
    if not sorted_values:
        return math.nan
    if len(sorted_values) == 1:
        return sorted_values[0]
    rank = (pct / 100) * (len(sorted_values) - 1)
    lo = math.floor(rank)
    hi = math.ceil(rank)
    if lo == hi:
        return sorted_values[int(rank)]
    return sorted_values[lo] + (sorted_values[hi] - sorted_values[lo]) * (rank - lo)


def distribution(values: list[float]) -> dict[str, float]:
    if not values:
        return {"count": 0, "p50": math.nan, "p95": math.nan, "p99": math.nan, "max": math.nan}
    vals = sorted(values)
    return {
        "count": len(vals),
        "min": vals[0],
        "p50": percentile(vals, 50),
        "p95": percentile(vals, 95),
        "p99": percentile(vals, 99),
        "max": vals[-1],
    }


def summarize_csv(path: Path) -> dict:
    total = ok = cold = 0
    latencies: list[float] = []
    start_ns: list[int] = []
    end_ns: list[int] = []
    with path.open(newline="") as fh:
        for row in csv.DictReader(fh):
            total += 1
            if truthy(row.get("ok")):
                ok += 1
            if truthy(row.get("cold")):
                cold += 1
            lat = parse_float(row.get("latency_ms"))
            if lat is None:
                ns = parse_float(row.get("latency_ns"))
                lat = ns / 1_000_000 if ns is not None else None
            if lat is not None:
                latencies.append(lat)
            try:
                if row.get("started_at_unix_nano"):
                    start_ns.append(int(row["started_at_unix_nano"]))
                if row.get("ended_at_unix_nano"):
                    end_ns.append(int(row["ended_at_unix_nano"]))
            except (TypeError, ValueError):
                pass
    csv_window = None
    if start_ns and end_ns and max(end_ns) > min(start_ns):
        csv_window = (max(end_ns) - min(start_ns)) / 1e9
    dist = distribution(latencies)
    return {
        "total": total,
        "ok": ok,
        "cold": cold,
        "success_pct": (100.0 * ok / total) if total else 0.0,
        "latency_ms": dist,
        "csv_window_s": csv_window,
    }


def parse_generator_log(path: Path) -> dict:
    text = path.read_text(errors="replace") if path.exists() else ""
    out: dict = {}
    match = FINISHED_RE.search(text)
    if match:
        out["window_s"] = float(match.group("window"))
        for key in ("scheduled", "issued", "dropped", "missed", "completed", "peak"):
            out[key] = int(match.group(key))
    err = re.search(r"^.*?\berror\b.*$", text, re.MULTILINE)
    if "unknown scenario" in text or "must be" in text:
        out["warnings"] = err.group(0).strip() if err else "see log"
    return out


def fmt(value: float | None, digits: int = 2) -> str:
    if value is None or (isinstance(value, float) and math.isnan(value)):
        return "n/a"
    return f"{value:.{digits}f}"


def cleandata(value):
    """Replace non-finite floats with None so JSON stays valid."""
    if isinstance(value, float):
        return value if math.isfinite(value) else None
    if isinstance(value, dict):
        return {k: cleandata(v) for k, v in value.items()}
    if isinstance(value, list):
        return [cleandata(v) for v in value]
    return value


def rss_total_peak(cpu_block: dict) -> float | None:
    """Sum per-role peak RSS (KiB) from a cpu block; None when not sampled."""
    peak = (cpu_block.get("rss_kb", {}) or {}).get("peak", {}) or {}
    if not peak:
        return None
    return sum(float(v or 0) for v in peak.values())


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--csv", required=True)
    ap.add_argument("--log", required=True)
    ap.add_argument("--cpu", required=True)
    ap.add_argument("--scenario", required=True)
    ap.add_argument("--load-key", default="")
    ap.add_argument("--commit", required=True)
    ap.add_argument("--dirty", default="")
    ap.add_argument("--topology", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--baseline", default="")
    ap.add_argument("--write-baseline", action="store_true")
    args = ap.parse_args()

    csv_path = Path(args.csv)
    log_path = Path(args.log)
    cpu_path = Path(args.cpu)
    out_path = Path(args.out)
    baseline_path = Path(args.baseline) if args.baseline else None

    if not csv_path.exists():
        print(f"load-local: no request CSV at {csv_path} (run failed?)", file=sys.stderr)
        return 2

    req = summarize_csv(csv_path)
    gen = parse_generator_log(log_path)
    window_s = gen.get("window_s") or req.get("csv_window_s") or 0.0

    scheduled = gen.get("scheduled", req["total"])
    issued = gen.get("issued", req["total"])
    dropped = gen.get("dropped", 0)
    missed = gen.get("missed", 0)
    completed = gen.get("completed", req["ok"])
    peak = gen.get("peak", 0)

    offered_rps = (scheduled / window_s) if window_s else 0.0
    effective_rps = (completed / window_s) if window_s else 0.0
    missed_pct = (100.0 * missed / scheduled) if scheduled else 0.0

    cpu = json.loads(cpu_path.read_text()) if cpu_path.exists() else {}
    nproc = int(cpu.get("nproc", 1) or 1)
    platform_s = float(cpu.get("platform_s", 0.0))
    generator_s = float(cpu.get("generator_s", 0.0))
    roles = cpu.get("roles", {}) or {}
    rss = cpu.get("rss_kb", {}) or {}
    platform_pct = (100.0 * platform_s / (window_s * nproc)) if window_s else 0.0
    generator_pct = (100.0 * generator_s / window_s) if window_s else 0.0

    lat = req["latency_ms"]
    record = {
        "schema": 1,
        "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "commit": args.commit,
        "dirty": bool(args.dirty),
        "scenario": args.scenario,
        "topology": args.topology,
        "load_key": args.load_key,
        "window_s": round(window_s, 3),
        "metrics": {
            "scheduled": scheduled,
            "issued": issued,
            "dropped": dropped,
            "missed": missed,
            "completed": completed,
            "peak_in_flight": peak,
            "offered_rps": offered_rps,
            "effective_rps": effective_rps,
            "missed_pct": missed_pct,
            "success_pct": req["success_pct"],
            "cold": req["cold"],
            "requests": req["total"],
            "p50_ms": lat["p50"],
            "p95_ms": lat["p95"],
            "p99_ms": lat["p99"],
            "max_ms": lat["max"],
        },
        "cpu": {
            "nproc": nproc,
            "platform_s": platform_s,
            "platform_pct": platform_pct,
            "generator_s": generator_s,
            "generator_pct_one_core": generator_pct,
            "roles_s": roles,
            "rss_kb": rss,
        },
    }
    out_path.write_text(json.dumps(cleandata(record), indent=2, allow_nan=False) + "\n")

    dirty = " +dirty" if args.dirty else ""
    print(f"== load-local: {args.scenario} | {args.topology} | {args.commit}{dirty} ==")
    print(f"offered {fmt(offered_rps,1)} rps  effective {fmt(effective_rps,1)} rps  window {fmt(window_s,1)}s")
    print(
        f"scheduled={scheduled} issued={issued} dropped={dropped} "
        f"missed={missed} ({fmt(missed_pct,2)}%) completed={completed}"
    )
    print(
        f"success {fmt(req['success_pct'],2)}%  cold {req['cold']}  "
        f"p50={fmt(lat['p50'])} p95={fmt(lat['p95'])} p99={fmt(lat['p99'])} "
        f"max={fmt(lat['max'])} ms"
    )
    role_bits = " ".join(f"{k}={fmt(v,1)}s" for k, v in sorted(roles.items()) if v >= 0.05)
    print(
        f"cpu platform={fmt(platform_s,1)}s ({fmt(platform_pct,1)}% of {nproc} cores)  "
        f"generator={fmt(generator_s,2)}s ({fmt(generator_pct,1)}% of 1 core)"
    )
    print(f"    {role_bits}".rstrip())
    peak_mib = rss_total_peak(record["cpu"])
    if peak_mib is not None:
        rpeak = (rss.get("peak", {}) or {})
        rbits = " ".join(f"{k}={float(v or 0)/1024:.0f}MiB" for k, v in sorted(rpeak.items()))
        print(f"rss peak total {peak_mib/1024:.0f} MiB  ({rbits})")
    print(f"result {out_path}  requests {csv_path}")
    print("caveat: fake echo sandbox (synthetic sandbox time); real control plane, routing,"
          " admission, autoscaling and proxy; generator and platform share this host")

    if args.write_baseline and baseline_path:
        baseline_path.parent.mkdir(parents=True, exist_ok=True)
        baseline_path.write_text(json.dumps(cleandata(record), indent=2, allow_nan=False) + "\n")
        print(f"baseline written: {baseline_path}")

    if baseline_path and baseline_path.exists() and not args.write_baseline:
        base = json.loads(baseline_path.read_text())
        if (
            base.get("scenario") != record["scenario"]
            or base.get("topology") != record["topology"]
            or base.get("load_key") != record["load_key"]
        ):
            print(
                f"baseline {baseline_path} is for scenario={base.get('scenario')} "
                f"topology={base.get('topology')} load={base.get('load_key') or '?'}; "
                "skipping comparison (re-run with LOAD_LOCAL_WRITE_BASELINE=1 to reset)"
            )
            return 0
        bmetrics = base.get("metrics", {})
        cmetrics = record["metrics"]
        bcpu, ccpu = base.get("cpu", {}), record["cpu"]

        def pct(cur, old):
            if cur is None or old is None or old == 0:
                return None
            return 100.0 * (cur - old) / old

        def relative_verdict(delta, higher_better):
            if delta is None:
                return "n/a"
            worse = -delta if higher_better else delta
            if worse <= -5:
                return "improved"
            if worse < 15:
                return "ok"
            if worse < 50:
                return "warn"
            return "bad"

        def miss_verdict(cur):
            # Absolute budget: a handful of generator misses is scheduling noise.
            if cur is None:
                return "n/a"
            if cur <= 0.1:
                return "ok"
            if cur <= 1.0:
                return "warn"
            return "bad"

        b_rss = rss_total_peak(bcpu)
        c_rss = rss_total_peak(ccpu)
        b_rss = b_rss / 1024 if b_rss is not None else None
        c_rss = c_rss / 1024 if c_rss is not None else None
        rows = [
            ("eff", bmetrics.get("effective_rps"), cmetrics["effective_rps"], True, "", False),
            ("p50", bmetrics.get("p50_ms"), cmetrics["p50_ms"], False, "ms", False),
            ("p95", bmetrics.get("p95_ms"), cmetrics["p95_ms"], False, "ms", False),
            ("p99", bmetrics.get("p99_ms"), cmetrics["p99_ms"], False, "ms", False),
            ("succ", bmetrics.get("success_pct"), cmetrics["success_pct"], True, "%", False),
            ("miss", bmetrics.get("missed_pct"), cmetrics["missed_pct"], False, "%", True),
            ("cpu", bcpu.get("platform_s"), ccpu.get("platform_s"), False, "s", False),
            ("rss", b_rss, c_rss, False, "MiB", False),
        ]
        verdicts = []
        bits = []
        for name, old, cur, higher_better, unit, absolute in rows:
            if absolute:
                verdict = miss_verdict(cur)
                delta_txt = "n/a" if (old is None or cur is None) else f"{cur - old:+.2f}pp"
            else:
                delta = pct(cur, old)
                verdict = relative_verdict(delta, higher_better)
                sign = "" if delta is None else ("+" if delta >= 0 else "")
                delta_txt = "n/a" if delta is None else f"{sign}{fmt(delta,1)}%"
            verdicts.append(verdict)
            bits.append(f"{name} {fmt(cur,2)}{unit}({delta_txt}) {verdict}")
        if "bad" in verdicts:
            overall = "bad"
        elif "warn" in verdicts:
            overall = "warn"
        else:
            overall = "ok"
        print(f"vs baseline {baseline_path.name} ({base.get('commit','?')}): " + " | ".join(bits))
        print(f"verdict: {overall}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
